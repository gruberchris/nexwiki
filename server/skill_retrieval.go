package server

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// This file holds story 11 (Phase B3): the retrieval-vs-injection toggle for
// evolution jobs.
//
// THE KNOB: a job carries `injection_mode` in its config — "all" (the default;
// the payload's data block stays exactly what the harness supplies, i.e. the
// paper's full-injection baseline) or "retrieve". Retrieve mode is a COARSE
// SERVER-SIDE PRE-FILTER, not a full retrieval system: the runner ranks the
// skill registry's name/description front matter against the task's keywords
// with a small BM25 (token-space IDF weighting over the task's own tokens)
// and quotes only the top-K selected skills into the stdin payload, alongside
// the skill under evolution, which is always injected. The skill BODIES ride
// quoted-as-data exactly like today's Data block — never instructions, never
// argv, never env.
//
// HONEST LIMITS: the ranking sees name + description only, not bodies; it
// runs per step against the task text (stable within an iteration); and when
// nothing matches (an empty task, or every skill scoring zero) the subset
// degrades to the skill under evolution alone and the trace says so. Full
// injection remains the default and the default remains unchanged.
//
// CONFIGURATION: the only new env is NEXWIKI_JOB_RETRIEVAL_TOPK (default 5),
// parsed fail-closed like every gate threshold — a set-but-unparseable value
// is an error naming the variable, refused at runner construction AND at step
// time, never silently ignored.

// Injection modes. All is the default: no server-side filtering, unchanged
// story-04 behavior.
const (
	InjectionModeAll      = "all"
	InjectionModeRetrieve = "retrieve"
)

// Retrieval ranking knobs.
const (
	JobRetrievalTopKEnv  = "NEXWIKI_JOB_RETRIEVAL_TOPK"
	DefaultRetrievalTopK = 5
)

// BM25 parameters (standard values, documented so the trace is reproducible).
const (
	retrievalK1 = 1.2
	retrievalB  = 0.75
)

// normalizeInjectionMode validates a job config's injection mode. Empty means
// the default; anything beyond the two values is a refusal naming the choice.
func normalizeInjectionMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return InjectionModeAll, nil
	}
	if m == InjectionModeAll || m == InjectionModeRetrieve {
		return m, nil
	}
	return "", fmt.Errorf("invalid injection_mode %q: must be %q (default — the payload carries exactly what the harness supplies) or %q (server-side pre-filter: top-K skills matching the task keywords, plus the skill under evolution)",
		mode, InjectionModeAll, InjectionModeRetrieve)
}

// injectionModeFor resolves a job's effective mode. Records that predate the
// toggle read "all"; a hand-edited invalid value falls back to "all" (the
// unchanged, safe behavior) rather than wedging the run.
func injectionModeFor(job *EvolutionJob) string {
	if m, err := normalizeInjectionMode(job.InjectionMode); err == nil {
		return m
	}
	return InjectionModeAll
}

// retrievalTopK reads the top-K override. Fail-closed, like the eval split
// floors: a typo in the env must not silently widen or disable the filter.
func retrievalTopK() (int, error) {
	raw := strings.TrimSpace(os.Getenv(JobRetrievalTopKEnv))
	if raw == "" {
		return DefaultRetrievalTopK, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid retrieval configuration: %s=%q must be a positive integer (default %d)",
			JobRetrievalTopKEnv, raw, DefaultRetrievalTopK)
	}
	return n, nil
}

// InjectedSkill is one selected skill in the payload's injection block. The
// body is untrusted wiki content — quoted as data, exactly like the Data
// block's excerpts.
type InjectedSkill struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Body        string  `json:"body,omitempty"`
	Score       float64 `json:"score,omitempty"`
	// Self marks the skill under evolution: always injected regardless of rank.
	Self bool `json:"self,omitempty"`
}

// SkillInjectionBlock is the story-11 stdin payload extension: the selected
// skill subset plus the ranking trace. The whole block is quoted-as-data on
// the wire — the payload's instruction_boundary label covers it the same way
// it covers the Data block, and a shim must forward it quoted, never as
// instructions.
type SkillInjectionBlock struct {
	Mode   string          `json:"mode"`
	Skills []InjectedSkill `json:"skills,omitempty"`
	Trace  string          `json:"ranking_trace"`
}

// RetrievalTrace is the per-iteration record of one retrieve-mode selection,
// stamped on the iteration record by the loop stepper so the wizard can show
// why the payload carried the skills it did.
type RetrievalTrace struct {
	Mode     string   `json:"mode"`
	TopK     int      `json:"top_k,omitempty"`
	Selected []string `json:"selected,omitempty"`
	Trace    string   `json:"trace,omitempty"`
}

// retrievalDoc is one ranked skill: name/description term counts plus length.
type retrievalDoc struct {
	slug  string
	title string
	desc  string
	tf    map[string]int
	dl    int
}

// retrievalTermCounts folds text into lowercase alphanumeric term counts —
// the same tokenization the scorer sandbox's overlap() uses.
func retrievalTermCounts(s string) map[string]int {
	out := map[string]int{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if tok != "" {
			out[tok]++
		}
	}
	return out
}

// selectSkillsForRetrieval builds the retrieve-mode injection block: BM25
// over every registry skill's name/description against the task's keywords,
// top-K selection, the evolving skill pinned first, and a deterministic
// ranking trace. Registry skills beyond the cut — and anything scoring zero
// overlap — stay out of the payload.
func (s *Storage) selectSkillsForRetrieval(skillSlug, task string, topK int) (*SkillInjectionBlock, error) {
	articles, err := s.ListArticles()
	if err != nil {
		return nil, fmt.Errorf("retrieve-mode injection failed to list the skill registry: %w", err)
	}

	var docs []retrievalDoc
	for i := range articles {
		meta := &articles[i]
		if meta.Type != ContentTypeSkill || !meta.ArchivedAt.IsZero() {
			continue
		}
		art, err := s.GetArticle(meta.Slug)
		if err != nil {
			continue // unparseable skills drop out, as in every other listing
		}
		// Terms come from the skill's name/description front matter, with the
		// slug's own words folded in once ("docker-clean" → docker, clean).
		text := art.Title + " " + art.Slug + " " + art.Description
		tf := retrievalTermCounts(text)
		dl := 0
		for _, n := range tf {
			dl += n
		}
		docs = append(docs, retrievalDoc{slug: art.Slug, title: art.Title, desc: art.Description, tf: tf, dl: dl})
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("retrieve-mode injection found no Custom AI Skills in the registry")
	}

	// BM25, standard form: idf(t) = ln(1 + (N - df + 0.5)/(df + 0.5)); the +1
	// keeps every term's weight positive. Query terms are the task's tokens,
	// each counted once.
	query := retrievalTermCounts(task)
	avgdl := 0.0
	for _, d := range docs {
		avgdl += float64(d.dl)
	}
	avgdl /= float64(len(docs))
	idf := func(term string) float64 {
		df := 0
		for _, d := range docs {
			if d.tf[term] > 0 {
				df++
			}
		}
		return math.Log(1 + (float64(len(docs)-df)+0.5)/(float64(df)+0.5))
	}
	type scoredDoc struct {
		doc   retrievalDoc
		score float64
	}
	var ranked []scoredDoc
	for _, d := range docs {
		score := 0.0
		for term := range query {
			tf := float64(d.tf[term])
			if tf == 0 {
				continue
			}
			denom := tf + retrievalK1*(1-retrievalB+retrievalB*float64(d.dl)/avgdl)
			score += idf(term) * tf * (retrievalK1 + 1) / denom
		}
		if score > 0 {
			ranked = append(ranked, scoredDoc{doc: d, score: score})
		}
	}
	// Deterministic order: score descending, then slug ascending.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].doc.slug < ranked[j].doc.slug
	})

	block := &SkillInjectionBlock{Mode: InjectionModeRetrieve}
	degraded := len(ranked) == 0
	pinned := false
	cut := 0
	for _, rd := range ranked {
		self := rd.doc.slug == skillSlug
		if self {
			pinned = true
		}
		if len(block.Skills) >= topK && !self {
			break
		}
		cut++
		block.Skills = append(block.Skills, InjectedSkill{
			Slug:        rd.doc.slug,
			Name:        rd.doc.title,
			Description: rd.doc.desc,
			Score:       roundScore4(rd.score),
			Self:        self,
		})
	}
	// The skill under evolution is always injected, appended after the
	// retrieved subset (it is the subject, not a retrieval hit). A zero
	// keyword overlap — an empty task, or a task about nothing in the registry
	// — degrades the subset to the skill under evolution alone, and the trace
	// says so instead of silently injecting nothing.
	if !pinned {
		selfScore := 0.0
		for _, rd := range ranked {
			if rd.doc.slug == skillSlug {
				selfScore = rd.score
				break
			}
		}
		self, err := s.GetArticle(skillSlug)
		if err != nil {
			return nil, fmt.Errorf("retrieve-mode injection: skill under evolution not found: %w", err)
		}
		block.Skills = append(block.Skills, InjectedSkill{
			Slug: self.Slug, Name: self.Title, Description: self.Description,
			Score: roundScore4(selfScore), Self: true,
		})
	}

	// Bodies ride last, quoted as data, in selection order.
	for i := range block.Skills {
		art, err := s.GetArticle(block.Skills[i].Slug)
		if err != nil {
			return nil, fmt.Errorf("retrieve-mode injection: skill '%s' unreadable: %w", block.Skills[i].Slug, err)
		}
		block.Skills[i].Body = art.Content
	}

	// Ranking trace: what the filter saw, what it kept, and why. Bodies and
	// case data are never quoted into it.
	var b strings.Builder
	fmt.Fprintf(&b, "retrieve mode: ranked %d registry skills against the task keywords (BM25 over name/description, k1=%.1f b=%.2f), top-%d plus the skill under evolution",
		len(docs), retrievalK1, retrievalB, topK)
	fmt.Fprintf(&b, "; selected %d of %d scored", len(block.Skills), len(ranked))
	if excluded := len(ranked) - cut; excluded > 0 {
		fmt.Fprintf(&b, "; excluded %d (below the rank cut or zero keyword overlap)", excluded)
	}
	if degraded {
		b.WriteString("; no keyword match — degraded to the skill under evolution alone")
	}
	b.WriteString("\n")
	for i, sk := range block.Skills {
		label := ""
		if sk.Self {
			label = " (self)"
		}
		fmt.Fprintf(&b, "%d. %s score %.4f%s\n", i+1, sk.Slug, sk.Score, label)
	}
	block.Trace = b.String()
	return block, nil
}
