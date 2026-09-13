package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file holds story 12 of the wikiskill evolution support plan (Phase
// E/E5): the Stability S0 pilot fixture — the seeded scenario tables, the
// protocol bodies the pilot trains, the verdict scorer, the E5 success-bar
// math, and the record-driven run summary.
//
// THE PILOT: the intended real pilot trains the KimmyDB Cluster Stability
// Protocol skill (an AI-Agent-Skill in the live wiki) on seeded scenarios and
// checks that training halves the protocol's false-bug rate on held-out val
// cases with zero true-detection loss. A live KimmyDB cluster is NOT
// available (and never required): scenarios are seeded text, the scorer is a
// sandboxed deterministic expression, and every number is computed from
// stored records. This file is the fixture side — the data and the math. The
// in-process demonstration (real eval gate → real jobs → real loop → real
// report under a scripted, deterministic harness shim) lives in
// skill_pilot_test.go behind TestStabilityPilot, and the operator procedure
// for the real run lives in skill_pilot_runbook.go plus the runbook section
// of docs/aiagent_skills.md.
//
// HOW A VERDICT IS SCORED: every scenario carries a stable scenario key. A
// protocol body answers a scenario with one verdict-table directive, and the
// eval case's `expected` field is that exact directive ("<key>: BUG" or
// "<key>: NO-BUG"). The per-case scorer is the sandboxed expression
//
//	contains(lower(output), lower(expected))
//
// which returns 1 exactly when the body carries the scenario's correct
// directive — a registered expression scorer (story 11) measuring verdict
// correctness per scenario, with no model call and no cluster. A val split's
// mean is then the fraction of held-out scenarios the body answers correctly,
// which is what the story 03 gate (via the story 11 eval path) compares
// against R_best, and what the E5 math below decomposes back into false bugs
// and true detections.
//
// WHAT THIS FILE DOES NOT DO: no storage writes beyond the staging helper
// (StagePilotBodies), no MCP tools, no network, no harness process — the
// demonstration wiring and the runbook own those.

// The pilot skill's identity. The slug is the Slugify of the title (asserted
// by the fixture test) so the runbook's create step and the storage agree.
const (
	PilotSkillTitle = "KimmyDB Cluster Stability Protocol"
	PilotSkillSlug  = "kimmydb-cluster-stability-protocol"

	// PilotMarkerPhrase is the stable marker sentence every pilot body
	// carries. It is the citation anchor of the pilot: the runbook quotes it,
	// and the run summary verifies it survived training, so a promoted body
	// that is not the stability protocol cannot pass unnoticed.
	PilotMarkerPhrase = "A verdict without its scenario key is not a verdict, and a key without a verdict is not an answer."

	// PilotSkillDescription is the seeded skill's description.
	PilotSkillDescription = "Triage protocol for KimmyDB cluster test rounds: classify each round's observations into real stability bugs and measurement artefacts."
)

// Pilot harness env names. NEXWIKI_-prefixed per the repo convention — which
// is also what makes them work: the runner's child environment passes every
// NEXWIKI_* variable through to the shim process (skill_runner.go childEnv),
// so the scripted profile needs no env allowlist of its own.
const (
	// PilotShimOptInEnv gates the re-exec'd shim entry: the package test
	// binary skips it unless the value is "1".
	PilotShimOptInEnv = "NEXWIKI_PILOT_SHIM"
	// PilotShimBinEnv names the package test binary the PATH wrapper re-execs.
	PilotShimBinEnv = "NEXWIKI_PILOT_SHIM_BIN"
	// PilotShimDirEnv names the staged pilot directory (bodies + handshake files).
	PilotShimDirEnv = "NEXWIKI_PILOT_SHIM_DIR"
)

// Pilot verdicts. A true-bug scenario's correct verdict is BUG; a
// false-bug trap's correct verdict is NO-BUG — the scenario describes healthy
// behavior the baseline protocol wrongly flags.
const (
	PilotVerdictBug   = "BUG"
	PilotVerdictNoBug = "NO-BUG"
)

// Pilot eval upload filename and the verdict scorer expression. The scorer is
// resolved by the story-11 registry at upload: a pure expression over the
// three case-bound variables, no process spawn, no network, no filesystem.
const (
	PilotEvalFilename  = "stability-s0.json"
	PilotVerdictScorer = "contains(lower(output), lower(expected))"
)

// PilotTaskInstruction is the operator-authored dispatch task — the only
// instructions the harness may follow (the payload's task/data boundary).
const PilotTaskInstruction = "Train the KimmyDB Cluster Stability Protocol on the seeded S0 scenarios: keep every true-bug verdict, and stop flagging measurement artefacts and timing effects as bugs. Change verdicts only through the verdict contract — one directive per scenario key."

// PilotScenario is one seeded S0 scenario: a stable scenario key, the correct
// verdict, the seeded round tag, the scenario description (the eval case's
// input), and the one-line rationale the runbook tables carry.
type PilotScenario struct {
	Key     string // the verdict-table directive's left side
	Verdict string // PilotVerdictBug (true bug) or PilotVerdictNoBug (false-bug trap)
	Round   string // seeded round tag, e.g. "R-1042"
	Input   string // the eval case's input: "Scenario <key> (round <round>): …"
	Why     string // why the correct verdict is what it is
}

// VerdictDirective is the exact verdict directive a protocol body must carry
// for this scenario — the eval case's expected answer under the verdict
// scorer.
func (p PilotScenario) VerdictDirective() string {
	return p.Key + ": " + p.Verdict
}

// IsTrueBug reports whether the scenario describes a real stability bug.
func (p PilotScenario) IsTrueBug() bool { return p.Verdict == PilotVerdictBug }

// PilotTrainScenarios are the six seeded train scenarios: four true bugs the
// baseline protocol catches and two false-bug traps it wrongly flags (the
// contamination and probe-path families).
func PilotTrainScenarios() []PilotScenario {
	return []PilotScenario{
		{
			Key: "drop-reports-success-without-delete", Verdict: PilotVerdictBug, Round: "R-1042",
			Input: "Scenario drop-reports-success-without-delete (round R-1042): the driver reported a successful delete for key k7, but the post-call tombstone scan finds no tombstone for k7 and the replicas still serve the key. Does the protocol flag a stability bug for this round?",
			Why:   "reported success without a persisted tombstone is a durability lie — the delete was never recorded",
		},
		{
			Key: "lag-gauge-wrong-both-ways", Verdict: PilotVerdictBug, Round: "R-1043",
			Input: "Scenario lag-gauge-wrong-both-ways (round R-1043): the lag gauge over-reports lag on idle nodes and under-reports it during catch-up bursts in the same round; both directions disagree with the ledger clock. Does the protocol flag a stability bug for this round?",
			Why:   "a gauge that is wrong in both directions cannot bound catch-up — the protocol flags it",
		},
		{
			Key: "empty-set-must-block", Verdict: PilotVerdictBug, Round: "R-1044",
			Input: "Scenario empty-set-must-block (round R-1044): a client submitted an empty operation set and the cluster accepted and acknowledged it. Does the protocol flag a stability bug for this round?",
			Why:   "the protocol requires blocking empty operation sets — accepting one is a violation",
		},
		{
			Key: "tombstone-hidden-by-barrier", Verdict: PilotVerdictBug, Round: "R-1045",
			Input: "Scenario tombstone-hidden-by-barrier (round R-1045): a tombstone written at t=900ms is still invisible to readers behind a wave barrier at t=2s, and the barrier never drains for that shard. Does the protocol flag a stability bug for this round?",
			Why:   "a barrier that hides a tombstone past its drain window is a real visibility bug",
		},
		{
			Key: "contaminated-baseline", Verdict: PilotVerdictNoBug, Round: "R-1046",
			Input: "Scenario contaminated-baseline (round R-1046): the baseline sweep ran while a compaction job held the write lock, so baseline latencies are inflated and the round delta looks like a regression. Does the protocol flag a stability bug for this round?",
			Why:   "a contaminated baseline is a measurement fault, not a cluster bug — re-baseline and re-run (NO-BUG)",
		},
		{
			Key: "front-routing-artefact", Verdict: PilotVerdictNoBug, Round: "R-1047",
			Input: "Scenario front-routing-artefact (round R-1047): the first probe of each round routes through the front-end proxy and reports one extra hop; the extra hop disappears on every later probe. Does the protocol flag a stability bug for this round?",
			Why:   "a front-routing artefact of the probe path is not a cluster fault (NO-BUG)",
		},
	}
}

// PilotValScenarios are the four held-out val scenarios: two false-bug traps
// (schema-vs-barrier timing, the dimension walk) and two true bugs (the
// mixed-version divergence budget and the lease-handoff echo). Held out means
// the train split never names them, so a body can only score them by
// following the protocol, never by reciting the set. The fourth case
// (quorum-lease-echo) is an additional held-out true bug so the
// zero-loss bar is verified over two independent true-bug families.
func PilotValScenarios() []PilotScenario {
	return []PilotScenario{
		{
			Key: "schema-vs-wave-barrier-timing", Verdict: PilotVerdictNoBug, Round: "R-2042",
			Input: "Scenario schema-vs-wave-barrier-timing (round R-2042): a schema refresh lands while a wave barrier is open; readers behind the barrier see the old schema until the barrier drains, then the new one, and the round self-heals at drain time. Does the protocol flag a stability bug for this round?",
			Why:   "schema-vs-barrier timing is expected to resolve at drain — a trap the baseline wrongly flags (NO-BUG)",
		},
		{
			Key: "dim-4096-vector-walk", Verdict: PilotVerdictNoBug, Round: "R-2043",
			Input: "Scenario dim-4096-vector-walk (round R-2043): a checksum walk over 4096-dimension vectors reports drift on the first walk after restart; a second walk over the same shard is clean and matches the harness's float reordering. Does the protocol flag a stability bug for this round?",
			Why:   "harness float reordering explains the first-walk drift — a trap the baseline wrongly flags (NO-BUG)",
		},
		{
			Key: "mixed-version-roll-divergence", Verdict: PilotVerdictBug, Round: "R-2044",
			Input: "Scenario mixed-version-roll-divergence (round R-2044): during a mixed-version rolling restart, a v2 replica and a v3 replica disagree about the same key for longer than the protocol's divergence budget. Does the protocol flag a stability bug for this round?",
			Why:   "cross-version divergence past the divergence budget is a true bug the baseline already catches",
		},
		{
			Key: "quorum-lease-echo", Verdict: PilotVerdictBug, Round: "R-2045",
			Input: "Scenario quorum-lease-echo (round R-2045): after a lease handoff, the previous lease-holder still acknowledges writes for one heartbeat interval, interleaving with the new holder's acknowledgements on the same key. Does the protocol flag a stability bug for this round?",
			Why:   "a lease-handoff echo past the handoff window is a true bug the baseline already catches",
		},
	}
}

// pilotScenarioOrder is the canonical verdict-table order: the train
// scenarios first, then the held-out val scenarios. Every pilot body renders
// its verdict table in this order, so revisions differ only in verdicts.
func pilotScenarioOrder() []PilotScenario {
	return append(append([]PilotScenario{}, PilotTrainScenarios()...), PilotValScenarios()...)
}

// PilotScenarioForCase locates the scenario an eval case was seeded from: the
// case's input names its scenario key ("Scenario <key> (round …)"). The
// second return is false for a case outside the seeded tables.
func PilotScenarioForCase(c EvalCase) (PilotScenario, bool) {
	for _, sc := range pilotScenarioOrder() {
		if strings.Contains(c.Input, sc.Key) {
			return sc, true
		}
	}
	return PilotScenario{}, false
}

// PilotEvalCaseFor renders the scenario as an eval case under the verdict
// scorer.
func PilotEvalCaseFor(sc PilotScenario) EvalCase {
	return EvalCase{Input: sc.Input, Expected: sc.VerdictDirective(), Scorer: PilotVerdictScorer}
}

// pilotEvalEnvelope is the eval upload's JSON shape: user-supplied splits so
// the format gate (skill_eval.go) stores exactly the train/val assignment the
// pilot intends, with the verdict scorer on every case.
type pilotEvalEnvelope struct {
	Train []EvalCase `json:"train"`
	Val   []EvalCase `json:"val"`
}

// PilotEvalUploadJSON renders the eval file the pilot uploads through the
// real story-05 format gate: six train cases and four held-out val cases,
// every case scored by the verdict-correctness expression.
func PilotEvalUploadJSON() string {
	env := pilotEvalEnvelope{}
	for _, sc := range PilotTrainScenarios() {
		env.Train = append(env.Train, PilotEvalCaseFor(sc))
	}
	for _, sc := range PilotValScenarios() {
		env.Val = append(env.Val, PilotEvalCaseFor(sc))
	}
	raw, err := json.Marshal(env)
	if err != nil {
		// Unreachable: the envelope is plain strings.
		return "{}"
	}
	return string(raw)
}

// --- Protocol bodies -------------------------------------------------------
//
// The baseline and the three scripted revision stages share the opening
// (purpose + verdict contract, marker phrase included) and differ in their
// guidance section, verdict table, and closing section. The baseline is the
// S0 subject: a deliberately over-cautious early protocol whose conservatism
// rule flags everything — the false-bug pathology the pilot trains away.

const pilotBodyOpening = "# KimmyDB Cluster Stability Protocol\n" +
	"\n" +
	"## Purpose\n" +
	"Keep test rounds against a KimmyDB cluster honest: classify each round's\n" +
	"observations into real stability bugs and measurement artefacts, so\n" +
	"engineering time goes to true regressions only. Rounds are run with the\n" +
	"kimmydb-testkit harness; this protocol triages what the rounds observe.\n" +
	"\n" +
	"## Verdict contract\n" +
	"A round is answered with one directive per scenario key in the verdict\n" +
	"table: `<scenario-key>: BUG` or `<scenario-key>: NO-BUG`.\n" +
	PilotMarkerPhrase + "\n"

const pilotBaselineGuidance = `## Conservatism rule

When in doubt, flag the round: a missed stability bug costs an outage, while
a false alarm costs one wasted triage hour. Flag first, investigate after.
`

const pilotHygieneGuidance3 = `## Measurement hygiene

1. Re-baseline before you blame the cluster: a sweep that ran under a
   compaction or backup job is a contaminated baseline — the delta describes
   the measurement, not the cluster. Re-baseline and re-run first.
2. Attribute one-hop artefacts to the probe path: an extra hop that appears
   only on the first probe of a round is a front-routing artefact, not a
   fault.
3. Judge schema timing after the barrier drains: a schema refresh that
   resolves when the wave barrier drains is expected timing, not a bug.
`

const pilotHygieneGuidance5 = `## Measurement hygiene

1. Re-baseline before you blame the cluster: a sweep that ran under a
   compaction or backup job is a contaminated baseline — the delta describes
   the measurement, not the cluster. Re-baseline and re-run first.
2. Attribute one-hop artefacts to the probe path: an extra hop that appears
   only on the first probe of a round is a front-routing artefact, not a
   fault.
3. Judge schema timing after the barrier drains: a schema refresh that
   resolves when the wave barrier drains is expected timing, not a bug.
4. Re-walk before you call drift: a checksum walk that is clean on a second
   pass is harness float reordering, not index drift.
5. Budget cross-version divergence: during a mixed-version roll, replicas may
   disagree inside the divergence budget; only a disagreement that outlives
   the budget is a bug.
`

const pilotEvidenceClosing = `## Evidence checklist

A BUG verdict cites the observation that crossed a budget (a visibility
window, a lag bound, the divergence budget, or the empty-set block rule); a
NO-BUG verdict cites the hygiene rule that reclassified it. A round report
carries the verdict table and the cited budgets together.
`

// pilotVerdictMaps are the staged verdict tables. Every scenario is BUG in
// the baseline (the over-cautious pathology). The fully-trained table (stage
// 3) reclassifies the four traps to NO-BUG and keeps the six true bugs; stage
// 1 is a partial fix (one val trap remains); stage 2 is the over-trimmed
// draft that additionally loses a held-out true-bug directive.
func pilotTrainedVerdicts() map[string]string {
	return map[string]string{
		"drop-reports-success-without-delete": PilotVerdictBug,
		"lag-gauge-wrong-both-ways":           PilotVerdictBug,
		"empty-set-must-block":                PilotVerdictBug,
		"tombstone-hidden-by-barrier":         PilotVerdictBug,
		"contaminated-baseline":               PilotVerdictNoBug,
		"front-routing-artefact":              PilotVerdictNoBug,
		"schema-vs-wave-barrier-timing":       PilotVerdictNoBug,
		"dim-4096-vector-walk":                PilotVerdictNoBug,
		"mixed-version-roll-divergence":       PilotVerdictBug,
		"quorum-lease-echo":                   PilotVerdictBug,
	}
}

func pilotStageVerdicts(stage int) map[string]string {
	v := pilotTrainedVerdicts()
	switch stage {
	case 1:
		v["dim-4096-vector-walk"] = PilotVerdictBug // the partial fix: one held-out trap remains
	case 2:
		v["dim-4096-vector-walk"] = PilotVerdictBug
		delete(v, "quorum-lease-echo") // the over-trim: a held-out true-bug verdict lost
	}
	return v
}

// pilotRenderVerdicts renders the verdict table in the canonical scenario
// order, skipping scenarios the map leaves out entirely (the over-trim).
func pilotRenderVerdicts(verdicts map[string]string) string {
	var b strings.Builder
	for _, sc := range pilotScenarioOrder() {
		v := verdicts[sc.Key]
		if v != PilotVerdictBug && v != PilotVerdictNoBug {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", sc.Key, v)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// pilotProtocolBody assembles one protocol body from its shared opening, the
// stage's guidance, its verdict table, and an optional closing block.
func pilotProtocolBody(guidance string, verdicts map[string]string, closing string) string {
	parts := []string{
		strings.TrimSuffix(pilotBodyOpening, "\n"),
		strings.TrimSuffix(guidance, "\n"),
		"## Verdict table\n\n" + pilotRenderVerdicts(verdicts),
	}
	if strings.TrimSpace(closing) != "" {
		parts = append(parts, strings.TrimSuffix(closing, "\n"))
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// PilotBaselineBody is the S0 subject: the condensed, faithful excerpt of the
// stability protocol as seeded — a conservative early protocol that flags
// every scenario family as a bug (the false-bug pathology the pilot trains
// away).
func PilotBaselineBody() string {
	all := map[string]string{}
	for _, sc := range pilotScenarioOrder() {
		all[sc.Key] = PilotVerdictBug
	}
	return pilotProtocolBody(pilotBaselineGuidance, all, "")
}

// PilotCandidateStage is one scripted harness proposal: the loop iteration
// that proposes it, the staged file the shim and the proposer both read (the
// single source of truth), the wiki patterns it cites, and the human note.
type PilotCandidateStage struct {
	Iteration int
	File      string
	Title     string
	Note      string
	Patterns  []string
	Body      string
}

// PilotCandidateStages is the deterministic proposal schedule the pilot's
// scripted harness profile follows:
//
//	iteration 1 — v2 measurement-hygiene clarifier (partial fix, accepted at 0.75)
//	iteration 2 — over-trimmed v3 draft (drops a held-out true-bug verdict, rejected at 0.50)
//	iteration 3 — v3 full protocol (every verdict correct, accepted at 1.0, early stop)
//
// A rejected attempt that stays in the audit trail is part of the E5 proof,
// which is why the schedule includes iteration 2.
func PilotCandidateStages() []PilotCandidateStage {
	stage := func(iteration int, title, note string, patterns []string, guidance, closing string) PilotCandidateStage {
		return PilotCandidateStage{
			Iteration: iteration,
			File:      fmt.Sprintf(pilotStageFileTmpl, iteration),
			Title:     title,
			Note:      note,
			Patterns:  patterns,
			Body:      pilotProtocolBody(guidance, pilotStageVerdicts(iteration), closing),
		}
	}
	return []PilotCandidateStage{
		stage(1, "v2 measurement-hygiene clarifier",
			"adds the measurement-hygiene rules and reclassifies the contaminated-baseline, front-routing-artefact, and schema-vs-wave-barrier-timing traps; keeps every true-bug verdict",
			[]string{"kimmydb-measurement-hygiene", "kimmydb-front-routing", "kimmydb-barrier-drain"},
			pilotHygieneGuidance3, ""),
		stage(2, "over-trimmed v3 draft",
			"carries the full hygiene rules but loses the quorum-lease-echo verdict line in the trim — exactly the regression the R_best gate exists to refuse",
			[]string{"kimmydb-lease-handoff", "kimmydb-divergence-budget"},
			pilotHygieneGuidance5, ""),
		stage(3, "v3 full protocol",
			"the complete protocol: every seeded scenario's correct verdict, full hygiene rules, and the evidence checklist",
			[]string{"kimmydb-measurement-hygiene", "kimmydb-lease-handoff", "kimmydb-divergence-budget", "kimmydb-barrier-drain"},
			pilotHygieneGuidance5, pilotEvidenceClosing),
	}
}

// Pilot staging and handshake file names. Stage files are named so the shim
// can derive the file for its payload's iteration without a manifest.
const (
	pilotBaselineFile  = "pilot-baseline.md"
	pilotStageFileTmpl = "stage-%02d.md"
	pilotWantFileTmpl  = "propose-%02d.want"
	pilotDoneFileTmpl  = "propose-%02d.done"
	// pilotHandshakeLimit bounds the shim's wait for the harness's proposal
	// handshake. A timeout fails the step loudly (a complete/failed callback)
	// instead of hanging the loop — the proposer is in-process, so a healthy
	// handshake completes in milliseconds.
	pilotHandshakeLimit = 30 * time.Second
)

// StagePilotBodies writes the baseline and every scripted stage body into dir
// as UTF-8 files. The shim and the proposer both read these files — the
// staged copy is the single source of truth the proposal handshake trusts.
func StagePilotBodies(dir string) error {
	files := map[string]string{pilotBaselineFile: PilotBaselineBody()}
	for _, st := range PilotCandidateStages() {
		files[st.File] = st.Body
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			return fmt.Errorf("failed to stage pilot body %s: %w", name, err)
		}
	}
	return nil
}

// pilotBodyVerdictLines extracts the verdict directives a protocol body
// carries, in body order: the "- <key>: <VERDICT>" rows of the verdict
// table. The scripted shim emits these as its per-scenario verdicts, and the
// demo cross-checks them against the candidate the gate actually decided on.
func pilotBodyVerdictLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		directive := strings.TrimPrefix(line, "- ")
		key, verdict, ok := strings.Cut(directive, ": ")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		verdict = strings.TrimSpace(verdict)
		if key == "" || (verdict != PilotVerdictBug && verdict != PilotVerdictNoBug) {
			continue
		}
		out = append(out, key+": "+verdict)
	}
	return out
}

// --- The E5 success bar ----------------------------------------------------
//
// The mechanical bar the pilot must clear, computed from the stored val split
// under the eval's own scorers (the same math the gate ran — every gate
// decision is reproducible from stored data):

// PilotE5Metrics is the decomposed E5 bar: per-case verdict correctness on
// the held-out val split, classified by scenario kind, for the S0 baseline
// body and the final (trained) body.
//
// A false bug is a false-bug trap the body fails to answer NO-BUG on (under
// the verdict scorer, failing a healthy scenario IS the false-bug behavior —
// the baseline's pathology is explicit over-flagging, and staying silent does
// not clear the bar either). True detection is a true-bug scenario the body
// answers BUG on. The bar clears when the final false-bug count is at most
// half the baseline's AND every true-bug val case is still caught.
type PilotE5Metrics struct {
	ValCases          int
	TrueBugCases      int
	FalseBugTrapCases int

	BaselineValScore       float64
	BaselineFalseBugs      int
	BaselineTrueBugsCaught int

	FinalValScore       float64
	FinalFalseBugs      int
	FinalTrueBugsCaught int

	// TrueDetectionLoss counts true-bug val cases caught at baseline but
	// missed by the final body — must be zero.
	TrueDetectionLoss int

	FalseBugsHalved     bool
	TrueDetectionIntact bool
	Success             bool
}

// PilotE5MetricsFor computes the bar for one baseline/final body pair over
// val. The scorer set is compiled from val itself, exactly as the gate
// compiled it, so the numbers reproduce the gate's decisions bitwise.
func PilotE5MetricsFor(val []EvalCase, baselineBody, finalBody string) PilotE5Metrics {
	m := PilotE5Metrics{ValCases: len(val)}
	set, err := compileScorerSet(val)
	if err != nil {
		// A stored set that cannot compile cannot clear the bar; return the
		// zero metrics so Success stays false and the summary shows it.
		return m
	}
	scores := func(body string) []float64 {
		out := make([]float64, len(val))
		for i, c := range val {
			out[i] = set.forCase(c).score(c.Input, c.Expected, body)
		}
		return out
	}
	base, final := scores(baselineBody), scores(finalBody)
	m.BaselineValScore = roundScore4(pilotMean(base))
	m.FinalValScore = roundScore4(pilotMean(final))
	for i, c := range val {
		sc, ok := PilotScenarioForCase(c)
		if !ok {
			continue
		}
		if sc.IsTrueBug() {
			m.TrueBugCases++
			baseCaught, finalCaught := base[i] >= 0.5, final[i] >= 0.5
			if baseCaught {
				m.BaselineTrueBugsCaught++
			}
			if finalCaught {
				m.FinalTrueBugsCaught++
			} else if baseCaught {
				m.TrueDetectionLoss++
			}
		} else {
			m.FalseBugTrapCases++
			if base[i] < 0.5 {
				m.BaselineFalseBugs++
			}
			if final[i] < 0.5 {
				m.FinalFalseBugs++
			}
		}
	}
	m.FalseBugsHalved = 2*m.FinalFalseBugs <= m.BaselineFalseBugs
	m.TrueDetectionIntact = m.TrueDetectionLoss == 0 && m.FinalTrueBugsCaught == m.TrueBugCases
	m.Success = m.FalseBugsHalved && m.TrueDetectionIntact
	return m
}

func pilotMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	total := 0.0
	for _, x := range xs {
		total += x
	}
	return total / float64(len(xs))
}

// Report renders the E5 numbers as the summary's metric block.
func (m PilotE5Metrics) Report() string {
	return fmt.Sprintf("E5 bar: false bugs on val %d → %d (halved: %v) · true-bug val cases %d caught at baseline / %d at final (loss %d, intact: %v)",
		m.BaselineFalseBugs, m.FinalFalseBugs, m.FalseBugsHalved,
		m.BaselineTrueBugsCaught, m.FinalTrueBugsCaught, m.TrueDetectionLoss, m.TrueDetectionIntact)
}

// --- The run summary -------------------------------------------------------

// PilotSummary is the finished pilot run's demonstration summary: where the
// loop ended, what the eval recorded, the E5 bar, the report, and the audit
// shape. Storage.PilotSummaryFor derives it from the stored records only —
// it is the mechanical step-6 verifier the runbook points at, and the block
// TestStabilityPilot prints.
type PilotSummary struct {
	SkillSlug   string
	JobID       string
	Profile     string
	LoopOutcome string

	Iterations       int
	AcceptedIters    int
	RejectedIters    int
	AuditAccepts     int
	AuditRejects     int
	RejectsPreserved bool

	EvalHash      string
	ScorerVersion string
	TrainCount    int
	ValCount      int
	BaselineS0    float64
	FinalValScore float64

	ReportSlug  string
	ReportCount int

	MarkerPhrase   string
	MarkerSurvived bool

	E5      PilotE5Metrics
	Success bool
}

// String renders the summary as the printed pilot report block.
func (p PilotSummary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "skill: %s · job: %s · profile: %s\n", p.SkillSlug, p.JobID, p.Profile)
	fmt.Fprintf(&b, "loop outcome: %s · iterations: %d (%d accepted, %d rejected) · gate decisions: %d accepted / %d rejected (rejects preserved: %v)\n",
		p.LoopOutcome, p.Iterations, p.AcceptedIters, p.RejectedIters, p.AuditAccepts, p.AuditRejects, p.RejectsPreserved)
	fmt.Fprintf(&b, "eval: %d train / %d val · scorer %s · hash %.12s · S0 %.4f → final %.4f\n",
		p.TrainCount, p.ValCount, p.ScorerVersion, p.EvalHash, p.BaselineS0, p.FinalValScore)
	fmt.Fprintf(&b, "result report: %s (exactly one: %v)\n", p.ReportSlug, p.ReportCount == 1)
	fmt.Fprintf(&b, "trained marker: marker phrase survived training: %v\n", p.MarkerSurvived)
	fmt.Fprintf(&b, "%s\n", p.E5.Report())
	fmt.Fprintf(&b, "SUCCESS: %v", p.Success)
	return b.String()
}

// PilotSummaryFor derives the run summary for one finished evolution job from
// the stored records: the live skill, the stored eval set, the iteration and
// audit records, and the generated result report. BaselineBody must be the
// S0 body the run started from (the fixture's PilotBaselineBody in the
// demonstration; the live wiki's pre-run body in a real pilot).
func (s *Storage) PilotSummaryFor(job *EvolutionJob, baselineBody string) (*PilotSummary, error) {
	if job == nil {
		return nil, fmt.Errorf("pilot summary requires an evolution job")
	}
	live, err := s.GetArticle(job.SkillSlug)
	if err != nil {
		return nil, fmt.Errorf("pilot summary: %w", err)
	}
	_, val, meta, err := s.ReadEvolutionEvalSet(job.ID)
	if err != nil {
		return nil, fmt.Errorf("pilot summary: %w", err)
	}
	recs, err := s.ListIterationRecords(job.ID)
	if err != nil {
		return nil, fmt.Errorf("pilot summary: %w", err)
	}
	auditAll, err := s.ListSkillAuditRecords()
	if err != nil {
		return nil, fmt.Errorf("pilot summary: %w", err)
	}
	audit := make([]SkillAuditRecord, 0, len(auditAll))
	for _, rec := range auditAll {
		if rec.SkillSlug == job.SkillSlug {
			audit = append(audit, rec)
		}
	}

	accepted, rejected := 0, 0
	for _, rec := range recs {
		switch rec.Outcome {
		case IterationOutcomeAccepted:
			accepted++
		case IterationOutcomeRejected:
			rejected++
		}
	}
	auditAccepts, auditRejects := 0, 0
	for _, rec := range audit {
		if !rec.Timestamp.Before(job.CreatedAt) {
			switch rec.Outcome {
			case SkillAuditAccepted:
				auditAccepts++
			case SkillAuditRejected:
				auditRejects++
			}
		}
	}

	reportSlug := job.ResultReportSlug
	reportCount := 0
	metas, err := s.ListArticles()
	if err != nil {
		return nil, fmt.Errorf("pilot summary: %w", err)
	}
	for i := range metas {
		m := &metas[i]
		if m.Type != ContentTypeWiki || !hasTag(m.Tags, SkillResultReportTag) {
			continue
		}
		art, gerr := s.GetArticle(m.Slug)
		if gerr != nil || !strings.Contains(art.Content, job.ID) {
			continue
		}
		reportCount++
		if reportSlug == "" {
			reportSlug = m.Slug
		}
	}

	e5 := PilotE5MetricsFor(val, baselineBody, live.Content)
	sum := &PilotSummary{
		SkillSlug:        live.Slug,
		JobID:            job.ID,
		Profile:          job.Profile,
		LoopOutcome:      job.LoopOutcome,
		Iterations:       len(recs),
		AcceptedIters:    accepted,
		RejectedIters:    rejected,
		AuditAccepts:     auditAccepts,
		AuditRejects:     auditRejects,
		RejectsPreserved: rejected == auditRejects,
		EvalHash:         meta.EvalHash,
		ScorerVersion:    meta.ScorerVersion,
		TrainCount:       meta.TrainCount,
		ValCount:         meta.ValCount,
		BaselineS0:       meta.BaselineS0,
		FinalValScore:    e5.FinalValScore,
		ReportSlug:       reportSlug,
		ReportCount:      reportCount,
		MarkerPhrase:     PilotMarkerPhrase,
		MarkerSurvived:   strings.Contains(live.Content, PilotMarkerPhrase),
		E5:               e5,
	}
	sum.Success = e5.Success &&
		reportCount == 1 &&
		sum.RejectsPreserved &&
		sum.MarkerSurvived
	return sum, nil
}
