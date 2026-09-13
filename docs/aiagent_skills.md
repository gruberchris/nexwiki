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
