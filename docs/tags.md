# NexWiki Tags & AI Agent Memories Guide 🏷️🤖

NexWiki organizes content along two independent axes: a free-form **Tagging System** for categorizing and filtering your notes, and an OKF document **`type`** that provides a completely isolated, protected storage layer for **AI-managed documents** (memories, collaborative plans, and agent skills).

This guide teaches you how tags and document types work in NexWiki and provides useful, practical examples for both humans and AI agents.

---

## 🏷️ Standard User Tags

Standard tags are user-created keywords applied to wiki articles to make categorizing, browsing, and filtering highly efficient.

### 1. Creating and Applying Tags
When editing any wiki article inside the split-pane **Editor**:
1. Locate the **Tags** input box underneath the article title.
2. Type a tag name (e.g., `frontend`, `database`, `recipes`).
3. Press **Enter** or type a **comma (`,`)** to commit the tag.
4. Click **Save Page** to write the tags to the article's front-matter.

Tags are saved directly inside the flat-file Markdown front-matter, which is real YAML conforming to the **Open Knowledge Format (OKF v0.2)**:
```yaml
---
type: Wiki
title: Database Configuration
slug: database-configuration
description: Connection pooling and credential setup for the production database.
tags:
    - database
    - backend
    - production
timestamp: "2026-05-31T15:30:00Z"
created_at: "2026-05-31T15:00:00Z"
version: 3
edit_summary: Updated connection pool size
sources:
    - id: pg-pool
      resource: https://www.postgresql.org/docs/16/runtime-config-connection.html
      title: PostgreSQL Connection Settings
stale_after: "2027-01-01T00:00:00Z"
generated:
    by: nexwiki/mcp
    at: "2026-05-31T15:30:00Z"
verified:
    - by: human:local
      at: "2026-06-01T10:00:00Z"
---
```

> The `type` key is the document's **class discriminator** and is managed by NexWiki — see [Protected AI Documents](#-protected-ai-agent-memories-plans--skills) below. `timestamp` is the canonical modified time (OKF), synchronized with `generated.at`. In OKF v0.2, frontmatter also natively carries provenance (`sources`), trust lineage (`generated`, `verified`), freshness (`stale_after`), and lifecycle (`status`), keeping open folksonomy tags clean. See the [OKF v0.2 Guide](./okf_v02_guide.md) for full specifications.

### 2. Removing and Deleting Tags
* **Remove a tag from an article**: Click the tiny `×` on the tag badge in the Editor, then save the page.
* **Delete a tag globally**: If you want to remove a tag from all articles in the wiki, click the tag badge in the **Filter by Tag** cloud in the sidebar, or issue a `DELETE /api/tags/{tag}` request. This will completely remove the tag — every case-insensitive variant of it (`Foo`, `foo`, `FOO`) in one sweep — from the front-matter of every article containing it. The sweep works document by document, so other edits are not blocked behind it; if it is interrupted (the server stops, or one document fails to save), the response reports how many documents were `rewritten`, `skipped`, and `failed`, and issuing the same request again finishes the remainder.
* **Update tags programmatically**: Connected clients can update tags using the `PUT /api/articles/{slug}/tags` API endpoint. This performs a tag-only update without loading or rewriting the main page body, offering high speed and preventing accidental modifications to page contents. MCP agents pass `tags` to `save_article` instead (with the document's `slug`, current `title` and `content`, and `loaded_version`); passing `tags` replaces the user tags, omitting it keeps them.

*Note: NexWiki does not allow global tag renaming. To rename a tag, apply the new tag name to the desired articles and delete the old tag.*

### 3. Searching and Filtering by User Tags
* **Tag Filter Cloud**: The sidebar displays all unique user tags currently in use. Click a tag badge in the cloud to instantly filter your Articles directory to show only articles containing that tag. Click **Clear** to reset.
* **Badges**: In the article reader, clicking any tag badge under the title instantly triggers a filtered search for that tag.

---

## 📌 Lifecycle Status — a Field, Not a Tag

Lifecycle state lives in a dedicated `status` front-matter field. It is deliberately **not** a tag: a status is a single value with a state machine, while tags are an unordered folksonomy, and storing one inside the other made it possible for a document to claim two contradictory states at once.

Only two document classes have a lifecycle at all, and each has an enforced vocabulary.

### Plan Lifecycle Statuses (AI-Agent-Plan)

Every Collaborative AI Plan has **exactly one** of these eight states in its `status` field — enforced on every write path (editor, REST, and MCP).

| Status | Meaning | Auto-transitions? |
|---|---|---|
| `draft` | Being written, or written and not yet started (the default for new plans) | No |
| `implementing` | Work has begun, not finished | No |
| `blocked` | Started but stuck on an external dependency | No |
| `completed` | Implementation finished | → `archived` after `NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS` |
| `superseded` | Terminal; the work moved to another plan | → `archived`, same timer |
| `parked` | Deliberately deferred; a design worth keeping | **Never** — the exemption is its purpose |
| `evergreen` | A running backlog with no finish line | **Never** |
| `archived` | Retired, retained for reference | → **deleted** after `NEXWIKI_PLAN_DELETE_AFTER_DAYS` |

The automatic transitions are driven by the background plan lifecycle worker — see the [Plan Lifecycle Guide](./plan_lifecycle_guide.md) for the state machine, the timers, the `status_changed_at` clock, and the safety guards (dry-run mode and the backlink guard that refuses to delete a plan other documents still link to).

### Skill Statuses (AI-Agent-Skill)

A Custom AI Skill has **at most one** of these — a skill may have no status at all:

| Status | Meaning |
|---|---|
| `draft` | Being written or revised; not yet trustworthy to follow |
| `ready` | Complete and safe for an agent to load and follow |
| `archived` | Retired, kept for reference |

Skills have no timers: nothing auto-archives or auto-deletes a skill.

### Wiki Articles and Agent Memories — no status, no rules

Wiki articles and agent memories have **no lifecycle status**. The editor offers no status control for them, no tool writes one, and their tags are **never validated, reserved, or stripped** — tag them with whatever is useful to you or your agents.

The one-time migration removed the words that used to be applied as status tags under the old convention (`draft`, `wip`, `in-progress`, `active`, `todo`, `pending`, `review`, `ready`, `done`) from these documents. The tags were simply deleted; nothing replaced them. `archived` and `inbox` were deliberately kept — neither describes a document's state: `archived` is the archival *mechanism*, and `inbox` marks a raw capture still queued for compilation. Nothing stops you re-applying any of these words as ordinary tags afterwards.

### The One Rule for Plans and Skills

A plan or a skill may not use a **lifecycle word as a tag**. Tagging a plan `completed` while its field says `implementing` would be two contradictory sources of truth, so the tag is rejected with a message pointing at the field. Project-context tags, topics, and every other free tag are unaffected.

### How Status Works

A status value is a **semantic label** — it does not trigger automatic filtering, hiding, or routing — with two exceptions: `archived` (visibility and deletion, below) and the plan lifecycle statuses (validation and timers, above).

* Marking a document archived — the `status` field on a plan or skill, the `archived` **tag** on a wiki article or memory — **hides it from search results by default**: a search only returns archived documents when the query text mentions "archived" (browser) or the caller passes `include_archived` / an `archived` tag facet (MCP `search_wiki`).
* Archived documents are also **hidden from the home dashboard sections and the sidebar by default**; typing `archived` in a filter box brings them back. Direct URLs always work — hiding from discovery never means 404.
* The filter help modals (accessible via the `?` icon in the filter bar) document the syntax and these defaults.

### Auto-Deletion of Archived Articles

When an article is tagged `archived`, NexWiki records the timestamp in the article's front-matter as `archived_at`. On each server startup, NexWiki checks all archived articles and deletes any whose `archived_at` timestamp is older than the configured retention period. The sweep is guarded: a document that other documents still link to is kept (as is one a backlink scan that skipped unreadable or misplaced entries cannot show unlinked), archived plans are left to the plan lifecycle worker, and a failed delete is logged and skipped without affecting startup — the document is checked again on the next startup.

This behavior is controlled by the `NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS` environment variable:

| Value | Behavior |
|---|---|
| Unset or `0` | Auto-deletion is **disabled** (default) |
| Positive integer (e.g. `30`) | Articles archived longer than that many days are deleted on the next startup |

```bash
# Delete archived articles that have been archived for more than 30 days
export NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS=30
./nexwiki -data ./wiki-data
```

> **Note:** Deletion is permanent and not recoverable (unless you have a backup). The server logs a line to stderr for each article deleted: `Deleted archived article: <slug> (archived at: <timestamp>)`.

### Setting a Status

* **In the Editor**: use the **Status** dropdown beside the Tags row. It appears only when editing a plan or a skill — the two types that have a status. Statuses render as a colored badge on article cards and in the article header.
* **Via MCP**: pass `status` to `save_article` when creating or updating a plan or skill (omitting it on an update preserves the current status). `get_wiki_overview` returns the two vocabularies in its structured output (`plan_status_tags`, `skill_status_tags`, and their union `status_tags`). `search_wiki` takes a `status` filter, with or without a query, e.g. `search_wiki(type: "plans", status: "implementing")`.
* **Via REST API**: include `"status"` in the `POST /api/articles` or `PUT /api/articles/{slug}` body. Omitting it **preserves** the current status, so an editor that does not manage lifecycle state cannot silently reset a completed plan.

Every write path enforces the contract: a save that leaves a plan without a valid status, gives a skill an unrecognized one, or puts a lifecycle word in either one's tags is rejected with an error naming the valid vocabulary.

---

## 🤖 Protected AI Agent Memories, Plans & Skills

AI-driven documents are **not** distinguished by tags. Every NexWiki document carries an OKF **`type`** front-matter key — its class discriminator — and that is what separates regular articles from AI-managed ones.

There are five recognized document types:

| `type` | Created by | Description |
|---|---|---|
| `Wiki` | `save_article` (the default type) / the web UI | The default for all regular articles. The primary non-reserved type. |
| `AI-Agent-Memory` | `save_article(type: "AI-Agent-Memory")` | Durable agent knowledge (troubleshooting logs, decisions, conventions, rules). |
| `AI-Agent-Plan` | `save_article(type: "AI-Agent-Plan")` | Roadmaps that **either** you or the agent can create, edit, and complete. |
| `AI-Agent-Skill` | `save_article(type: "AI-Agent-Skill")` / the UI Skill button | Reusable procedural agent instructions (`SKILL.md` format). Exposed as a custom Skills Registry. |
| `Attested Computation` | Import / API | OKF v0.2 executable scripts, runtimes, typed parameters, and attestation specifications. |

> **Historical note:** earlier versions of NexWiki keyed these classes off `aiagent-*` tag prefixes. Those class tags were removed when NexWiki adopted OKF — the class now lives in `type`. You will not find `aiagent-plan` or `aiagent-memory-*` tags on current documents.

### 🧭 A memory has two axes: kind and scope

Ask two different questions about any memory, and NexWiki answers them with two different mechanisms:

| | **Kind** — *what sort of fact is this?* | **Scope** — *how far does it reach?* |
|---|---|---|
| Vocabulary | **Closed**: `project`, `reference`, `user`, `feedback` | **Open**: any project or topic name |
| Stored as | the `memory_kind` **field** | the tool-managed `memory-<scope>` **tag** |
| Set with | `memory_kind` (optional, but always set it on a new memory) | `memory_type` (optional) |
| Filter with | `search_wiki(memory_kind:)` | `search_wiki(type: "memories", tag: "memory-<scope>")` |

The split is not arbitrary — it is the same rule NexWiki learned the hard way with lifecycle status: **closed vocabularies are fields, open vocabularies are tags.** A single value with a fixed set of options stored inside an unordered folksonomy forces "exactly one" counting and a denylist for near-misses; a dedicated field makes the invalid states unrepresentable instead of merely detectable.

The two axes are **independent**, and the full cross-product is legal. A `feedback` memory may be scoped to a project or carry no scope at all.

| Kind | Holds | Example |
|---|---|---|
| `project` | Goals and constraints **not derivable** from the repo or its git history | "Deploys from an agent session are refused by the permission classifier" |
| `reference` | A pointer to an external resource — dashboard, ticket, host, URL | "The metrics dashboard lives at …" |
| `user` | Who the operator is — role, expertise, standing preferences | "Prefers the standard argued from the RFC, not from current clients" |
| `feedback` | A correction the operator gave, plus *why*, plus *how to apply it* | "Raise scope concerns in conversation, not in the PR description — because a PR describes the change" |

`user` and `feedback` are the two kinds that had nowhere to live before this axis existed, and their absence is why the second brain was split in half: every stored memory was a technical fact about a system, while everything known about *the person* lived in one client's local files, invisible to every other agent on the MCP server.

**Memories written before the kind axis existed carry none.** They stay valid, readable and editable — no write path refuses a memory without a kind — and `wiki_health` lists them under `unkinded_memories` as a burn-down worklist. Nothing backfills them automatically: deciding `project` versus `reference` for an existing memory is a judgment call per memory, not a mechanical rewrite.

### 🧷 Memory scope tags

The one system tag that remains is the **memory-scope tag**, `memory-<scope>`. It is set from the `memory_type` argument of `save_article` (on an `AI-Agent-Memory`) and narrows a memory to a project or topic:

| `memory_type` | Scope tag applied | Use for |
|---|---|---|
| `nexwiki` (any project name) | `memory-nexwiki` | Knowledge that only applies to that project |
| `docker`, `golang` (any topic) | `memory-docker` | Reusable knowledge across projects |
| *(omitted)* | *(none)* | Knowledge with no clear project or topic home |

Scope tags are **tool-managed**: preserved automatically when `save_article` or `PUT /api/articles/{slug}/tags` replaces a memory's user tags, hidden from the sidebar tag cloud, and not freely assignable by users to non-memory documents. Filter memories by scope with `search_wiki(type: "memories", tag: "memory-nexwiki")`, and by both axes at once with `search_wiki(query: "...", type: "memories", tag: "memory-nexwiki", memory_kind: "reference")`.

In the web UI, kind renders as a badge beside the status badge on cards and in the article header, is edited from a **Kind** dropdown in the editor (memories only), and is matched by the filter bar alongside titles, tags and status — so typing `feedback` finds the corrections, and `feedback || user` finds everything known about the operator.

> **Preservation applies only to `AI-Agent-Memory` documents.** That is the only class where the tag is genuinely tool-managed — `save_article` derives it from `memory_type`, and dropping it would orphan the memory from its scope. A `memory-*` tag sitting on a `Wiki`, `AI-Agent-Plan`, or `AI-Agent-Skill` document is stray data that no tool puts there, so it is **removable** by replacing that document's tags. It was not always: until this was fixed, such a tag survived every edit and `DeleteTagGlobally` refused it too, leaving it permanently stuck. Forging a new scope tag onto a non-memory document is still refused.

### 🛡️ Type rules & validation
To preserve integrity while keeping documents fully collaborative:
1. **Types are tool-assigned.** There is no user-facing type picker. The reserved `AI-Agent-*` values are set by the `type` argument of `save_article` (or the REST `type` field), and by the web UI's dedicated creation buttons such as **AI Skill**.
2. **An ordinary edit never changes the type.** An update that omits `type` keeps the current one. Relabelling is explicit: passing a different `type` changes the class, and the lifecycle fields follow — a document moved into `AI-Agent-Plan` enters at `draft` (unless its status is already a valid plan status), one moved into `Wiki` or `AI-Agent-Memory` drops its status, a plan or skill status the new type does not accept must be replaced with an explicit `status`, and a document leaving `AI-Agent-Memory` drops its `memory-<scope>` tags and `memory_kind`. An unknown type is an error, never a silent `Wiki`. The tag-only `PUT /api/articles/{slug}/tags` never touches `type` at all.
3. **Deletion is per document.** `delete_article` removes exactly one document by slug, whatever its type; `wiki_health` is the place to find memories that are candidates for retirement (near-duplicates, unsourced, unkinded).
4. **Freedom to edit & delete.** You can still fully edit, append to, and delete any AI-created document from the web UI, and add or remove its free user tags however you like.

### 🧹 Default search & sidebar isolation
AI documents are isolated by **type**, keeping your personal workspace tidy:
* **Sidebar directories** — the sidebar splits documents into four sections by `type`:
  * **📚 Articles** — documents of type `Wiki`.
  * **📋 AI plans** — collapsible folder, type `AI-Agent-Plan`.
  * **🛠️ AI skills** — collapsible folder, type `AI-Agent-Skill`.
  * **🤖 AI memories** — collapsible folder, type `AI-Agent-Memory`.
* **Default search** — a standard search returns only `Wiki` documents. Everything with a reserved type is excluded, even when it shares project tags with your regular pages.
* **Explicit search bypass** — include `aiagent` or `ai-agent` anywhere in the query to opt every agent document back into the results (e.g. `ai-agent build error`). Searching an exact slug or title also resolves the document directly.

---

## 💡 Practical Examples & Guides

### 1. Organizing Standard User Notes
Imagine you are building a full-stack web application. You can use standard tagging to categorize your documentation:
* **`frontend` / `css` / `react`**: Applied to your UI component guidelines.
* **`backend` / `database` / `security`**: Applied to API specs and database setups.
* **`reference` / `cheatsheet`**: Applied to command shortcuts or quick-lookup syntax.

To see all your frontend guides, click the `frontend` tag pill in your sidebar tag cloud.

### 2. Collaborative Plan Tracking (type `AI-Agent-Plan`)
When you launch a complex project, either you or your connected AI assistant can create an implementation roadmap. `save_article(type: "AI-Agent-Plan")` sets `type: AI-Agent-Plan` and applies a project tag from its `project_context` argument (e.g. `nexwiki`):
```markdown
# Migration to Go 1.22 Plan 🚀

— [x] Task 1: Audit code for old mux patterns
— [/] Task 2: Refactor routing to support wildcard path values
— [ ] Task 3: Run comprehensive integration test suite
```
The page slug is named directly after the feature (e.g. `migration-to-go-122`). Both you and your AI agent can collaboratively edit, check tasks, and complete this plan. The page remains safely stored under your **📋 AI plans** directory, keeping your main wiki page list clean.

When the work is finished, the agent appends closing notes with `append_article` and then sets `status: "completed"` via `save_article`. Neither call passes `type`, so the `AI-Agent-Plan` type is preserved through both.

### 3. AI-Driven Troubleshooting Log (type `AI-Agent-Memory`)
If a server build fails, the agent can document the investigation with `save_article(type: "AI-Agent-Memory")`:
* **Title**: `Go Build Error May 2026`
* **`memory_type`**: `nexwiki` → applies the tool-managed scope tag `memory-nexwiki`
* **Additional user tags**: `backend`
* **Content**: Logs the specific error message, hypotheses tested, steps taken, and the final solution (e.g., importing the missing `strings` package).
* **Benefit**: The next time a build error occurs, the agent (or you!) can run `search_wiki(type: "memories", tag: "memory-nexwiki")`, or search `ai-agent build error`, to find past resolutions instantly — avoiding repeated debugging.
