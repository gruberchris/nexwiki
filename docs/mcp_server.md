# NexWiki Model Context Protocol (MCP) Server Guide 🤖

NexWiki is designed as an **AI-ready second brain**. In addition to providing a beautiful personal knowledge base web application, NexWiki runs an **always-on Model Context Protocol (MCP) server** directly inside its compiled executable. 

This protocol acts as a standardized bridge allowing AI agents (like Claude Desktop, Cursor, or custom LLM systems) to query, read, and explore your personal wiki in real-time. By connecting your agent to NexWiki, you empower it to reason with access to your entire personal knowledge base.

---

## 🏗️ Architectural Overview

The NexWiki MCP server supports two primary transport layers:

1. **Stdio (Standard Input/Output) [JSON-RPC 2.0]**: Typically used for local server-agent processes. The agent runs the NexWiki binary or spins up the Docker container directly, piping JSON-RPC 2.0 messages via standard input/output.
2. **Streamable HTTP**: Enables a modern, secure networked connection over HTTP (2025 Spec, the official successor to the deprecated HTTP+SSE specification). It uses a streamable HTTP connection at `/api/mcp` supporting GET (initiating the stream) and POST (executing synchronous JSON-RPC commands) to execute tools.

### 🔒 Log Safety Guarantee
To prevent stdio pipe corruption (which breaks JSON-RPC communication in tools like Claude Desktop), **NexWiki redirects all internal system and web application logs exclusively to standard error (`Stderr`)**. Only valid JSON-RPC envelopes are ever output to `Stdout`.

---

## 🛠️ Exposed MCP Tools

The NexWiki MCP server registers and exposes twenty powerful, semantic tools for AI agents:

### 1. `search_wiki`
Performs a high-speed, full-text search across all wiki articles using the built-in **Bleve Search** engine.

* **Arguments**:
  * `query` (string, **required**): The search keywords or query string. Supports wildcards, quotes for exact matches, and boolean terms.
* **Behavior**:
  Executes the search query against the local Bleve index. It converts scored matches into a human-readable text block. To optimize LLM context usage, all HTML `<mark>` search highlight tags are automatically converted to clean Markdown bold formatting (`**`).

---

### 2. `read_article`
Retrieves the raw Markdown content and Yaml-style front-matter configurations of a specific article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target article (e.g. `home` or `setup-guide`).
* **Behavior**:
  Reads the Markdown file on disk, parses the front-matter metadata, and returns a plain text document listing the article Title, Slug, Created timestamp, Updated timestamp, and the complete raw Markdown body.

---

### 3. `list_articles`
Lists all articles currently available in your knowledge base. This acts as a directory index for the agent to understand what documentation exists.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the database and returns a bulleted plain text index containing the titles, URL-safe slugs, and last-edited timestamps for all active articles.

---

### 4. `create_wiki_article`
Creates a new wiki article with a given title and raw Markdown content body.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the new article (e.g. "React Hooks Guide").
  * `content` (string, **required**): The raw Markdown content of the article body.
  * `tags` (array of strings, **optional**): Status or user tags to apply to the article. Call `get_status_tags` to see the recognized status values (e.g. `draft`, `wip`). System `aiagent-*` tags are reserved and will be ignored if provided.
  * `edit_summary` (string, **optional**): A summary describing the reason for creating the page.
* **Behavior**:
  Automatically handles title slugification, checks for slug collisions, serializes the metadata block, commits the first version backup snapshot, saves the flat Markdown file on disk, and indexes the new article in Bleve for search.

---

### 5. `edit_wiki_article`
Modifies the title, Markdown content, tags, or edit the summary of an existing wiki article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL slug of the article to edit.
  * `title` (string, **required**): The updated title of the article.
  * `content` (string, **required**): The updated raw Markdown content of the article body.
  * `tags` (array of strings, **optional**): Tags to set on the article (replaces existing user tags; existing system `aiagent-*` tags are always preserved). Call `get_status_tags` to see the recognized status values (e.g. `completed`, `review`). Omit to leave existing tags unchanged.
  * `loaded_version` (integer, **required**): The current version number loaded by the AI agent.
  * `edit_summary` (string, **optional**): A summary detailing the modifications.
* **Behavior**:
  Employs **optimistic locking** to prevent write collision conflicts. If the `loaded_version` does not match the active version on disk, it aborts the writing with a conflict message (notifying the agent to re-fetch and try again). On success, it creates a new gzipped history backup snapshot (`.md.gz`), writes the updated flat Markdown file, and refreshes the search index.

---

### 6. `update_article_tags`
Directly updates the tags array of an existing wiki article without modifying its content body. This is the fastest and most token-efficient way to classify or re-classify an article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the article to update tags for.
  * `tags` (array of strings, **required**): The complete array of user/status tags to apply (replaces existing user tags; existing system `aiagent-*` tags are always preserved). Call `get_status_tags` to see recognized status values.
  * `loaded_version` (integer, **optional**): The active version number of the article loaded by the client (helps detect multi-session edit collisions).
  * `edit_summary` (string, **optional**): Optional summary explaining the tag updates.
* **Behavior**:
  Validates and cleans the supplied tags (stripping reserved `aiagent-*` prefixes from the user-supplied list while preserving any existing system tags), applies optimistic locking if `loaded_version` is provided, increments the version, and saves the updated front-matter without touching the Markdown body.

---

### 7. `delete_wiki_article`
Permanently deletes an existing wiki article and its associated resources.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the article to delete.
* **Behavior**:
  Permanently deletes the Markdown file, all its gzip revision backups, and its uploaded media files/assets from the server. It also de-indexes the page from Bleve.

---

### 8. `get_article_history`
Retrieves the full revision history log of a wiki page, showing version numbers, timestamps, and edit summaries.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the target article.
* **Behavior**:
  Scans the gzip history directory and returns a structured, bulleted plain text revision list of all historical edits made to the page.

---

### 9. `revert_article_version`
Reverts the active state of an article to a specific historical version number.

* **Arguments**:
  * `slug` (string, **required**): The URL slug of the target article.
  * `version` (integer, **required**): The historical version number to restore.
* **Behavior**:
  Extracts the compressed `.md.gz` snapshot of that version, restores it to the active flat file on disk, increments the active version number, and updates the search index.

---

### 10. `get_wiki_statistics`
Scans the entire knowledge base to compile total page stats and **autonomously scan for dead or broken internal WikiLinks** (e.g., `[[Missing Page]]`).

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the raw content of all wiki articles for double-bracket WikiLink references. It normalizes targets into slugs and matches them against active articles. It returns a summary text listing total pages, total WikiLinks, total broken links, and details on exactly which pages contain dead references so the AI agent can autonomously fix them!

---

### 11. `create_agent_memory`
Creates a brand new protected AI Agent Memory document. The `memory_type` scopes the memory and determines its protected tag. Memories must be **succinct and high-value** — they are loaded into agent context windows, so keep them short, specific, and free of repetition.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the memory article (e.g. "NexWiki MCP Tag Preservation Rules").
  * `content` (string, **required**): The raw Markdown content of the memory document. Prefer bullet points over paragraphs. One clear insight per memory.
  * `memory_type` (string, **optional**): Scopes the memory and sets the protected tag. Use a **project name** (e.g. `nexwiki`) for project-specific knowledge, a **topic name** (e.g. `docker`) for reusable cross-project knowledge, or **omit** for general knowledge. Becomes the tag `aiagent-memory-<memory_type>`, or bare `aiagent-memory` if omitted.
  * `edit_summary` (string, **optional**): Optional description summarizing why this memory was created.
* **Behavior**:
  Checks for slug collision, automatically attaches the protected tag (`aiagent-memory-<memory_type>` or bare `aiagent-memory`), saves the flat Markdown file, commits the first version snapshot, and indexes the document in the search engine.

---

### 12. `append_agent_memory`
Appends observations, subtask completions, or updates to the end of an existing protected AI Agent Memory page.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target memory article.
  * `content_to_append` (string, **required**): The raw Markdown text to append.
  * `edit_summary` (string, **optional**): Optional summary outlining what was appended.
* **Behavior**:
  Verifies that the target article is a protected agent memory (possesses at least one tag starting with `aiagent-memory-`), appends the new text cleanly with double newlines, creates a gzipped history backup snapshot, and saves the updated active file.

---

### 13. `list_agent_memories`
Lists all protected AI Agent Memory articles saved in your wiki.

* **Arguments**:
  * `memory_type` (string, **optional**): Optional filter by memory type (the project name, topic name, or other free-form value used at creation). For example, `nexwiki` returns only nexwiki project memories.
* **Behavior**:
  Scans all active articles, isolates pages tagged with `aiagent-memory` or any `aiagent-memory-*` prefix, optionally filters by the specified type, and returns a bulleted index of matches including titles, slugs, and active tags.

---

### 14. `create_agent_plan`
Creates a new Collaborative AI Plan that can be collaboratively edited/viewed by both the user and the agent. Automatically applies the protected `aiagent-plan` tag, which must **NEVER** be removed unless explicitly instructed.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the plan (e.g., "Go 1.22 Migration Plan").
  * `content` (string, **required**): The raw Markdown content of the plan document.
  * `project_context` (string, **required**): The name of the project this plan is for (e.g. "nexwiki"). Generates a custom project tag.
  * `edit_summary` (string, **optional**): Optional summary detailing the creation of the plan.
* **Behavior**:
  Checks for slug collision, automatically attaches the whitelisted `aiagent-plan` tag, applies a custom tag for the project name, saves the flat Markdown file, commits the first version snapshot, and indexes the plan in Bleve for search.
* **Plan Completion Workflow**:
  After a plan is fully implemented, use `append_agent_plan` to add final notes documenting the implementation (plan deviations, files created, tools used, unexpected challenges, or other observations). Then use `edit_agent_plan` to add the `completed` status tag to mark the plan as done.

---

### 15. `append_agent_plan`
Appends task status, observations, or checklists to an existing Collaborative AI Plan (must possess the `aiagent-plan` tag). Use this to log implementation progress as tasks are completed and to add final notes when a plan is fully implemented before marking it completed.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target plan.
  * `content_to_append` (string, **required**): The raw Markdown text to append to the end of the plan.
  * `edit_summary` (string, **optional**): Optional summary outlining the updates.
* **Behavior**:
  Verifies that the target article possesses the `aiagent-plan` tag, appends the new text cleanly with double newlines, creates a gzipped history backup snapshot, and saves the updated plan.

---

### 16. `edit_agent_plan`
Modifies the title, tags, or edit the summary of an existing Collaborative AI Plan. Uses optimistic locking to prevent concurrent edit conflicts. The `aiagent-plan` protected tag is strictly preserved and must **NEVER** be removed. Use this to mark a plan as `completed` after implementation by adding the `completed` status tag.

* **Arguments**:
  * `slug` (string, **required**): The unique URL slug of the plan to edit.
  * `title` (string, **optional**): The updated title of the plan (preserves existing title if omitted).
  * `tags` (array of strings, **optional**): Tags to set on the plan (replaces existing tags; `aiagent-plan` is always preserved). Use status tags to signal plan state — call `get_status_tags` to see recognized values (e.g. `completed`, `wip`, `blocked`).
  * `loaded_version` (integer, **required**): The current version number loaded by the AI agent for optimistic locking checks.
  * `edit_summary` (string, **optional**): Description summarizing what changed.
* **Behavior**:
  Verifies that the target article possesses the `aiagent-plan` tag, checks `loaded_version` against the disk version for optimistic locking, updates title/tags while preserving `aiagent-plan`, increments the version number, creates a gzipped history backup snapshot, and updates the Bleve search index.

---

### 17. `list_agent_plans`
Lists all Collaborative AI Plans (tagged with `aiagent-plan`) currently saved inside the knowledge base.

* **Arguments**:
  * `project_context` (string, **optional**): An optional project context name to filter plans by.
  * `tag` (string, **optional**): An optional tag to filter plans by. Use a status tag to find plans in a specific state (e.g. `completed`, `wip`). Call `get_status_tags` to see all recognized status values.
* **Behavior**:
  Scans all active articles, isolates pages that possess the `aiagent-plan` tag, filters them by project context tag and/or additional tags if provided, and returns a bulleted index of matching plans.

---

### 18. `create_agent_skill`
Creates a new Custom AI Skill, automatically making it part of the custom Skills Registry. Automatically applies the protected `aiagent-skill` tag, which must **NEVER** be removed unless explicitly instructed.

* **Arguments**:
  * `title` (string, **required**): The title of the skill (e.g., "Docker Container Pruning").
  * `content` (string, **required**): The raw Markdown content of the skill instructions (procedural SKILL.md format).
  * `tags` (array of strings, **optional**): Optional tags to apply to the skill. Use status tags to signal the skill's state — call `get_status_tags` to see recognized values (e.g. `draft`, `ready`).
  * `edit_summary` (string, **optional**): Optional summary describing why the skill was created.
* **Behavior**:
  Checks for slug collision, automatically attaches the `aiagent-skill` tag, applies any additional user tags, saves the flat Markdown file, commits the first version snapshot, and indexes the skill in Bleve.

---

### 19. `list_agent_skills`
Lists all Custom AI Skills (tagged with `aiagent-skill`) currently saved in the knowledge base.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans all active articles, isolates pages possessing the `aiagent-skill` tag, and returns a bulleted index of matching skills.

---

### 20. `get_status_tags`
Returns the canonical list of recognized status tags used to indicate the lifecycle state of wiki articles and AI plans.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Returns the server-authoritative list of status tag values along with usage tips. Call this before tagging articles, plans, or skills to ensure you use a recognized value. Status tags are displayed with the highest visual priority on the home dashboard. Output includes a tip about the plan completion workflow: after a plan is fully implemented, use `append_agent_plan` to add final notes, then use `edit_agent_plan` to add the `completed` status tag.

* **Recognized values**: `completed`, `done`, `wip`, `draft`, `in-progress`, `archived`, `active`, `todo`, `pending`, `review`, `blocked`, `ready`

---

## 🔌 Connecting Clients

To connect your AI agents (Claude Desktop, Cursor, Copilot CLI, Claude Code, or Google `agy` CLI) to NexWiki, you can choose between two transport models:

1. **Streamable HTTP (Recommended 🚀)**:
   Connects the client directly to your active running web server on port `8080` (at `http://localhost:8080/api/mcp`).
   * **Advantages**: Zero process overhead, and **completely avoids database file lock contentions** (since the active running Go server process maintains exclusive locks, and all clients share it over HTTP).
2. **Stdio (Process-Based Alternative 📦)**:
   The client spawns its own isolated background process of the `nexwiki` Go executable on demand.
   * **Disadvantage**: Since each Stdio client spawns a separate binary process, they might compete to acquire exclusive database/search index file locks if the active web server is already running, which can trigger file-locking errors. Use this only if you aren't running the web server interface.

---

### 1. Cursor IDE (Streamable HTTP Connection – Preferred)
NexWiki implements the modern **Streamable HTTP** transport (2025 Spec) at `/api/mcp`.

To connect Cursor:
1. Open **Cursor Settings** (gear icon in the top-right corner).
2. Go to **Features** → **MCP**.
3. Click **+ Add New MCP Server**.
4. Configure the server:
   * **Name**: `nexwiki`
   * **Type**: `Streamable HTTP` *(Note: select `SSE` as a fallback if your Cursor version does not list the new 2025 Streamable HTTP type yet)*
   * **URL**: `http://localhost:8080/api/mcp`
5. Click **Save**.

---

### 2. Claude Desktop (Preferred: Streamable HTTP)
Locate your Claude Desktop configuration file (`claude_desktop_config.json`):
* **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add the `nexwiki` server configuration block:

#### Option A: Streamable HTTP (Recommended)
```json
{
  "mcpServers": {
    "nexwiki": {
      "url": "http://localhost:8080/api/mcp"
    }
  }
}
```

#### Option B: Stdio Process Fallback
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "/path/to/your/compiled/nexwiki",
      "args": [
        "-data", "/path/to/your/wiki-data",
        "-name", "My Personal Brain"
      ]
    }
  }
}
```

---

### 3. Claude Code CLI (Preferred: Streamable HTTP)
Anthropic's terminal agent **Claude Code** (`claude` CLI) can dynamically connect to the active NexWiki server over HTTP/SSE.

#### Option A: Streamable HTTP (Recommended)
Run this command in your shell to register the running server:
```bash
claude mcp add --transport http nexwiki http://localhost:8080/api/mcp
```

#### Option B: Stdio Process Fallback
```bash
claude mcp add nexwiki -- /path/to/your/compiled/nexwiki -data /path/to/your/wiki-data -name "My Personal Brain"
```

---

### 4. GitHub Copilot CLI (Preferred: Streamable HTTP)
GitHub Copilot's CLI environment supports connecting to custom HTTP/SSE servers. Add this block to your Copilot config file (`~/.config/github-copilot/config.json`):

#### Option A: Streamable HTTP (Recommended)
```json
{
  "mcpServers": {
    "nexwiki": {
      "url": "http://localhost:8080/api/mcp"
    }
  }
}
```

#### Option B: Stdio Process Fallback
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "/path/to/your/compiled/nexwiki",
      "args": [
        "-data", "/path/to/your/wiki-data",
        "-name", "My Personal Brain"
      ]
    }
  }
}
```
