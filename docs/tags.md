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

Tags are saved directly inside the flat-file Markdown front-matter, which is real YAML conforming to the **Open Knowledge Format (OKF v0.1)**:
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
---
```

> The `type` key is the document's **class discriminator** and is managed by NexWiki — see [Protected AI Documents](#-protected-ai-agent-memories-plans--skills) below. `timestamp` is the last-modified time; there is no `updated_at` key.

### 2. Removing and Deleting Tags
* **Remove a tag from an article**: Click the tiny `×` on the tag badge in the Editor, then save the page.
* **Delete a tag globally**: If you want to remove a tag from all articles in the wiki, click the tag badge in the **Filter by Tag** cloud in the sidebar, or issue a `DELETE /api/tags/{tag}` request. This will completely remove the tag from the front-matter of every article containing it.
* **Update tags programmatically**: Connected clients and AI agents can update tags using the `PUT /api/articles/{slug}/tags` API endpoint or the `update_article_tags` MCP tool. This performs a tag-only update without loading or rewriting the main page body, offering high speed and preventing accidental modifications to page contents.

*Note: NexWiki does not allow global tag renaming. To rename a tag, apply the new tag name to the desired articles and delete the old tag.*

### 3. Searching and Filtering by User Tags
* **Tag Filter Cloud**: The sidebar displays all unique user tags currently in use. Click a tag badge in the cloud to instantly filter your Articles directory to show only articles containing that tag. Click **Clear** to reset.
* **Badges**: In the article reader, clicking any tag badge under the title instantly triggers a filtered search for that tag.

---

## 📌 Status & Lifecycle Tags

NexWiki recognizes two separate status vocabularies, split by document type. Status tags are displayed with **highest priority** on the home dashboard article cards, making it easy to see what's active, blocked, or done at a glance.

### Plan Lifecycle Statuses (AI-Agent-Plan only)

Every Collaborative AI Plan carries **exactly one** of these eight states — enforced on every write path (editor, REST, and MCP). All but `archived` are **invalid on any other document type**.

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

### General Status Tags (wiki articles, memories, skills)

| Tag | Meaning |
|---|---|
| `wip` | Actively being worked on |
| `in-progress` | Same as `wip` — task underway |
| `todo` | Queued but not yet started |
| `active` | Currently in use or being maintained |
| `review` | Ready for review by another person or agent |
| `ready` | Approved and ready to act on |
| `pending` | Awaiting an external event or decision |
| `done` | Fully implemented or resolved |
| `archived` | Retired — kept for reference, no longer active |
| `inbox` | Raw, unprocessed capture awaiting compilation into the wiki |

`archived` is the sole tag shared by both vocabularies: on a plan it is a lifecycle state reached automatically; on any other type it keeps the manual archive-then-delete behavior described below.

### How Status Tags Work

Most status tags are **purely semantic labels** — they do not trigger automatic filtering, hiding, or routing in the backend. The exceptions are `archived` (visibility and auto-deletion, below) and the plan lifecycle statuses (validation and timers, above).

* Applying `archived` to a document **hides it from search results by default** — a search only returns archived documents when the query text mentions "archived" (browser) or the caller passes `include_archived` / an `archived` tag facet (MCP `search_wiki`).
* Archived documents are also **hidden from the home dashboard sections and the sidebar by default**; typing `archived` in a filter box brings them back. Direct URLs always work — hiding from discovery never means 404.
* The filter help modals (accessible via the `?` icon in the filter bar) document the syntax and these defaults.

### Auto-Deletion of Archived Articles

When an article is tagged `archived`, NexWiki records the timestamp in the article's front-matter as `archived_at`. On each server startup, NexWiki checks all archived articles and deletes any whose `archived_at` timestamp is older than the configured retention period.

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

### Applying Status Tags

* **In the Editor**: Type a status tag (e.g., `archived`) in the Tags input field. Status tags appear with higher visual priority than regular user tags on article cards, and plan lifecycle statuses render with a per-state color palette.
* **Via MCP**: AI agents can apply status tags using `update_article_tags` or the `tags` parameter on `create_wiki_article`, `edit_wiki_article`, `create_agent_plan`, or `edit_agent_plan`. Call `get_status_tags` to retrieve both vocabularies, grouped by document type.
* **Via REST API**: Use `PUT /api/articles/{slug}/tags` with your updated tags array.

Every write path enforces the plan status contract: a save that leaves a plan with zero or several lifecycle statuses — or puts a plan-exclusive status on a non-plan document — is rejected with an error naming the valid vocabulary.

---

## 🤖 Protected AI Agent Memories, Plans & Skills

AI-driven documents are **not** distinguished by tags. Every NexWiki document carries an OKF **`type`** front-matter key — its class discriminator — and that is what separates regular articles from AI-managed ones.

There are exactly four types:

| `type` | Created by | Description |
|---|---|---|
| `Wiki` | `create_wiki_article` / the web UI | The default for all regular articles. The only non-reserved type. |
| `AI-Agent-Memory` | `create_agent_memory` | Durable agent knowledge (troubleshooting logs, decisions, conventions, rules). Protected from bulk deletion. |
| `AI-Agent-Plan` | `create_agent_plan` | Roadmaps that **either** you or the agent can create, edit, and complete. |
| `AI-Agent-Skill` | `create_agent_skill` / the UI Skill button | Reusable procedural agent instructions (`SKILL.md` format). Exposed as a custom Skills Registry. |

> **Historical note:** earlier versions of NexWiki keyed these classes off `aiagent-*` tag prefixes. Those class tags were removed when NexWiki adopted OKF — the class now lives in `type`. You will not find `aiagent-plan` or `aiagent-memory-*` tags on current documents.

### 🧷 Memory scope tags

The one system tag that remains is the **memory-scope tag**, `memory-<scope>`. It is set from the `memory_type` argument of `create_agent_memory` and narrows a memory to a project or topic:

| `memory_type` | Scope tag applied | Use for |
|---|---|---|
| `nexwiki` (any project name) | `memory-nexwiki` | Knowledge that only applies to that project |
| `docker`, `golang` (any topic) | `memory-docker` | Reusable knowledge across projects |
| *(omitted)* | *(none)* | Knowledge with no clear project or topic home |

Scope tags are **tool-managed**: preserved automatically by `edit_agent_memory` and `update_article_tags`, hidden from the sidebar tag cloud, and not freely assignable by users to non-memory documents. Filter memories by scope with `list_agent_memories(memory_type: "nexwiki")`.

> **Preservation applies only to `AI-Agent-Memory` documents.** That is the only class where the tag is genuinely tool-managed — `create_agent_memory` derives it from `memory_type`, and dropping it would orphan the memory from its scope. A `memory-*` tag sitting on a `Wiki`, `AI-Agent-Plan`, or `AI-Agent-Skill` document is stray data that no tool puts there, so it is **removable** by replacing that document's tags. It was not always: until this was fixed, such a tag survived every edit and `DeleteTagGlobally` refused it too, leaving it permanently stuck. Forging a new scope tag onto a non-memory document is still refused.

### 🛡️ Type rules & validation
To preserve integrity while keeping documents fully collaborative:
1. **Types are tool-assigned.** There is no user-facing type picker. The reserved `AI-Agent-*` values are set solely by `create_agent_memory` / `_plan` / `_skill`.
2. **Types are immutable on edit.** A reserved type is preserved through every edit and may **never** be relabelled to a non-reserved type. `update_article_tags` never touches `type` at all.
3. **Memories resist bulk deletion.** `delete_wiki_article` refuses a document of type `AI-Agent-Memory` and steers the agent to `delete_agent_memory`, so curated memories survive cleanup sweeps.
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
When you launch a complex project, either you or your connected AI assistant can create an implementation roadmap. `create_agent_plan` sets `type: AI-Agent-Plan` and applies a project tag from its `project_context` argument (e.g. `nexwiki`):
```markdown
# Migration to Go 1.22 Plan 🚀

— [x] Task 1: Audit code for old mux patterns
— [/] Task 2: Refactor routing to support wildcard path values
— [ ] Task 3: Run comprehensive integration test suite
```
The page slug is named directly after the feature (e.g. `migration-to-go-122`). Both you and your AI agent can collaboratively edit, check tasks, and complete this plan. The page remains safely stored under your **📋 AI plans** directory, keeping your main wiki page list clean.

When the work is finished, the agent appends closing notes with `append_agent_plan` and then swaps the plan's status to `completed` via `edit_agent_plan` (replacing `implementing` — a plan carries exactly one lifecycle status). The `AI-Agent-Plan` type is preserved through both operations.

### 3. AI-Driven Troubleshooting Log (type `AI-Agent-Memory`)
If a server build fails, the agent can document the investigation with `create_agent_memory`:
* **Title**: `Go Build Error May 2026`
* **`memory_type`**: `nexwiki` → applies the tool-managed scope tag `memory-nexwiki`
* **Additional user tags**: `backend`
* **Content**: Logs the specific error message, hypotheses tested, steps taken, and the final solution (e.g., importing the missing `strings` package).
* **Benefit**: The next time a build error occurs, the agent (or you!) can run `list_agent_memories(memory_type: "nexwiki")`, or search `ai-agent build error`, to find past resolutions instantly — avoiding repeated debugging.
