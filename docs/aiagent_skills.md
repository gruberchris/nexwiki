# 🛠️ AI Agent Skills & Custom Registry User Guide

NexWiki serves as a **dynamic AI skills registry** that allows you to author procedural instructions, templates, and rules (commonly referred to as `SKILL.md` documents) and expose them directly to AI agents.

This guide details what AI skills are, how to manage them inside NexWiki, how they interact with search, and how to connect your AI agents (such as JetBrains AI Assistant, custom agents, or Claude Code) to NexWiki's custom Skills Registry.

---

## ⚠️ Before You Start: Create the Core Guidelines Skill

If you are connecting AI agents (Claude Desktop, Cursor, etc.) to NexWiki, the **most important skill to create first** is the `nexwiki-agent-guidelines` skill. The MCP tool descriptions for `create_wiki_article`, `create_agent_plan`, and `create_agent_memory` all instruct agents to load this skill before acting. If it does not exist, agents will error and proceed without any governance rules.

See **[AI Agent Integration Guide](agent_integration_guide.md)** → *Crafting Your `nexwiki-agent-guidelines` Skill* for exactly what to write and what to leave out.

---

## 💡 What is an AI Agent Skill?

In the AI ecosystem, **Skills** are reusable packages of specialized logic, conventions, and instructions that tell an AI agent how to perform a task. They represent your **procedural knowledge**, whereas articles represent **declarative knowledge** and AI Memories represent the **AI's state/logs**.

A typical skill folder in standard registries (like the `JetBrains/skills` repository) contains:
1. **`SKILL.md`**: Standardized Markdown file featuring YAML frontmatter metadata (`name`, `description`, `tags`) and a Markdown body detailing the agent's procedural directives.
2. **References & Scripts (Optional)**: Accompanying scripts or reference files.

NexWiki replicates this structure seamlessly. Any document carrying the reserved OKF `type: AI-Agent-Skill` is instantly compiled into a registry-ready skill that AI agents can consume via REST APIs.

---

## 🎨 Managing AI Skills in the UI

NexWiki makes creating and managing AI Skills extremely easy:

### 1. Creating a Skill
To create a Custom AI Skill inside NexWiki:
- Click the **AI Skill** button in the sidebar (under the Create New Page buttons), or click the **Create Custom Skill** card on the dashboard homepage.
- This opens the editor in **Custom AI Skill Mode** (marked by a premium indigo Wrench badge). The document's OKF `type` is set to `AI-Agent-Skill` automatically — there is no tag to apply, and the type is immutable on subsequent edits. AI agents create skills the same way via the `create_agent_skill` MCP tool.
- Write your skill instructions (in standard `SKILL.md` format) and click **Save Page**.

### 2. Collapsible Sidebar Folder
Once saved, skill pages are instantly moved out of your main **Articles** section and grouped under the dedicated **🛠️ AI skills** collapsible sidebar folder. This keeps your standard personal notes and wiki index clean.

### 3. Glassmorphic Registry Dashboard
When viewing any page registered as an AI skill, NexWiki renders a beautiful, glassmorphic **AI Agent Skill Active** banner right above your Markdown content.
* This banner confirms that the page is currently being served on your local custom Skills Registry.
* It provides direct click-to-open links to inspect the **JSON Schema** metadata or view the **Raw SKILL.md** representation served to agents.

---

## 🔍 How AI Skills Interact with Search

To keep your standard wiki searches focused, **AI skills do not appear in standard wiki search results by default.** Search returns only documents of type `Wiki`, so skills are filtered out alongside AI Memories and Plans.

However, NexWiki includes a smart **Explicit Search Bypass** rule:
If you are explicitly looking for a skill, it will appear in your search results *only* if:
1. Your search query contains `aiagent` or `ai-agent` (e.g. `ai-agent docker`). This opts **every** agent-typed document — skills, plans, and memories — back into the results.
2. Your search query matches the skill's **title** or **slug** (e.g. `docker-clean`).

You can also enumerate skills directly, bypassing search entirely, with the `list_agent_skills` MCP tool or `GET /api/skills`.

---

## 🔌 Using NexWiki as an AI Skills Registry

NexWiki registers three lightweight REST API endpoints, allowing any AI agent, CLI tool, or custom LLM client to pull and consume your skills.

### 1. Registry Index
* **Endpoint**: `GET /api/skills`
* **Response**: Returns a JSON array of all registered skills, parsed descriptions (extracted from the first paragraph of your page), and dynamic raw URLs.
```json
[
  {
    "name": "docker-cleanup",
    "title": "Docker Container Cleanup Guide",
    "description": "This skill instructs the agent on how to safely prune unused Docker containers, images, and volumes while safeguarding running environments.",
    "tags": ["docker", "devops"],
    "version": 2,
    "raw_url": "http://localhost:5808/api/skills/docker-cleanup/raw",
    "updated_at": "2026-06-01T00:36:25-04:00"
  }
]
```

### 2. Individual Skill Details
* **Endpoint**: `GET /api/skills/{slug}`
* **Response**: Returns the JSON metadata representation of a single skill.

### 3. Raw SKILL.md Content
* **Endpoint**: `GET /api/skills/{slug}/raw`
* **Response**: Serves the raw physical Markdown file (YAML frontmatter + Markdown body) directly as plain text (`text/plain`). This corresponds exactly to the `SKILL.md` file format that AI agents require.

### 4. Trained State
* **Endpoint**: `GET /api/skills/{slug}/trained-state`
* **Response**: The derived trained state of one skill — `untrained`, `trained`, or `stale` with the reason the marker stopped matching (skill edited or eval set re-uploaded after training), plus the marker metadata (`trained_at`, `trained_version`, `trained_val_score`, …). The registry rows from `GET /api/skills` already carry the same `trained_state` per skill; this endpoint refreshes a single one.

---

## 🔁 Evolution Wizard REST API (stories 08–09)

The WikiSkill evolution wizard (the browser UI at `/skills/<slug>/train`) reads and drives runs
through the endpoints below. Everything rides under `/api`, so it is gated by the same
origin-allow-list policy as the rest of the REST API. The browser is the operator: these
endpoints never accept (or expose) a per-harness job token — `token_hash` is withheld from every
job response, and job creation, dispatch, resume-after-pause, and eval upload stay harness-side
over MCP, where the token was minted.

### Run reads
* **Endpoint**: `GET /api/evolution/jobs?skill=<slug>`
* **Response**: JSON array of evolution jobs (oldest first), optionally narrowed to one skill. The wizard follows the newest active job for a skill, or the newest record when nothing is active.
* **Endpoint**: `GET /api/evolution/jobs/{id}`
* **Response**: One job record — status, iteration, `eval_hash`, `eval_train_count`/`eval_val_count`, `baseline_s0`, `baseline_scorer`, `loop_outcome`, stop-rule fields (`max_iterations`, `plateau_limit`, `run_deadline_at`), and timestamps.
* **Endpoint**: `GET /api/evolution/jobs/{id}/loop`
* **Response**: One-poll body for the live view: `loop` (the stepper's current iteration/phase/status — `null` until first driven), `iterations` (every per-iteration record with its phase trail, candidate, `val_score` vs `r_best`, and outcome), `plateau_count`, `max_iterations`, and `plateau_limit`. The browser twin of the `get_evolution_iterations` MCP tool.
* **Endpoint**: `GET /api/evolution/jobs/{id}/eval`
* **Response**: The stored eval metadata — split summary, S0 baseline, the 5-sample dry run, and the cost-estimate stub. `404` while the job has no accepted upload yet (the wizard's "waiting for training data" state, not a fault).
* **Endpoint**: `GET /api/evolution/jobs/{id}/eval`
* **Response**: The stored eval metadata — split summary, S0 baseline under the eval's own scorers, the train-fit indicator, the 5-sample dry run with per-sample scores, and the cost-estimate stub. `404` while the job has no accepted upload yet (the wizard's "waiting for training data" state, not a fault). Eval cases carry an optional per-case `scorer`: the built-in `exact` match by default, or a sandboxed deterministic expression (no process spawn, no network, no filesystem); unknown or unsafe names are refused at upload. The derived scorer version is part of the eval hash and stamps every audit record and report, bumping itself when the scorer set changes.
* **Endpoint**: `GET /api/evolution/candidates/{id}`
* **Response**: One skill candidate record — the diff, proposed body, and pattern slugs the wizard's iteration cards render read-only.

### Human loop controls (audited)
Each is a `POST` with an optional JSON body (`{"checkpoint": "…"}` for pause, `{"reason": "…"}` for abort) and returns the updated job record. Each lands in the activity log (`source: "api"`, actions `pause` / `abort` / `approve`) via the story-07 audited wrappers, and each responds in well under 2 s.

* **Endpoint**: `POST /api/evolution/jobs/{id}/pause` — requests a cooperative park at the next step boundary (immediately when nothing is driving). `409` on a terminal job.
* **Endpoint**: `POST /api/evolution/jobs/{id}/abort` — cancels the run (the story-04 SIGTERM → 10 s grace → SIGKILL path), reconciling the loop records. The skill stays at its last accepted version.
* **Endpoint**: `POST /api/evolution/jobs/{id}/approve` — approve-early: the newest pending candidate goes through the normal validation gate and the run finishes `completed` either way. An accept promotes (stamping the trained marker); a gate rejection leaves the skill byte-identical.

Note on resuming: a paused run resumes by re-dispatching from the harness side (the process holding the job's token), not from the browser. The wizard's paused banner says so rather than offering a control that cannot work.

### Review + result report (story 09)
The Review and Report steps read from the same seam: the skill's audited gate trail, the human rollback control, and the run's auto-generated result report.

* **Endpoint**: `GET /api/skills/{slug}/audit`
* **Response**: Every gate decision recorded for one skill (story 03 records, oldest first) — accept/reject entries with the candidate ref, parent version, content hash, validation score vs `r_best` before/after, decider, scorer version, and reason — plus the derived running best (`r_best`). This is the source of record for the Review step's audit table and the final R_best cell. `404` for a missing or non-skill slug.

* **Endpoint**: `POST /api/skills/{slug}/rollback`
* **Body**: `{"reason": "…"}` (required — the revocation is recorded with it).
* **Response**: The refreshed derived trained state. The skill body is restored to its pre-training version (the trained candidate's parent) and the marker is recorded as revoked with the caller's reason — never deleted, so the training history stays readable. Audited in the activity log like every other operator control (`source: "api"`, action `rollback`). Statuses: `404` unknown or non-skill slug, `409` no trained marker / live evolution run, `500` otherwise. The audit trail and the run's report survive a rollback.

* **Endpoint**: `GET /api/evolution/jobs/{id}/report`
* **Response**: One run's Report-step view: `job_id`, `skill_slug`, `status`, `loop_outcome`, and `terminal`, plus `report` — the Trained Skill Result article the wiki generated for this run (absent until the run ends) — and `trained_state`, the skill's live derived trained state. Staleness is derived at serve time, never stored in the report: if the eval set is re-uploaded after the run, the skill is edited after training, or the marker is revoked, `trained_state` reports `stale` with the reason.

The result report itself is written ONCE by the wiki (Go, from stored records — never a model call) when a run ends, whatever the ending: accepted, exhausted, plateaued, cancelled, or failed. It carries the `wikiskill-report` marker tag plus the `wikiskill-wiki` wiki-scope marker, and agents cannot edit, strip, or revert it (agent write tools refuse; deletion is refused twice over — it is both a report and wiki-scope content, which nothing ever auto-deletes), while a human may amend it through the editor by appending a dated note (the report's Amendments footer explains the convention). The report always reflects the run END — its outcome is the real terminal outcome with the end reason — and a no-promotion run touches the skill not at all: the report links the skill; the skill gains its backlink section only when the run promoted a candidate.

### Pre-live simulation (story 11)
Before an operator accepts a candidate, the wiki can validate it offline: the candidate's body is scored against the job's stored historical val cases with the gate's own sandboxed scorers — WITHOUT gating. No threshold compare, no audit record, no promotion.

* **Endpoint**: `POST /api/evolution/jobs/{id}/simulate`
* **Body**: optional `{"candidate_id": "…"}` — omit it to target the newest pending candidate for the job's skill.
* **Response**: The simulation record: `job_id`, `skill_slug`, `simulation` (1-based sequence), `candidate_id`, `candidate_parent_version`, `candidate_content_hash`, `eval_hash`, `scorer_version`, `val_cases` (+ `val_cases_capped` at the 200-record cap), `resolution_rate` (mean per-case score, 4 decimals — the same number the gate math produces), `pass_count`, and one `cases` row per simulated case (`index`, quoted truncated `input_preview`/`expected_preview`, `scorer` when custom, `score`, and the `pass` flag at the 0.5 mark). Results append as new immutable records — re-running never rewrites, and every record carries a hash chain (`prev_hash` → the previous record's `result_hash`) mirroring the eval set's tamper evidence.
* Statuses: `404` unknown job or candidate, `409` on an in-flight stepper (`claimed`/`running` — pause or finish it first), on a job without a stored eval set, or on a decided candidate; `500` otherwise.

### Retrieval vs injection (story 11)
A job's `injection_mode` config chooses how the stdin payload is filled: `all` (the default) keeps the story as-is — the loop sends exactly what the creating harness supplied — while `retrieve` asks the runner to rank the skill registry's name/description against the task's keywords with a small BM25 score and to quote only the top-K best matches into the payload, always plus the skill under evolution itself. This is a coarse pre-filter for the inference context, not a full retrieval system: the ranking sees names and descriptions only, and when nothing matches the subset degrades to the skill under evolution alone (the ranking trail in the iteration record says so). The default remains the default: `all`. Top-K is `NEXWIKI_JOB_RETRIEVAL_TOPK` (default 5).

---

## 🧪 KimmyDB Stability S0 Pilot Runbook (story 12)

The stability pilot trains the **`KimmyDB Cluster Stability Protocol`** skill (`kimmydb-cluster-stability-protocol`) on seeded scenarios and checks the story 12 (E5) success bar mechanically: training must halve the protocol's false-bug rate on held-out val cases while losing zero true detections. **A live KimmyDB cluster is not required** — scenarios are seeded text, the scorers are sandboxed deterministic expressions, and every number is computed from stored records. The canonical copy of this runbook lives in `server/skill_pilot_runbook.go` (`PilotRunbook()`), held against the real tool names, env vars, and endpoints by `TestPilotRunbookAccuracy`. Run the in-repo reference implementation first — the whole loop in-process, no network, no model:

```bash
go test ./server/ -run TestStabilityPilot -count=1
```

### Success bar (E5)

1. False bugs on val at least halved versus the S0 baseline (2 × final ≤ baseline).
2. Zero true-detection loss: every true-bug val case is still caught.
3. Exactly ONE Trained Skill Result report for the run (generated once, at the end).
4. Every rejected attempt preserved: candidate record (status `rejected`), iteration record, and gate-rejected audit record.

All four are asserted by `TestStabilityPilot` over the stored records and re-derived at any time with `PilotSummaryFor(job, baselineBody)` (`PilotE5MetricsFor` is the math) over the stored val split under the eval's own scorers — the same numbers the validation gate decided on.

### Step 1 — Ensure the skill exists (operator, over MCP)

Check with `list_agent_skills` and `get_skill_trained_state` (expect state `untrained` for a fresh skill); if absent, create it with `create_agent_skill`, title `KimmyDB Cluster Stability Protocol`, and the baseline body from `PilotBaselineBody()` in `server/skill_pilot.go`. It must carry the verdict contract with the stable marker phrase *"A verdict without its scenario key is not a verdict, and a key without a verdict is not an answer."* and this verdict table — the S0 baseline over-flags every family, which is the pathology the pilot trains away:

```
- drop-reports-success-without-delete: BUG
- lag-gauge-wrong-both-ways: BUG
- empty-set-must-block: BUG
- tombstone-hidden-by-barrier: BUG
- contaminated-baseline: BUG
- front-routing-artefact: BUG
- schema-vs-wave-barrier-timing: BUG
- dim-4096-vector-walk: BUG
- mixed-version-roll-divergence: BUG
- quorum-lease-echo: BUG
```

Never hand-edit a trained skill — the evolution flow owns the body from here on.

### Step 2 — Create the evolution job (injection mode)

Create the job over MCP (`create_evolution_job`) with `"skill_slug": "kimmydb-cluster-stability-protocol"` and `"profile": "opencode"` (or `claude-code`). The response mints the per-harness `job_token` once — never retrievable again, so capture it where the harness keeps credentials. Injection mode defaults to `all` (full injection, the pilot's default); pass `"injection_mode": "retrieve"` only to exercise the story-11 BM25 pre-filter. One run per skill: a second active job is refused.

### Step 3 — Upload the eval sets via MCP (the real scenarios)

Upload with `upload_evolution_eval_set`, using the job's `id` and `job_token`, filename `stability-s0.json`, and content from `PilotEvalUploadJSON()` — user-supplied splits, every case shaped `{"input": <scenario description>, "expected": "<key>: <verdict>", "scorer": "contains(lower(output), lower(expected))"}`. That scorer is the verdict-correctness measure: a body passes a case exactly when it carries that scenario's correct directive. The format gate refuses duplicates, train/val input overlap, and secret/PII-bearing cases before anything is stored. Split floors default to 30 train / 10 val (`NEXWIKI_EVAL_MIN_TRAIN` / `NEXWIKI_EVAL_MIN_VAL`); the seeded S0 set is 6 train / 4 val, so lower the floors to 6/4 for this pilot or grow the set — a set below the floors is refused, never truncated.

**Train scenarios (6):**

| Scenario key | Round | Correct verdict | Why |
|---|---|---|---|
| `drop-reports-success-without-delete` | R-1042 | BUG | reported success without a persisted tombstone is a durability lie — the delete was never recorded |
| `lag-gauge-wrong-both-ways` | R-1043 | BUG | a gauge that is wrong in both directions cannot bound catch-up — the protocol flags it |
| `empty-set-must-block` | R-1044 | BUG | the protocol requires blocking empty operation sets — accepting one is a violation |
| `tombstone-hidden-by-barrier` | R-1045 | BUG | a barrier that hides a tombstone past its drain window is a real visibility bug |
| `contaminated-baseline` | R-1046 | NO-BUG | a contaminated baseline is a measurement fault, not a cluster bug — re-baseline and re-run (NO-BUG) |
| `front-routing-artefact` | R-1047 | NO-BUG | a front-routing artefact of the probe path is not a cluster fault (NO-BUG) |

**Val scenarios (4) — held out:**

| Scenario key | Round | Correct verdict | Why |
|---|---|---|---|
| `schema-vs-wave-barrier-timing` | R-2042 | NO-BUG | schema-vs-barrier timing is expected to resolve at drain — a trap the baseline wrongly flags (NO-BUG) |
| `dim-4096-vector-walk` | R-2043 | NO-BUG | harness float reordering explains the first-walk drift — a trap the baseline wrongly flags (NO-BUG) |
| `mixed-version-roll-divergence` | R-2044 | BUG | cross-version divergence past the divergence budget is a true bug the baseline already catches |
| `quorum-lease-echo` | R-2045 | BUG | a lease-handoff echo past the handoff window is a true bug the baseline already catches |

The baseline scores S0 = 0.5000 on val (both traps wrong, both true bugs caught) and a 0.6667 train fit (both train traps wrong) — the upload response reports both.

### Step 4 — Dispatch with a real OpenCode/Claude profile (harness side)

The harness holds the job token and drives the loop with the runner's `RunLoop` (`server/skill_loop.go`) under a CLI profile from `NEXWIKI_JOB_PROFILES_FILE` (allowlisted binaries: `opencode`, `claude-code`). The stepper spawns one CLI step per evolution phase (inference → maintaining → proposing), names the phase in the stdin payload, and resolves each iteration's validation gate server-side. During the proposing phase the harness proposes the candidate it drafted over MCP (`propose_skill_candidate`, full proposed body plus motivating pattern slugs); the gate then decides — an accept promotes and stamps the trained marker, a rejection leaves the live skill byte-identical and the attempt in the audit trail. For the in-repo rehearsal the profile is the scripted shim behind `TestStabilityPilot` following `PilotCandidateStages()`: the partial fix (accepted), the over-trimmed draft the gate must reject (preserved), and the full protocol that reaches 1.0 and stops the loop.

### Step 5 — Monitor the iterations

MCP: `get_evolution_iterations` with the job's `id` and `job_token` — current iteration, phase, plateau count, and every recorded iteration with its score vs R_best. Browser: the wizard's live view at `GET /api/evolution/jobs/{id}/loop` (the wizard never holds the harness token). A healthy S0 run reads: an early iteration accepted on a partial fix, a rejected iteration (preserved — that is the gate working), then an accept at 1.0 that stops the loop `completed` via the perfect-score early stop.

### Step 6 — Verify the success bar (mechanical)

1. The run's Trained Skill Result report (`GET /api/evolution/jobs/{id}/report`): the *Evaluation set* table carries the eval hash and scorer version, *Scores: baseline vs final* carries S0 and R_best, *Iteration history* carries every accept and reject. One report per run, generated once at the end.
2. Re-derive the E5 bar mechanically: `PilotSummaryFor(job, baselineBody)` over the stored val split, the pre-run baseline body, and the final live body. The re-derived baseline val score must equal the recorded S0 — same scorers, same rounding, same gate math.
3. The audit trail (`data/skill_audit.jsonl`, or `GET /api/skills/{slug}/audit`) must carry every rejected attempt alongside the accepts.

`TestStabilityPilot` performs exactly these three steps in-process; treat its output as the reference for the real run.

### Step 7 — Human promote, review, and rollback

The gate promotes on accept automatically — the trained marker stamps with the run's val score and eval hash, and the wiki generates the run's result report once the run ends. The human controls are operator-only and audited: `POST /api/evolution/jobs/{id}/pause` (cooperative park), `POST /api/evolution/jobs/{id}/approve` (approve-early: the newest pending candidate goes through the normal gate and the run finishes `completed` either way), `POST /api/evolution/jobs/{id}/abort` (cancel; the skill stays at its last accepted version), `GET /api/skills/{slug}/audit` (the Review step's source of record), and `POST /api/skills/{slug}/rollback` (withdraw the trained marker and restore the pre-training body; the audit trail and the report survive). The report article is immutable to agents (the `wikiskill-report` marker) — humans amend it through the editor by appending a dated note.

### Notes and boundaries

- No live KimmyDB cluster, no network, and no model calls are needed for S0; the real pilot's per-scenario behavior lives in the protocol body, not in a cluster.
- The payload boundary holds: `task` is the operator's instruction, `data` is quoted untrusted material, and wiki content never enters argv or env.
- A job is bounded by the 90-minute run cap and the iteration cap (12 by default); the scripted in-repo run finishes in under a minute.
- Every record the run writes is RAW-layer evidence: never rewritten, never reverted, never deleted.

---

## 🚀 Practical Example: Creating a Git Skill

Here is a practical example of a skill that you can write inside NexWiki to guide your AI assistant on how you prefer git commits to be structured.

### 1. Frontmatter and Content
Click **AI Skill** to start a new skill page and write this content in the editor:

```markdown
# Git Commit Standardizer

## Overview
Guides the agent on how to construct clean, meaningful git commits according to the Conventional Commits specification.

## When to Use
Trigger this skill whenever the user asks you to write a commit message, commit changes, or prepare a pull request.

## Instructions
1. Format all commit messages as: `<type>(<scope>): <description>`
2. Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`.
3. Keep the subject line under 50 characters.
4. Use the imperative, present tense (e.g., "add feature" instead of "added feature").

## Example Output
```text
feat(auth): add google oauth2 login provider
```
```

### 2. Consume in JetBrains AI Assistant or Claude Code
In editors supporting custom registries, or custom Python scripts, you can direct your client to query `/api/skills` to discover available skills, and fetch their raw rules from `/api/skills/<slug>/raw` to inject into the system prompt!
