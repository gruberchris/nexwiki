package server

import (
	"fmt"
	"strings"
)

// This file holds story 12 (Phase E/E5): the Stability S0 pilot runbook —
// the operator-facing procedure to run the REAL pilot against the live wiki
// later. The canonical text lives here so tests can hold it against the
// actual code paths (every tool name must exist in the MCP registry, every
// env name must be a real constant, every endpoint must be a registered
// route); docs/aiagent_skills.md mirrors the same procedure for humans, and
// TestPilotRunbookAccuracy keeps the two copies in sync (step headings,
// scenario tables, tool names).
//
// The runbook describes operator and harness steps only; it introduces no
// new tool, endpoint, or env var. Every reference below is a story 01-11
// surface.

// pilotRunbookSteps are the ordered step headings both the Go runbook and
// the docs section carry verbatim — the sync anchor the accuracy test walks.
var pilotRunbookSteps = []string{
	"### Step 1 — Ensure the skill exists (operator, over MCP)",
	"### Step 2 — Create the evolution job (injection mode)",
	"### Step 3 — Upload the eval sets via MCP (the real scenarios)",
	"### Step 4 — Dispatch with a real OpenCode/Claude profile (harness side)",
	"### Step 5 — Monitor the iterations",
	"### Step 6 — Verify the success bar (mechanical)",
	"### Step 7 — Human promote, review, and rollback",
}

// PilotRunbook returns the canonical Stability S0 pilot runbook text.
func PilotRunbook() string {
	return strings.Join([]string{
		"# KimmyDB Stability S0 Pilot Runbook (story 12, Phase E/E5)",
		"",
		pilotRunbookIntro(),
		pilotRunbookSuccessBar(),
		pilotRunbookStep1(),
		pilotRunbookStep2(),
		pilotRunbookStep3(),
		pilotRunbookStep4(),
		pilotRunbookStep5(),
		pilotRunbookStep6(),
		pilotRunbookStep7(),
		pilotRunbookNotes(),
	}, "\n")
}

func pilotRunbookIntro() string {
	return strings.Join([]string{
		"## What the pilot is",
		"",
		"The pilot trains the `KimmyDB Cluster Stability Protocol` skill on seeded scenarios",
		"and checks the E5 success bar mechanically: training must halve the protocol's",
		"false-bug rate on held-out val cases while losing zero true detections. A live",
		"KimmyDB cluster is NOT required — the scenarios are seeded text, the scorers are",
		"sandboxed deterministic expressions (story 11), and every number is computed from",
		"stored records. The in-repo reference implementation runs the whole loop in-process",
		"(real eval gate, real jobs, real loop, real report, scripted deterministic shim):",
		"",
		"    go test ./server/ -run TestStabilityPilot -count=1",
		"",
		"It prints the pilot summary and exits green only when the bar clears. Run it first:",
		"it is the proof the machinery works before the real run touches the live wiki.",
		"",
	}, "\n")
}

func pilotRunbookSuccessBar() string {
	return strings.Join([]string{
		"## Success bar (E5)",
		"",
		"1. False bugs on val at least halved versus the S0 baseline (2 × final ≤ baseline).",
		"2. Zero true-detection loss: every true-bug val case is still caught.",
		"3. Exactly ONE Trained Skill Result report for the run (generated once, at the end).",
		"4. Every rejected attempt preserved: a rejected iteration has its candidate record",
		"   (status rejected), its iteration record, and its gate-rejected audit record.",
		"",
		"The same four checks are asserted by `TestStabilityPilot` over the stored records,",
		"and the run summary is re-derived at any time with",
		"`Storage.PilotSummaryFor(job, baselineBody)` (server/skill_pilot.go) — the math is",
		"`PilotE5MetricsFor(val, baselineBody, finalBody)` over the stored val split under the",
		"eval's own scorers, so it reproduces the gate's decisions exactly.",
		"",
	}, "\n")
}

func pilotRunbookStep1() string {
	return strings.Join([]string{
		pilotRunbookSteps[0],
		"",
		"Ensure the skill exists; never hand-edit a trained skill — the evolution flow owns",
		"the body from here on.",
		"",
		"1. Check: `list_agent_skills`, then `get_skill_trained_state` for",
		"   `kimmydb-cluster-stability-protocol` (expect state `untrained` for a fresh skill).",
		"2. If absent, create it with `create_agent_skill`, title",
		"   \"KimmyDB Cluster Stability Protocol\", and the baseline body — the condensed",
		"   excerpt from `PilotBaselineBody()` (server/skill_pilot.go). It must carry the",
		"   verdict contract with the stable marker phrase",
		"   \"A verdict without its scenario key is not a verdict, and a key without a verdict",
		"   is not an answer.\" and this verdict table (the S0 baseline over-flags every",
		"   family — that is the pathology the pilot trains away):",
		"",
		"    - drop-reports-success-without-delete: BUG",
		"    - lag-gauge-wrong-both-ways: BUG",
		"    - empty-set-must-block: BUG",
		"    - tombstone-hidden-by-barrier: BUG",
		"    - contaminated-baseline: BUG",
		"    - front-routing-artefact: BUG",
		"    - schema-vs-wave-barrier-timing: BUG",
		"    - dim-4096-vector-walk: BUG",
		"    - mixed-version-roll-divergence: BUG",
		"    - quorum-lease-echo: BUG",
		"",
	}, "\n")
}

func pilotRunbookStep2() string {
	return strings.Join([]string{
		pilotRunbookSteps[1],
		"",
		"Create the job over MCP with `create_evolution_job`; the response mints the",
		"per-harness job token once — it is never retrievable again, so capture it where",
		"the harness keeps credentials.",
		"",
		"    create_evolution_job",
		"      \"skill_slug\": \"kimmydb-cluster-stability-protocol\"",
		"      \"profile\": \"opencode\"   (or \"claude-code\")",
		"",
		"Injection mode defaults to `all` — full injection of the supplied context, the",
		"pilot's default. Pass `\"injection_mode\": \"retrieve\"` only to exercise the",
		"story-11 BM25 pre-filter (top-K via NEXWIKI_JOB_RETRIEVAL_TOPK, default 5); the",
		"paper default stays `all`. One run per skill: a second active job is refused.",
		"",
	}, "\n")
}

func pilotRunbookStep3() string {
	return strings.Join([]string{
		pilotRunbookSteps[2],
		"",
		"Upload the seeded eval set over MCP with `upload_evolution_eval_set`, using the",
		"job's id and job_token: filename `stability-s0.json`, content from",
		"`PilotEvalUploadJSON()` (server/skill_pilot.go) — user-supplied splits with the",
		"verdict scorer on every case:",
		"",
		"    upload_evolution_eval_set",
		"      \"id\": \"<job id>\"",
		"      \"job_token\": \"<minted at creation>\"",
		"      \"filename\": \"stability-s0.json\"",
		"      \"content\": {\"train\": [...6 cases...], \"val\": [...4 cases...]}",
		"",
		"Every case is {\"input\": <scenario description>, \"expected\": \"<key>: <verdict>\",",
		"\"scorer\": \"contains(lower(output), lower(expected))\"}. The scorer is the",
		"verdict-correctness measure: a body passes a case exactly when it carries that",
		"scenario's correct directive. The format gate refuses duplicates, train/val input",
		"overlap, and secret/PII-bearing cases before anything is stored.",
		"",
		"Split floors: the gate defaults to 30 train / 10 val (NEXWIKI_EVAL_MIN_TRAIN /",
		"NEXWIKI_EVAL_MIN_VAL). The seeded S0 set is 6 train / 4 val, so either lower the",
		"floors for this pilot (6 and 4) or grow the set — a set below the floors is",
		"refused, never truncated.",
		"",
		"#### Train scenarios (6)",
		"",
		pilotScenarioTable(PilotTrainScenarios()),
		"#### Val scenarios (4) — held out",
		"",
		pilotScenarioTable(PilotValScenarios()),
		"The baseline scores S0 = 0.5000 on val (both traps wrong, both true bugs caught)",
		"and a 0.6667 train fit (both train traps wrong) — the upload response reports both.",
		"",
	}, "\n")
}

func pilotRunbookStep4() string {
	return strings.Join([]string{
		pilotRunbookSteps[3],
		"",
		"The harness holds the job token and drives the loop with the runner's RunLoop",
		"(server/skill_loop.go) under a CLI profile from NEXWIKI_JOB_PROFILES_FILE — the",
		"allowlisted binaries are `opencode` and `claude-code`. The stepper spawns one CLI",
		"step per evolution phase (inference → maintaining → proposing), names the phase in",
		"the stdin payload, and resolves each iteration's validation gate server-side",
		"(story 03 gate over the story 11 eval math). The shim contract (server/skill_runner.go)",
		"defines the wire: task/data on stdin, progress/artifact/complete callbacks on stdout.",
		"",
		"During the proposing phase the harness proposes the candidate it drafted — over MCP",
		"with `propose_skill_candidate` (full proposed body, motivating pattern slugs), or",
		"through the same storage path the tool wraps. The gate then decides: an accept",
		"promotes the body and stamps the trained marker (story 06); a rejection leaves the",
		"live skill byte-identical and the attempt in the audit trail (story 03).",
		"",
		"For the in-repo rehearsal the profile is the scripted shim behind",
		"`TestStabilityPilot`: it follows `PilotCandidateStages()` — iteration 1 proposes the",
		"partial fix, iteration 2 the over-trimmed draft the gate must reject, iteration 3 the",
		"full protocol that reaches 1.0 and stops the loop — with no network and no model.",
		"",
	}, "\n")
}

func pilotRunbookStep5() string {
	return strings.Join([]string{
		pilotRunbookSteps[4],
		"",
		"Watch the loop while the run is in flight:",
		"",
		"- MCP: `get_evolution_iterations` with the job's id and job_token — current iteration,",
		"  phase, plateau count, and every recorded iteration with its score vs R_best.",
		"- Browser: the wizard's live view, GET /api/evolution/jobs/{id}/loop (same state,",
		"  rendered). The wizard never holds the harness token.",
		"",
		"A healthy S0 run reads: an early iteration accepted on a partial fix, a rejected",
		"iteration (preserved — do not panic at it, it is the gate working), then an accept",
		"at 1.0 that stops the loop `completed` via the perfect-score early stop. Designed",
		"stops: completed, exhausted (iteration cap), plateaued, cancelled.",
		"",
	}, "\n")
}

func pilotRunbookStep6() string {
	return strings.Join([]string{
		pilotRunbookSteps[5],
		"",
		"Verify the bar from records, not from faith:",
		"",
		"1. The run's Trained Skill Result report (GET /api/evolution/jobs/{id}/report) — the",
		"   \"Evaluation set\" table carries the eval hash and scorer version, the \"Scores:",
		"   baseline vs final\" table carries S0 and R_best, and the \"Iteration history\" table",
		"   carries every accept and reject. One report per run, generated once at the end.",
		"2. Re-derive the E5 bar mechanically: `PilotSummaryFor(job, baselineBody)` over the",
		"   stored val split, the pre-run baseline body, and the final live body",
		"   (`PilotE5MetricsFor` is the math). The re-derived baseline val score must equal",
		"   the recorded S0 — the same scorers, the same rounding, the same gate math.",
		"3. The audit trail (data/skill_audit.jsonl, or GET /api/skills/{slug}/audit) must",
		"   carry every rejected attempt alongside the accepts.",
		"",
		"`TestStabilityPilot` performs exactly these three steps in-process; treat its run",
		"as the reference output for the real one.",
		"",
	}, "\n")
}

func pilotRunbookStep7() string {
	return strings.Join([]string{
		pilotRunbookSteps[6],
		"",
		"The gate promotes on accept automatically — the trained marker stamps with the",
		"run's val score and eval hash (story 06), and the wiki generates the run's result",
		"report once the run ends (story 09). The human controls are operator-only and",
		"audited:",
		"",
		"- POST /api/evolution/jobs/{id}/pause — cooperative park at the next step boundary.",
		"- POST /api/evolution/jobs/{id}/approve — approve-early: the newest pending candidate",
		"  goes through the normal gate and the run finishes completed either way.",
		"- POST /api/evolution/jobs/{id}/abort — cancel the run; the skill stays at its last",
		"  accepted version.",
		"- GET /api/skills/{slug}/audit — the Review step's source of record.",
		"- POST /api/skills/{slug}/rollback (body {\"reason\": \"…\"}) — withdraw the",
		"  trained marker and restore the pre-training body; the audit trail and the report",
		"  survive the rollback.",
		"",
		"The report article is immutable to agents (the `wikiskill-report` marker) — humans",
		"amend it through the editor by appending a dated note.",
		"",
	}, "\n")
}

func pilotRunbookNotes() string {
	return strings.Join([]string{
		"## Notes and boundaries",
		"",
		"- No live KimmyDB cluster, no network, and no model calls are needed for S0: the",
		"  scenarios are seeded text and the scorers are sandboxed expressions. The real",
		"  pilot's per-scenario behavior lives in the protocol body, not in a cluster.",
		"- The payload boundary (server/skill_runner.go) holds: `task` is the operator's",
		"  instruction (PilotTaskInstruction is the pilot's), `data` is quoted untrusted",
		"  material; wiki content never enters argv or env.",
		"- Run cap: a job is bounded by the 90-minute run cap and the iteration cap",
		"  (12 by default, `max_iterations` on the job record). The scripted in-repo run",
		"  finishes in under a minute.",
		"- Every record the run writes is RAW-layer evidence (story 10): never rewritten,",
		"  never reverted, never deleted.",
		"",
	}, "\n")
}

// pilotScenarioTable renders one scenario table for the runbook and the docs
// section: key, round, correct verdict, and the why.
func pilotScenarioTable(scenarios []PilotScenario) string {
	var b strings.Builder
	b.WriteString("| Scenario key | Round | Correct verdict | Why |\n|---|---|---|---|\n")
	for _, sc := range scenarios {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", sc.Key, sc.Round, sc.Verdict, sc.Why)
	}
	return b.String()
}
