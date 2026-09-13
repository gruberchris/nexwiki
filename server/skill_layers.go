package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file holds story 10 of the wikiskill evolution support plan: the
// three-layer guards (Phase B1). WikiSkill keeps three layers distinct, and the
// asymmetry between them is the central design choice — permanent memory versus
// reversible action — so the storage semantics have to enforce it, not merely
// document it.
//
//   - RAW layer — immutable. The evolution artifacts (job records
//     data/skill_jobs/*.json, everything under <id>.files/ — captured runner
//     output, artifacts, eval splits, loop.json, iterations/ —, candidate
//     records data/skill_candidates/*.json, and the audit trail
//     data/skill_audit.jsonl) are evidence for later steps: never rewritten,
//     never reverted, never deleted. They live outside the article tree, and
//     Slugify confines every article read/write/delete to data/articles, so no
//     agent-facing article path can reach them; the tests below hold that
//     invariant. What agents COULD do before this story is tamper on disk —
//     stories 04–05 verified hashes only at upload. ScanRawArtifactIntegrity
//     extends hash verification to a standing scan: every stored artifact,
//     eval split, and decided candidate is re-hashed against its recorded
//     hash, corrupt records and broken references are flagged, and findings
//     surface in two places — a Stderr warning at startup and a wiki_health
//     category. Never silent, never fatal.
//
//   - WIKI layer — accumulative. Articles tagged with the tool-managed
//     `wikiskill-wiki` marker (every generated Trained Skill Result report is
//     stamped with it at creation) are pattern/log pages the evolution loop
//     writes. Agents may patch and append them — the wiki never resets — but
//     revert_article_version and delete_wiki_article refuse them with a
//     WikiScopeError. The refusal lives at the MCP tool layer, the same
//     boundary the story-09 report immutability used, so the human-facing REST
//     editor keeps its documented amend path. CleanupArchivedArticles skips
//     wiki-scope articles: NO auto-deletion, ever. Staleness is declared with
//     the OKF stale_after front-matter key (already supported by the create
//     and edit tools) and surfaced by wiki_health's stale-concepts check;
//     supersession is declared with the superseded_by front-matter key and
//     reported by wiki_health as the chain. Nothing is ever pruned
//     automatically — wiki_health reports, a human decides.
//
//   - SKILLS layer — reversible but purpose-linked. A skill trained through
//     the evolution flow should carry a PURPOSE link back to the motivating
//     wiki patterns, the way WikiSkill's PURPOSE.md maps a skill to the
//     patterns that drove it. wiki_health flags trained skills with zero
//     pattern backlinks as a WARNING: a link in either direction between the
//     skill and a wiki-scope article counts, and the story-09 report's own
//     Run-table link to its skill is exempt via its `wikiskill-report` marker
//     — it is boilerplate every run produces, not recorded motivation.

// WikiskillWikiTag is the tool-managed marker tag for wiki-scope articles —
// the wiki layer the evolution loop accumulates into (patterns, logs, and the
// generated Trained Skill Result reports). Agents may set it on a wiki article
// (it only restricts the article they set it on) and may never strip it from
// one that carries it; DeleteTagGlobally refuses it, and saveArticleLocked
// re-asserts it the way it re-asserts the trained and report markers.
const WikiskillWikiTag = "wikiskill-wiki"

// ensureWikiScopeTag returns tags with the wiki-scope marker asserted,
// deduped case-insensitively. It always copies so a caller's tag slice is
// never mutated through a shared backing array.
func ensureWikiScopeTag(tags []string) []string {
	if hasTag(tags, WikiskillWikiTag) {
		return tags
	}
	out := make([]string, len(tags), len(tags)+1)
	copy(out, tags)
	return append(out, WikiskillWikiTag)
}

// isWikiScopeArticle reports whether art belongs to the wiki layer: tagged
// with the `wikiskill-wiki` marker. Generated result reports carry the marker
// too, so the scope is one tag.
func isWikiScopeArticle(art *Article) bool {
	return art != nil && art.Type == ContentTypeWiki && hasTag(art.Tags, WikiskillWikiTag)
}

// isReportArticle reports whether art is a wiki-generated Trained Skill
// Result report (story 09). Used to exempt the report's Run-table link to its
// skill from the PURPOSE backlink count — it is not a motivating pattern.
func isReportArticle(art *Article) bool {
	return art != nil && art.Type == ContentTypeWiki && hasTag(art.Tags, SkillResultReportTag)
}

// WikiScopeError reports a refused agent revert or delete of a wiki-scope
// article. The wiki layer is accumulative: patching and appending stay open,
// rolling back and deleting do not.
type WikiScopeError struct {
	Slug   string
	Action string
}

func (e *WikiScopeError) Error() string {
	return fmt.Sprintf("article '%s' is a WikiSkill wiki-scope article: the wiki layer is accumulative — agents may edit (patch/append) it but must not roll it back or delete it, because even a superseded pattern leaves its lesson recorded; %s refused — mark it superseded with the superseded_by front-matter key instead", e.Slug, e.Action)
}

// checkWikiScopeAccumulative returns a *WikiScopeError when art is a
// wiki-scope article and action would rewrite or remove it, nil otherwise.
// Called by the agent-facing MCP write tools (revert_article_version,
// delete_wiki_article); the human-facing REST editor deliberately does not
// call it, mirroring the story-09 report-immutability boundary.
func checkWikiScopeAccumulative(art *Article, action string) error {
	if isWikiScopeArticle(art) {
		return &WikiScopeError{Slug: art.Slug, Action: action}
	}
	return nil
}

// RawIntegrityFinding is one raw-layer artifact the integrity scan cannot
// account for: a hash that no longer matches, a record that no longer parses,
// or a reference to a record that vanished.
type RawIntegrityFinding struct {
	// Kind names what was checked: "artifact", "eval", "candidate", "audit",
	// "job", "loop", or "iteration".
	Kind string
	// ID is the owning record's identifier — the job ID, or the candidate ID.
	ID string
	// Path is the artifact's location relative to the data directory, the
	// form the operator needs to inspect or restore the file.
	Path string
	// Detail explains the finding and how to act on it.
	Detail string
}

// dataRelPath renders a path relative to the data directory for findings. A
// failure falls back to the absolute path — a finding must never lose its
// location over a filepath.Rel error.
func dataRelPath(dataDir, path string) string {
	if rel, err := filepath.Rel(dataDir, path); err == nil {
		return rel
	}
	return path
}

// ScanRawArtifactIntegrity walks every raw-layer artifact and verifies it
// against the hashes the wiki recorded when it accepted the data:
//
//   - each job artifact under <id>.files/artifacts/<name> against its
//     story-04 ContentHash (and Size);
//   - each uploaded eval split against the job's story-05 EvalHash;
//   - each decided (promoted or rejected) candidate record against the
//     content hash its audit decision recorded (story 03);
//   - each gate audit record against the candidate record it decided;
//   - corrupt job/candidate/loop/iteration/audit records that other read
//     paths silently skip.
//
// It is a read-only scan: it mutates nothing and repairs nothing — a flagged
// artifact stays flagged until a human restores or removes it. Absence of a
// loop state or iteration directory is normal (a job that never entered the
// loop produces neither) and never a finding.
func (s *Storage) ScanRawArtifactIntegrity() ([]RawIntegrityFinding, error) {
	findings := []RawIntegrityFinding{}

	auditByCandidate := map[string]SkillAuditRecord{}
	records, err := s.ListSkillAuditRecords()
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		auditByCandidate[rec.CandidateID] = rec
	}
	// Corrupt audit lines vanish silently from ListSkillAuditRecords; the raw
	// line count makes them visible.
	if corrupt, cerr := s.corruptAuditLineCount(); cerr == nil && corrupt > 0 {
		findings = append(findings, RawIntegrityFinding{
			Kind:   "audit",
			ID:     SkillAuditFilename,
			Path:   dataRelPath(s.DataDir, SkillAuditPath(s.DataDir)),
			Detail: fmt.Sprintf("%d line(s) of the append-only audit trail no longer parse as JSON — entries may have been altered or truncated; compare against a backup before trusting the trail", corrupt),
		})
	}

	candidateIDs := map[string]bool{}
	entries, err := os.ReadDir(s.CandidateDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		c, err := s.GetSkillCandidate(id)
		if err != nil {
			findings = append(findings, RawIntegrityFinding{
				Kind: "candidate", ID: id,
				Path:   dataRelPath(s.DataDir, filepath.Join(s.CandidateDir, e.Name())),
				Detail: fmt.Sprintf("candidate record no longer parses as JSON (%v) — the proposal data may have been altered", err),
			})
			continue
		}
		candidateIDs[c.ID] = true
		if c.Status == SkillCandidatePending {
			continue // never gated: no recorded hash to verify against yet
		}
		rec, ok := auditByCandidate[c.ID]
		if !ok {
			findings = append(findings, RawIntegrityFinding{
				Kind: "candidate", ID: c.ID,
				Path:   dataRelPath(s.DataDir, filepath.Join(s.CandidateDir, e.Name())),
				Detail: fmt.Sprintf("candidate is decided (%s) but the audit trail holds no decision record for it — the decision may have been removed", c.Status),
			})
			continue
		}
		if want, got := rec.ContentHash, SkillCandidateContentHash(c); want != got {
			findings = append(findings, RawIntegrityFinding{
				Kind: "candidate", ID: c.ID,
				Path:   dataRelPath(s.DataDir, filepath.Join(s.CandidateDir, e.Name())),
				Detail: fmt.Sprintf("candidate content changed after the audit decision: the trail records hash %.12s for the %s decision, the record now hashes to %.12s", want, rec.Outcome, got),
			})
		}
	}
	// Gate decisions decide real candidates, and candidates are never deleted
	// by any flow — so a gate record whose candidate file is gone is a loss
	// signal. (Human revoke/unlock records may carry synthetic candidate
	// references instead and are exempt.)
	for _, rec := range records {
		if rec.Decider == SkillAuditDeciderGate && rec.CandidateID != "" && !candidateIDs[rec.CandidateID] {
			findings = append(findings, RawIntegrityFinding{
				Kind: "candidate", ID: rec.CandidateID,
				Path:   dataRelPath(s.DataDir, SkillAuditPath(s.DataDir)),
				Detail: fmt.Sprintf("the audit trail's %s decision for '%s' has no candidate record on disk", rec.Outcome, rec.CandidateID),
			})
		}
	}

	jobEntries, err := os.ReadDir(s.JobDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range jobEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		jobID := strings.TrimSuffix(e.Name(), ".json")
		job, err := s.GetEvolutionJob(jobID)
		if err != nil {
			findings = append(findings, RawIntegrityFinding{
				Kind: "job", ID: jobID,
				Path:   dataRelPath(s.DataDir, filepath.Join(s.JobDir, e.Name())),
				Detail: fmt.Sprintf("job record no longer parses as JSON (%v) — the run record may have been altered", err),
			})
			continue
		}

		filesDir, ferr := s.jobFilesDir(job.ID)
		if ferr != nil {
			continue // storage not configured; the record itself still parsed
		}

		// Hash-verify every attached artifact.
		for _, art := range job.Artifacts {
			path := filepath.Join(filesDir, "artifacts", art.Name)
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				findings = append(findings, RawIntegrityFinding{
					Kind: "artifact", ID: job.ID, Path: dataRelPath(s.DataDir, path),
					Detail: fmt.Sprintf("artifact '%s' is missing: %v — the run's stored output is incomplete", art.Name, rerr),
				})
				continue
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])
			if got != strings.ToLower(art.ContentHash) {
				findings = append(findings, RawIntegrityFinding{
					Kind: "artifact", ID: job.ID, Path: dataRelPath(s.DataDir, path),
					Detail: fmt.Sprintf("artifact '%s' changed after upload: recorded hash %.12s, file now hashes to %.12s (%d bytes on disk, %d recorded)", art.Name, art.ContentHash, got, len(raw), art.Size),
				})
			} else if int64(len(raw)) != art.Size {
				findings = append(findings, RawIntegrityFinding{
					Kind: "artifact", ID: job.ID, Path: dataRelPath(s.DataDir, path),
					Detail: fmt.Sprintf("artifact '%s' size drifted: %d bytes on disk, %d recorded (hash still matches)", art.Name, len(raw), art.Size),
				})
			}
		}

		// Hash-verify the accepted eval splits.
		if job.EvalHash != "" {
			train, val, _, rerr := s.ReadEvolutionEvalSet(job.ID)
			version := evalScorerVersionOfCases(train, val)
			recomputed := evalSetHash(train, val, version)
			switch {
			case rerr != nil:
				findings = append(findings, RawIntegrityFinding{
					Kind: "eval", ID: job.ID,
					Path:   dataRelPath(s.DataDir, filepath.Join(filesDir, "eval")),
					Detail: fmt.Sprintf("eval data recorded (hash %.12s) but unreadable: %v — the training data of record is incomplete", job.EvalHash, rerr),
				})
			case recomputed != job.EvalHash:
				findings = append(findings, RawIntegrityFinding{
					Kind: "eval", ID: job.ID,
					Path:   dataRelPath(s.DataDir, filepath.Join(filesDir, "eval")),
					Detail: fmt.Sprintf("eval splits changed after upload: recorded hash %.12s, stored splits now hash to %.12s — every gate decision that compared against this set is suspect", job.EvalHash, recomputed),
				})
			}
		}

		// The loop stepper's records are checked for parse integrity; they
		// carry no recorded hashes of their own, so corruption (not a hash
		// mismatch) is what the scan can see.
		if st, err := os.ReadFile(filepath.Join(filesDir, "loop.json")); err == nil {
			var probe LoopState
			if jerr := json.Unmarshal(st, &probe); jerr != nil {
				findings = append(findings, RawIntegrityFinding{
					Kind: "loop", ID: job.ID,
					Path:   dataRelPath(s.DataDir, filepath.Join(filesDir, "loop.json")),
					Detail: fmt.Sprintf("loop state no longer parses as JSON (%v) — the run's position may have been altered", jerr),
				})
			}
		}
		if iters, err := os.ReadDir(filepath.Join(filesDir, "iterations")); err == nil {
			names := make([]string, 0, len(iters))
			for _, it := range iters {
				if it.IsDir() || !strings.HasSuffix(it.Name(), ".json") {
					continue
				}
				raw, rerr := os.ReadFile(filepath.Join(filesDir, "iterations", it.Name()))
				if rerr != nil {
					continue
				}
				var probe IterationRecord
				if jerr := json.Unmarshal(raw, &probe); jerr != nil {
					names = append(names, it.Name())
				}
			}
			if len(names) > 0 {
				findings = append(findings, RawIntegrityFinding{
					Kind: "iteration", ID: job.ID,
					Path:   dataRelPath(s.DataDir, filepath.Join(filesDir, "iterations")),
					Detail: fmt.Sprintf("%d iteration record(s) no longer parse as JSON: %s — the run history may have been altered", len(names), strings.Join(names, ", ")),
				})
			}
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		if findings[i].ID != findings[j].ID {
			return findings[i].ID < findings[j].ID
		}
		return findings[i].Path < findings[j].Path
	})
	return findings, nil
}

// corruptAuditLineCount counts lines of the audit trail that no longer parse
// as JSON — the records ListSkillAuditRecords silently skips.
func (s *Storage) corruptAuditLineCount() (int, error) {
	raw, err := os.ReadFile(SkillAuditPath(s.DataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	corrupt := 0
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe SkillAuditRecord
		if json.Unmarshal([]byte(line), &probe) != nil {
			corrupt++
		}
	}
	return corrupt, nil
}

// logRawIntegrityWarnings writes the startup integrity report to Stderr, per
// the repo logging convention. This is the log half of "never silent": the
// wiki_health category is the other half.
func logRawIntegrityWarnings(findings []RawIntegrityFinding) {
	if len(findings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "Warning: raw artifact integrity scan flagged %d issue(s) — evolution artifacts must never be rewritten; details below and in wiki_health\n", len(findings))
	for _, f := range findings {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: raw artifact integrity [%s] %s %s: %s\n", f.Kind, f.ID, f.Path, f.Detail)
	}
}
