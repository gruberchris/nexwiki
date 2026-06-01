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

*Note: NexWiki does not allow global tag renaming. To rename a tag, apply the new tag name to the desired articles and delete the old tag.*

### 3. Searching and Filtering by User Tags
* **Tag Filter Cloud**: The sidebar displays all unique user tags currently in use. Click a tag badge in the cloud to instantly filter your Articles directory to show only articles containing that tag. Click **Clear** to reset.
* **Badges**: In the article reader, clicking any tag badge under the title instantly triggers a filtered search for that tag.

---

## 🤖 Protected AI Agent Memories & Collaborative Plans

NexWiki separates AI-driven note-taking into three distinct, structured models:
1. **AI Agent Memories (`aiagent-memory-<type>`)**: Strictly AI-originated (e.g., troubleshooting logs, decision files, todos). While standard users can view and delete them, they are protected from manual creation in the UI.
2. **AI Agent Skills (`aiagent-skill`)**: Reusable agent instructions (`SKILL.md` format) that standard users can create, edit, and manage. Exposed as a custom skills registry.
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
