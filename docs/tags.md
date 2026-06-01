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

## 🤖 Protected AI Agent Memories (`aiagent-` prefixed tags)

AI Agent Memories are special, protected notes created by connected AI assistants (such as Claude Desktop, Cursor, or `agy` CLI) using dedicated Model Context Protocol (MCP) server tools. 

To keep your personal workspace tidy, these pages are marked with protected tags starting with the prefix `aiagent-`.

### 🛡️ Secure Tag Rules & Validation
To preserve the integrity of AI memory systems while maintaining absolute user control:
1. **No Manual Creation**: Standard users **cannot manually create or add any new** tags starting with the `aiagent-` prefix. Any attempts to input or save new `aiagent-` tags are automatically blocked by the Editor input box and filtered out by the server.
2. **Freedom to Edit & Delete**: Standard users **can fully edit and delete** `aiagent-` tagged memories and documents. In the Editor, they can also **delete/remove existing `aiagent-` tags** from an article, giving them total control over their notes.
3. **Global Tag Deletion**: Globally deleting `aiagent-` tags remains restricted to prevent bulk AI data corruption.

### 🧹 Default Search & Sidebar Exclusion
AI memories can grow large and clutter your daily personal wiki. NexWiki implements standard exclusion rules for AI agent pages:
* **Sidebar Directory**: The sidebar separates articles into two directories. Standard articles are listed under **📚 Articles** (which completely hides any page with an `aiagent-` tag). AI memories are grouped under a dedicated, collapsible **🤖 AI memories** section.
* **Default Search**: Running a standard search (e.g., searching "database") will **auto-exclude** all articles possessing `aiagent-` tags.
* **Explicit Search Bypassing**: If you explicitly search using the `aiagent-` tag name (e.g., searching `tags:aiagent-memory-plan` or `aiagent-memory-plan`), the exclusion is bypassed, and the matching agent memories are successfully returned.

---

## 💡 Practical Examples & Guides

### 1. Organizing Standard User Notes
Imagine you are building a full-stack web application. You can use standard tagging to categorize your documentation:
* **`frontend` / `css` / `react`**: Applied to your UI component guidelines.
* **`backend` / `database` / `security`**: Applied to API specs and database setups.
* **`reference` / `cheatsheet`**: Applied to command shortcuts or quick-lookup syntax.

To see all your frontend guides, click the `frontend` tag pill in your sidebar tag cloud.

### 2. AI-Driven Plan Tracking (`aiagent-memory-plan`)
When you launch a complex task, a connected AI assistant can call `create_agent_memory` with `memory_type: "plan"` to create a roadmap:
```markdown
# Migration to Go 1.22 Plan 🚀

— [x] Task 1: Audit code for old mux patterns
— [/] Task 2: Refactor routing to support wildcard path values
— [ ] Task 3: Run comprehensive integration test suite
```
This is saved as an article with the tag `aiagent-memory-plan` and an optional custom tag for the project name (e.g. `nexwiki`). The page slug is named directly after the feature (e.g. `migration-to-go-122`). As the agent completes milestones, it calls `append_agent_memory` to log accomplishments. The page remains safely stored under your **🤖 AI memories** directory, keeping your main wiki page list clean.

### 3. AI-Driven Troubleshooting Log (`aiagent-memory-troubleshooting`)
If a server build fails, the agent can document the investigation:
* **Title**: `Go Build Error May 2026`
* **Tags (Auto-applied)**: `aiagent-memory-troubleshooting`, `backend`
* **Content**: Logs the specific error message, hypotheses tested, steps taken, and the final solution (e.g., importing the missing `strings` package).
* **Benefit**: The next time a build error occurs, the agent (or you!) can explicitly search for `aiagent-memory-troubleshooting` or `backend` to find past resolutions instantly, avoiding repeated debugging.
