package server

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Memory hygiene — the §6.8 checks, hosted inside wiki_health.
//
// §6.5's argument for one tool rather than four applies here too: these answer the same question
// the existing checks answer ("what needs attention?"), and an agent that has to make six calls to
// ask it will make none.
//
// Everything here is deterministic and dependency-free. §6.6 (hybrid semantic search) stays
// tabled, so no check may depend on embeddings — which rules out true contradiction detection and
// is why the duplicate check is deliberately framed as "worth a look", not "these disagree".
//
// Deliberately NOT implemented: stored confidence or decay scores. §6.8 floats them, but a numeric
// confidence field is a schema addition to OKF documents that nothing else consumes, and access
// recency already carries the signal without inventing a number nobody can calibrate.

// defaultColdMemoryDays is how long a memory may go unread and unedited before it is worth
// revisiting. Longer than the 30-day plan threshold on purpose: a plan is work in flight and going
// quiet is meaningful, whereas a memory is a durable fact and not consulting it for a month is
// completely normal.
const defaultColdMemoryDays = 90

// duplicateTitleOverlap is the share of significant title tokens two memories must share before
// the report suggests looking at them together.
//
// Tuned against the real corpus. At 0.5 the two format-template memories
// ("Programming Language Article Format & Template" / "SQL Dialect Article Format & Template")
// pair up — they share Article/Format/Template and genuinely are parallel documents — while
// unrelated memories in the same scope do not. Lower values start pairing anything sharing one
// common word.
const duplicateTitleOverlap = 0.5

// maxDuplicateScopeSize caps the pairwise comparison. Comparison is O(n²) within a scope, and the
// large-corpus benchmarks (§13.8) put 10,000 documents in reach, so a scope past this size is
// skipped rather than allowed to dominate the report's runtime. Every other check is linear.
const maxDuplicateScopeSize = 300

// titleStopWords are words too common in this corpus to indicate similarity. Kept deliberately
// small: an aggressive list starts discarding the words that actually distinguish documents.
var titleStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "the": true, "for": true, "of": true,
	"to": true, "in": true, "on": true, "with": true, "or": true, "vs": true,
	"nexwiki": true, // in this wiki it is on everything, so it separates nothing
}

// DuplicateMemoryPair is two memories in the same scope similar enough to be worth reviewing
// together. It is a prompt to look, not an assertion that they conflict — see the file comment.
type DuplicateMemoryPair struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	OtherSlug string `json:"other_slug"`
	// Scope is the memory-<scope> tag both share, or "" for unscoped memories.
	Scope  string `json:"scope,omitempty"`
	Detail string `json:"detail"`
}

// significantTitleTokens lowercases a title and drops punctuation, stop words, and single
// characters, leaving the words that carry meaning.
func significantTitleTokens(title string) map[string]bool {
	tokens := map[string]bool{}
	isSeparator := func(r rune) bool {
		alphanumeric := ('a' <= r && r <= 'z') || ('0' <= r && r <= '9')
		return !alphanumeric
	}
	for _, raw := range strings.FieldsFunc(strings.ToLower(title), isSeparator) {
		if len(raw) < 2 || titleStopWords[raw] {
			continue
		}
		tokens[raw] = true
	}
	return tokens
}

// titleOverlap is the Jaccard-style overlap of two token sets, measured against the *smaller* set.
//
// Against the smaller set rather than the union on purpose: "SQL Dialect Article Format" and
// "SQL Dialect Article Format & Template Reference Guide" describe the same thing, and union-based
// similarity punishes the longer title for being more descriptive.
func titleOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for token := range a {
		if b[token] {
			shared++
		}
	}
	smaller := len(a)
	if len(b) < smaller {
		smaller = len(b)
	}
	return float64(shared) / float64(smaller)
}

// memoryScope returns the memory-<scope> tag a memory carries, or "" when it is unscoped.
func memoryScope(tags []string) string {
	for _, tag := range tags {
		if lower := strings.ToLower(tag); strings.HasPrefix(lower, "memory-") {
			return strings.TrimPrefix(lower, "memory-")
		}
	}
	return ""
}

// findDuplicateMemories pairs up memories within a scope whose titles overlap heavily.
//
// Scoped, because a "Deployment Notes" memory about `docker` and one about `nexwiki` are supposed
// to be separate documents. Comparing across scopes would report the scoping system working as
// intended, which is the mistake §6.5 already made once with orphan detection.
//
// outbound is the link graph's outbound edges, used to suppress pairs that already reference each
// other — see crossLinked.
func findDuplicateMemories(memories []Article, outbound map[string][]WikiLinkRef) []DuplicateMemoryPair {
	byScope := map[string][]Article{}
	for _, m := range memories {
		scope := memoryScope(m.Tags)
		byScope[scope] = append(byScope[scope], m)
	}

	scopes := make([]string, 0, len(byScope))
	for scope := range byScope {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)

	pairs := []DuplicateMemoryPair{}
	for _, scope := range scopes {
		group := byScope[scope]
		if len(group) > maxDuplicateScopeSize {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].Slug < group[j].Slug })

		tokens := make([]map[string]bool, len(group))
		for i, m := range group {
			tokens[i] = significantTitleTokens(m.Title)
		}

		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if titleOverlap(tokens[i], tokens[j]) < duplicateTitleOverlap {
					continue
				}
				if crossLinked(outbound, group[i].Slug, group[j].Slug) {
					continue
				}
				pairs = append(pairs, DuplicateMemoryPair{
					Slug:      group[i].Slug,
					Title:     group[i].Title,
					OtherSlug: group[j].Slug,
					Scope:     scope,
					Detail: fmt.Sprintf("Closely resembles '%s'. Check whether they agree; if so merge them with "+
						"edit_agent_memory, and if they conflict fix the wrong one rather than leaving both.", group[j].Title),
				})
			}
		}
	}
	return pairs
}

// crossLinked reports whether either document WikiLinks the other.
//
// A deliberate suppression, and the real corpus is what argued for it. The only pair the check
// found there was "Programming Language Article Format & Template" and "SQL Dialect Article Format
// & Template" — genuinely parallel documents, and the first one ends with
// "See also: [[sql-dialect-article-format-template]]". An author who has linked two documents
// together already knows both exist and has decided to keep them separate; telling them to consider
// merging is telling them something they have answered. That took the check from one false positive
// on the live wiki to zero.
func crossLinked(outbound map[string][]WikiLinkRef, a, b string) bool {
	links := func(from, to string) bool {
		for _, ref := range outbound[from] {
			if ref.Slug == to {
				return true
			}
		}
		return false
	}
	return links(a, b) || links(b, a)
}

// coldMemoryScan holds the result of the recency check, including whether it was able to run.
//
// The "able to run" part is the whole design. Recency comes from the activity log, and on a fresh
// install — or after NEXWIKI_ACTIVITY_MAX_ARCHIVES pruning — the log may be younger than the
// threshold. In that case *every* memory looks untouched, and reporting all of them is worse than
// reporting none: it is §6.5's "a check that fires on 84% of the wiki is noise an agent learns to
// skip", except here the report would be entirely false.
type coldMemoryScan struct {
	Findings []HealthFinding
	// Ran is false when the activity log does not span the threshold, so the report can say why it
	// stayed quiet rather than implying every memory is fine.
	Ran bool
	// LogSpanDays is how far back the log actually reaches, reported when Ran is false.
	LogSpanDays int
}

// scanColdMemories finds memories neither read nor edited within coldDays.
//
// Reads count. The activity log records `read` for every non-mutating tool call, so a memory the
// agent keeps consulting is demonstrably alive even if nobody has edited it in a year — which is
// exactly what a good memory looks like. Only a memory nobody has touched at all is a candidate
// for review.
func scanColdMemories(logPath string, memories []Article, coldDays int) coldMemoryScan {
	cutoff := time.Now().AddDate(0, 0, -coldDays)

	events, err := ReadActivityLogFiltered(logPath, ActivityFilter{})
	if err != nil || len(events) == 0 {
		return coldMemoryScan{Findings: []HealthFinding{}, Ran: false}
	}

	// ReadActivityLogFiltered returns oldest-first, so the first event bounds the log's reach.
	earliest := events[0].Timestamp
	if earliest.After(cutoff) {
		// Findings is an empty slice rather than nil so the payload always carries an array; a
		// null where a client expects a list is the kind of thing that breaks a consumer for no
		// reason.
		return coldMemoryScan{Findings: []HealthFinding{}, Ran: false,
			LogSpanDays: int(time.Since(earliest).Hours() / 24)}
	}

	lastTouched := map[string]time.Time{}
	for _, ev := range events {
		if ev.Slug == "" {
			continue
		}
		if prev, ok := lastTouched[ev.Slug]; !ok || ev.Timestamp.After(prev) {
			lastTouched[ev.Slug] = ev.Timestamp
		}
	}

	findings := []HealthFinding{}
	for _, m := range memories {
		// A memory written after the cutoff is new, not cold, even with no activity recorded.
		if m.Timestamp.After(cutoff) {
			continue
		}
		if touched, ok := lastTouched[m.Slug]; ok && touched.After(cutoff) {
			continue
		}

		since := m.Timestamp.Format("2006-01-02")
		if touched, ok := lastTouched[m.Slug]; ok {
			since = touched.Format("2006-01-02")
		}
		findings = append(findings, HealthFinding{
			Slug: m.Slug, Title: m.Title, Type: m.Type,
			Detail: fmt.Sprintf("Not read or edited since %s. Confirm it is still true, or archive it — "+
				"a memory nothing consults is either settled knowledge or quietly wrong.", since),
		})
	}
	return coldMemoryScan{Findings: findings, Ran: true}
}
