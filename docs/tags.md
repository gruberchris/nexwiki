# NexWiki Tags & AI Agent Memories Guide 🏷️🤖

NexWiki supports a dual-layer **Tagging System** designed to keep standard user notes organized while providing a completely isolated, protected storage layer for **AI Agent Memories** (such as plans, troubleshooting notes, conceptual memories, ADRs, todos, and rules).

This guide teaches you how standard and protected tags work in NexWiki and provides useful, practical examples for both humans and AI agents.

---

## 🏷️ Standard User Tags

Standard tags are user-created keywords applied to wiki articles to make categorizing, browsing, and filtering highly efficient.

### 1. Creating and Applying Tags
When editing any wiki article inside the split-pane **Editor**:
1. Locate the **Tags** input box underneath the article title.
2. Type a tag name (e.g., `frontend`, `database`, `recipes`).
3. Press **Enter** or type a **comma (`,`)** to commit the tag.
4. Click **Save Page** to write the tags to the article's front-matter.

Tags are saved directly inside the flat-file Markdown front-matter:
```yaml
---
title: Database Configuration
slug: database-configuration
created_at: 2026-05-31T15:00:00Z
updated_at: 2026-05-31T15:30:00Z
version: 3
edit_summary: Updated connection pool size
tags: database, backend, production
---
```

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

NexWiki recognizes a fixed set of **status tags** that signal the lifecycle state of any article or AI plan. These tags are displayed with **highest priority** on the home dashboard article cards, making it easy to see what's active, blocked, or done at a glance.

### Recognized Status Values

| Tag | Meaning |
|---|---|
| `draft` | Work in progress, not ready for review |
| `wip` | Actively being worked on |
| `in-progress` | Same as `wip` — task underway |
| `todo` | Queued but not yet started |
| `active` | Currently in use or being maintained |
| `review` | Ready for review by another person or agent |
| `ready` | Approved and ready to act on |
| `blocked` | Cannot proceed — waiting on a dependency |
| `pending` | Awaiting an external event or decision |
| `completed` | Fully implemented or resolved |
| `done` | Equivalent to `completed` |
| `archived` | Retired — kept for reference, no longer active |

### How Status Tags Work

Most status tags are **purely semantic labels** — they do not trigger automatic filtering, hiding, or routing in the backend. The `archived` tag is the exception: it has optional auto-deletion behavior.

* Applying `archived` to an article does **not** remove it from search results or move it to a separate folder. The article remains fully visible and searchable.
* To exclude archived articles from a filter query, use the negation operator explicitly: `!archived` in the sidebar filter or search bar.
* The filter help modals (accessible via the `?` icon in the filter bar) include examples like `draft OR wip !archived`.

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

* **In the Editor**: Type a status tag (e.g., `archived`) in the Tags input field. Status tags appear with higher visual priority than regular user tags on article cards.
* **Via MCP**: AI agents can apply status tags using `update_article_tags` or the `tags` parameter on `create_wiki_article`, `edit_wiki_article`, `create_agent_plan`, or `edit_agent_plan`. Call `get_status_tags` to retrieve the canonical list.
* **Via REST API**: Use `PUT /api/articles/{slug}/tags` with your updated tags array.

---

## 🤖 Protected AI Agent Memories & Collaborative Plans

NexWiki separates AI-driven note-taking into three distinct, structured models:
1. **AI Agent Memories (`aiagent-memory-<type>`)**: Strictly AI-originated (e.g., troubleshooting logs, decision files, todos). While standard users can view and delete them, they are protected from manual creation in the UI.
2. **AI Agent Skills (`aiagent-skill`)**: Reusable agent instructions (`SKILL.md` format) that standard users can create, edit, and manage. Exposed as a custom Skills Registry.
3. **Collaborative AI Plans (`aiagent-plan`)**: Roadmap files that can be created, edited, and completed by **either** the user or the AI agent.

### 🛡️ Secure Tag Rules & Validation
To preserve the integrity of AI memory systems while maintaining collaborative flexibility:
1. **Protected Memories**: Standard users **cannot manually create or add any new** tags starting with `aiagent-memory-`. These are reserved for agents.
2. **Locked Mode-Defining Tags**: Standard users create Custom Skills and AI Plans explicitly using the dedicated creation interface, which automatically applies and locks the corresponding `aiagent-skill` or `aiagent-plan` tag. These core type tags cannot be removed in the tag editor, protecting the integrity of the document type.
3. **Freedom to Edit & Delete**: Standard users **can fully edit, append, and delete** any AI-created memory or plan document, along with removing existing tags as they see fit.

### 🧹 Default Search & Sidebar Exclusion
To keep your personal workspace tidy, AI agent pages are cleanly isolated:
* **Sidebar Directories**: The sidebar separates articles into four directories:
  * **📚 Articles**: Shows standard wiki pages (hiding all `aiagent-` prefixed tags).
  * **📋 AI plans**: Collapsible folder listing collaborative roadmaps (tagged with `aiagent-plan`).
  * **🛠️ AI skills**: Collapsible folder listing custom agent skills (tagged with `aiagent-skill`).
  * **🤖 AI memories**: Collapsible folder listing standard memory logs (tagged with `aiagent-memory-` prefix).
* **Default Search**: Running a standard search will **auto-exclude** all articles possessing `aiagent-` tags—even if they share common project tags with standard wiki pages.
* **Explicit Search Bypassing**: The exclusion is bypassed only if you search by exact case-insensitive slug/title, or if the search query explicitly includes `aiagent-plan` (or `plan`), `aiagent-skill` (or `skill`), or `aiagent-memory`.

---

## 💡 Practical Examples & Guides

### 1. Organizing Standard User Notes
Imagine you are building a full-stack web application. You can use standard tagging to categorize your documentation:
* **`frontend` / `css` / `react`**: Applied to your UI component guidelines.
* **`backend` / `database` / `security`**: Applied to API specs and database setups.
* **`reference` / `cheatsheet`**: Applied to command shortcuts or quick-lookup syntax.

To see all your frontend guides, click the `frontend` tag pill in your sidebar tag cloud.

### 2. Collaborative Plan Tracking (`aiagent-plan`)
When you launch a complex project, either you or your connected AI assistant can create an implementation roadmap (which is automatically tagged with `aiagent-plan` and a custom project tag like `nexwiki`):
```markdown
# Migration to Go 1.22 Plan 🚀

— [x] Task 1: Audit code for old mux patterns
— [/] Task 2: Refactor routing to support wildcard path values
— [ ] Task 3: Run comprehensive integration test suite
```
The page slug is named directly after the feature (e.g. `migration-to-go-122`). Both you and your AI agent can collaboratively edit, check tasks, and complete this plan. The page remains safely stored under your **📋 AI plans** directory, keeping your main wiki page list clean.

### 3. AI-Driven Troubleshooting Log (`aiagent-memory-troubleshooting`)
If a server build fails, the agent can document the investigation:
* **Title**: `Go Build Error May 2026`
* **Tags (Auto-applied)**: `aiagent-memory-troubleshooting`, `backend`
* **Content**: Logs the specific error message, hypotheses tested, steps taken, and the final solution (e.g., importing the missing `strings` package).
* **Benefit**: The next time a build error occurs, the agent (or you!) can explicitly search for `aiagent-memory-troubleshooting` or `backend` to find past resolutions instantly, avoiding repeated debugging.
