# NexWiki Model Context Protocol (MCP) Server Guide 🤖

NexWiki is designed as an **AI-ready second brain**. In addition to providing a beautiful personal knowledge base web application, NexWiki runs an **always-on Model Context Protocol (MCP) server** directly inside its compiled executable. 

This protocol acts as a standardized bridge allowing AI agents (like Claude Desktop, Cursor, or custom LLM systems) to query, read, and explore your personal wiki in real-time. By connecting your agent to NexWiki, you empower it to reason with access to your entire personal knowledge base.

---

## 🏗️ Architectural Overview

The NexWiki MCP server supports two primary transport layers:

1. **Stdio (Standard Input/Output) [JSON-RPC 2.0]**: Typically used for local server-agent processes. The agent runs the NexWiki binary or spins up the Docker container directly, piping JSON-RPC 2.0 messages via standard input/output.
2. **Streamable HTTP**: A modern, networked connection over HTTP at `/api/mcp`, the official successor to the deprecated HTTP+SSE transport. POST carries every JSON-RPC message.

### 🕰️ Protocol Revisions: NexWiki is Dual-Era

The MCP specification changed shape in revision **`2026-07-28`**. NexWiki implements **both eras on the same endpoint** and picks per request, so old and new clients work side by side with no configuration.

| | **Modern** (`2026-07-28`) | **Legacy** (`2025-06-18` and earlier) |
|---|---|---|
| Handshake | None — stateless | `initialize` |
| Protocol version | `_meta` on **every** request | negotiated once at `initialize` |
| Sessions | None (`Mcp-Session-Id` ignored) | connection-scoped |
| Discovery | `server/discover` | `initialize` result |
| Results | carry `resultType: "complete"` | bare result object |
| Protocol errors | real HTTP status (`400`/`404`) | `200` with an error body |

**How NexWiki decides:** a request whose `params._meta` carries `io.modelcontextprotocol/protocolVersion` is served under the modern revision; anything else takes the legacy path. Both eras share the same 27 tools and the same 2 prompts — only the envelope differs.

#### Modern-era requirements

A modern request **must** carry per-request metadata:

```json
{
  "jsonrpc": "2.0", "id": 1, "method": "tools/list",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {},
      "io.modelcontextprotocol/clientInfo": { "name": "MyClient", "version": "1.0.0" }
    }
  }
}
```

Over HTTP it must also mirror those fields into headers, which NexWiki validates against the body — a mismatch means an intermediary could route on one value while the server acts on another, so it is rejected rather than reconciled:

| Header | Required for | Must match |
|---|---|---|
| `MCP-Protocol-Version` | every request | `_meta` protocol version |
| `Mcp-Method` | every request | the JSON-RPC `method` |
| `Mcp-Name` | `tools/call`, `prompts/get` | `params.name` |

`Mcp-Name` values may use the spec's Base64 sentinel form (`=?base64?...?=`); NexWiki decodes before comparing.

#### Error codes

| Code | Name | HTTP | Raised when |
|---|---|---|---|
| `-32020` | `HeaderMismatch` | `400` | a mirrored header disagrees with the body, or is missing |
| `-32022` | `UnsupportedProtocolVersion` | `400` | the requested revision is not implemented; `data.supported` lists what is |
| `-32602` | `InvalidParams` | `400` | a required `_meta` field is missing |
| `-32601` | `MethodNotFound` | `404` | unknown method (the `404` is how a dual-era client tells a modern server from a legacy one) |

#### `server/discover`

Modern servers must implement it. One request returns supported versions, capabilities, and identity — no handshake needed:

```bash
curl -X POST http://localhost:8080/api/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: server/discover" \
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{
        "io.modelcontextprotocol/protocolVersion":"2026-07-28",
        "io.modelcontextprotocol/clientCapabilities":{}}}}'
```

> **What NexWiki does not implement:** MCP Resources, and `subscriptions/listen` (the modern mechanism for long-lived server→client change notifications). `capabilities` advertises only `tools` and `prompts`, because claiming a capability the server does not serve is worse than omitting it. The standalone `GET` SSE stream and `Mcp-Session-Id` were removed by the 2026-07-28 revision; NexWiki keeps the `GET` stream only for legacy-era clients that open one.

### 🔒 Log Safety Guarantee
To prevent stdio pipe corruption (which breaks JSON-RPC communication in tools like Claude Desktop), **NexWiki redirects all internal system and web application logs exclusively to standard error (`Stderr`)**. Only valid JSON-RPC envelopes are ever output to `Stdout`.

---

## 🛠️ Exposed MCP Tools

> **Native OKF storage & document `type`.** Every NexWiki `.md` file is a conformant Open Knowledge Format (OKF v0.1) concept document at rest (real YAML front matter). Each document carries a `type` — exactly one of **`Wiki`** (regular articles) or the reserved **`AI-Agent-Memory`** / **`AI-Agent-Plan`** / **`AI-Agent-Skill`** classes, which only the agent tools set. The legacy `aiagent-*` *class* tags are gone; the class is now the `type`. System tags remain: **status tags** (e.g. `wip`, `completed`, `inbox`) and tool-managed **memory-scope tags** (`memory-<scope>`).

> **Stdio alongside a web primary (`-mcp-only`).** A normal launch binds the web port (and is the primary that persists the activity log); if it cannot bind, it halts rather than silently falling back. To run a stdio MCP server next to an always-running web primary — e.g., a Claude Desktop subprocess — start NexWiki with the **`-mcp-only`** flag (or `NEXWIKI_MCP_ONLY=true`); it skips the port bind entirely and serves all tools from the in-process storage layer. If it detects a running NexWiki web server, it forwards its activity events to it; with no NexWiki web server, it persists the log itself. The clean single-process recommendation remains Streamable HTTP (`claude mcp add --transport http ...`).

The NexWiki MCP server registers and exposes twenty-seven powerful tools for AI agents:

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
  Reads the Markdown file on disk, parses the front-matter metadata, and returns a plain text document listing the article Title, Slug, Created timestamp, Updated timestamp, Description and Source (when set), and the complete raw Markdown body. If other articles link to this page via WikiLinks, a `Linked from:` section is appended (capped at 15 entries) so agents can traverse the knowledge graph in reverse.

---

### 3. `list_articles`
Lists all articles currently available in your knowledge base. This acts as a directory index for the agent to understand what documentation exists.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the database and returns a bulleted plain text index containing the titles, URL-safe slugs, last-edited timestamps, and one-line summaries (when a `description` is set) for all active articles. For a sectioned, orientation-friendly index, prefer `get_context_overview`.

---

### 4. `create_wiki_article`
Creates a new wiki article with a given title and raw Markdown content body.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the new article (e.g. "React Hooks Guide").
  * `content` (string, **required**): The raw Markdown content of the article body.
  * `description` (string, **optional**): A one-line summary shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — the URL, document, or reference this knowledge came from. AI-created articles SHOULD cite their source.
  * `tags` (array of strings, **optional**): Status or user tags to apply to the article. Call `get_status_tags` to see the recognized status values (e.g. `draft`, `wip`). Tool-managed `memory-<scope>` tags are reserved and will be ignored if provided.
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
  * `description` (string, **optional**): New one-line summary. Omit or pass empty to preserve the existing description.
  * `source` (string, **optional**): New provenance reference. Omit or pass empty to preserve the existing source.
  * `tags` (array of strings, **optional**): Tags to set on the article (replaces existing user tags; tool-managed `memory-<scope>` tags are always preserved). Call `get_status_tags` to see the recognized status values (e.g. `completed`, `review`). Omit to leave existing tags unchanged.
  * `loaded_version` (integer, **required**): The current version number loaded by the AI agent.
  * `edit_summary` (string, **optional**): A summary detailing the modifications.
* **Behavior**:
  Employs **optimistic locking** to prevent write collision conflicts. If the `loaded_version` does not match the active version on disk, it aborts the writing with a conflict message (notifying the agent to re-fetch and try again). On success, it creates a new gzipped history backup snapshot (`.md.gz`), writes the updated flat Markdown file, and refreshes the search index.

---

### 6. `update_article_tags`
Directly updates the tags array of an existing wiki article without modifying its content body. This is the fastest and most token-efficient way to classify or re-classify an article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the article to update tags for.
  * `tags` (array of strings, **required**): The complete array of user/status tags to apply (replaces existing user tags; tool-managed `memory-<scope>` tags are always preserved). Call `get_status_tags` to see recognized status values.
  * `loaded_version` (integer, **optional**): The active version number of the article loaded by the client (helps detect multi-session edit collisions).
  * `edit_summary` (string, **optional**): Optional summary explaining the tag updates.
* **Behavior**:
  Validates and cleans the supplied tags (stripping reserved `memory-<scope>` prefixes from the user-supplied list while preserving any existing tool-managed tags), applies optimistic locking if `loaded_version` is provided, increments the version, and saves the updated front-matter without touching the Markdown body. The document's OKF `type` is never altered.

---

### 7. `delete_wiki_article`
Permanently deletes an existing wiki article and its associated resources.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the article to delete.
* **Behavior**:
  Permanently deletes the Markdown file, all its gzip revision backups, and its uploaded media files/assets from the server. It also de-indexes the page from Bleve. **Protected AI Agent Memories are refused** — the tool returns an error steering the agent to `delete_agent_memory`, preventing curated memories from being destroyed by bulk cleanup calls. (Human deletion via the web UI/REST API is unaffected.)

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
Creates a brand new protected AI Agent Memory document. The `memory_type` scopes the memory and determines its tool-managed scope tag. Memories must be **succinct and high-value** — they are loaded into agent context windows, so keep them short, specific, and free of repetition.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the memory article (e.g. "NexWiki MCP Tag Preservation Rules").
  * `content` (string, **required**): The raw Markdown content of the memory document. Prefer bullet points over paragraphs. One clear insight per memory.
  * `memory_type` (string, **optional**): Scopes the memory. Use a **project name** (e.g. `nexwiki`) for project-specific knowledge, a **topic name** (e.g. `docker`) for reusable cross-project knowledge, or **omit** for general knowledge. Applies a tool-managed `memory-<memory_type>` scope tag (e.g. `memory-nexwiki`), or no scope tag if omitted. The OKF document `type` is always set to `AI-Agent-Memory` regardless.
  * `description` (string, **optional**): One-line summary shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — where this knowledge came from (URL, document, or session context).
  * `edit_summary` (string, **optional**): Optional description summarizing why this memory was created.
* **Behavior**:
  Checks for slug collision, sets the OKF `type` to `AI-Agent-Memory`, applies a tool-managed `memory-<memory_type>` scope tag if a `memory_type` was provided, saves the Markdown file, commits the first version snapshot, and indexes the document in the search engine.
* **Memory hygiene**: Search for an existing memory before creating one. If a memory later becomes stale, use `edit_agent_memory` to correct it in place or `delete_agent_memory` to retire it — do not create near-duplicates.

---

### 12. `append_agent_memory`
Appends observations, subtask completions, or updates to the end of an existing protected AI Agent Memory page.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target memory article.
  * `content_to_append` (string, **required**): The raw Markdown text to append.
  * `edit_summary` (string, **optional**): Optional summary outlining what was appended.
* **Behavior**:
  Verifies that the target article is of OKF type `AI-Agent-Memory`, appends the new text cleanly with double newlines, creates a gzipped history backup snapshot, and saves the updated active file.

---

### 13. `list_agent_memories`
Lists all protected AI Agent Memory articles saved in your wiki.

* **Arguments**:
  * `memory_type` (string, **optional**): Optional filter by memory type (the project name, topic name, or other free-form value used at creation). For example, `nexwiki` returns only memories tagged `memory-nexwiki`.
* **Behavior**:
  Scans all active articles, isolates pages with OKF type `AI-Agent-Memory`, optionally filters by the `memory-<type>` scope tag, and returns a bulleted index of matches including titles, slugs, and active tags.

---

### 14. `create_agent_plan`
Creates a new Collaborative AI Plan that can be collaboratively edited/viewed by both the user and the agent. Sets the OKF `type` to `AI-Agent-Plan` — the reserved type is immutable and must **NEVER** be relabelled.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the plan (e.g., "Go 1.22 Migration Plan").
  * `content` (string, **required**): The raw Markdown content of the plan document.
  * `project_context` (string, **required**): The name of the project this plan is for (e.g. "nexwiki"). Generates a custom project tag.
  * `description` (string, **optional**): One-line summary shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — where this plan originated (URL, ticket, or session context).
  * `edit_summary` (string, **optional**): Optional summary detailing the creation of the plan.
* **Behavior**:
  Checks for slug collision, sets the OKF `type` to `AI-Agent-Plan`, applies a tag for the project name, saves the Markdown file, commits the first version snapshot, and indexes the plan in Bleve for search.
* **Plan Completion Workflow**:
  After a plan is fully implemented, use `append_agent_plan` to add final notes documenting the implementation (plan deviations, files created, tools used, unexpected challenges, or other observations). Then use `edit_agent_plan` to add the `completed` status tag to mark the plan as done.

---

### 15. `append_agent_plan`
Appends task status, observations, or checklists to an existing Collaborative AI Plan. Use this to log implementation progress as tasks are completed and to add final notes when a plan is fully implemented before marking it completed.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target plan.
  * `content_to_append` (string, **required**): The raw Markdown text to append to the end of the plan.
  * `edit_summary` (string, **optional**): Optional summary outlining the updates.
* **Behavior**:
  Verifies that the target article is of OKF type `AI-Agent-Plan`, appends the new text cleanly with double newlines, creates a gzipped history backup snapshot, and saves the updated plan.

---

### 16. `edit_agent_plan`
Modifies the title, content, tags, or edit summary of an existing Collaborative AI Plan. Uses optimistic locking to prevent concurrent edit conflicts. The reserved `AI-Agent-Plan` OKF type is immutable and must **NEVER** be relabelled. Use this to correct or rewrite plan content in-place, or to mark a plan as `completed` after implementation by adding the `completed` status tag.

* **Arguments**:
  * `slug` (string, **required**): The unique URL slug of the plan to edit.
  * `title` (string, **optional**): The updated title of the plan (preserves existing title if omitted).
  * `content` (string, **optional**): Replacement Markdown body. Omit to preserve existing content. Use `append_agent_plan` to add progress notes without replacing.
  * `tags` (array of strings, **optional**): Tags to set on the plan (replaces existing tags; the `AI-Agent-Plan` OKF type is always preserved). Use status tags to signal plan state — call `get_status_tags` to see recognized values (e.g. `completed`, `wip`, `blocked`).
  * `loaded_version` (integer, **required**): The current version number loaded by the AI agent for optimistic locking checks.
  * `edit_summary` (string, **optional**): Description summarizing what changed.
* **Behavior**:
  Verifies that the target article is an AI-Agent-Plan, checks `loaded_version` against the disk version for optimistic locking, updates title/content/tags while preserving the plan type, increments the version number, creates a gzipped history backup snapshot, and updates the Bleve search index.

---

### 17. `list_agent_plans`
Lists all Collaborative AI Plans (OKF type `AI-Agent-Plan`) currently saved inside the knowledge base.

* **Arguments**:
  * `project_context` (string, **optional**): An optional project context name to filter plans by.
  * `tag` (string, **optional**): An optional tag to filter plans by. Use a status tag to find plans in a specific state (e.g. `completed`, `wip`). Call `get_status_tags` to see all recognized status values.
* **Behavior**:
  Scans all active articles, isolates pages of OKF type `AI-Agent-Plan`, filters them by project context tag and/or additional tags if provided, and returns a bulleted index of matching plans.

---

### 18. `create_agent_skill`
Creates a new Custom AI Skill, automatically making it part of the custom Skills Registry. Sets the OKF `type` to `AI-Agent-Skill` — the reserved type is immutable and must **NEVER** be relabelled.

* **Arguments**:
  * `title` (string, **required**): The title of the skill (e.g., "Docker Container Pruning").
  * `content` (string, **required**): The raw Markdown content of the skill instructions (procedural SKILL.md format).
  * `description` (string, **optional**): One-line summary of what the skill does, shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — where this skill's procedure came from.
  * `tags` (array of strings, **optional**): Optional tags to apply to the skill. Use status tags to signal the skill's state — call `get_status_tags` to see recognized values (e.g. `draft`, `ready`).
  * `edit_summary` (string, **optional**): Optional summary describing why the skill was created.
* **Behavior**:
  Checks for slug collision, sets the OKF `type` to `AI-Agent-Skill`, applies any user-provided tags, saves the Markdown file, commits the first version snapshot, and indexes the skill in Bleve.

---

### 19. `list_agent_skills`
Lists all Custom AI Skills (OKF type `AI-Agent-Skill`) currently saved in the knowledge base.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans all active articles, isolates pages of OKF type `AI-Agent-Skill`, and returns a bulleted index of matching skills.

---

### 20. `get_status_tags`
Returns the canonical list of recognized status tags used to indicate the lifecycle state of wiki articles and AI plans.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Returns the server-authoritative list of status tag values along with usage tips. Call this before tagging articles, plans, or skills to ensure you use a recognized value. Status tags are displayed with the highest visual priority on the home dashboard. Output includes a tip about the plan completion workflow: after a plan is fully implemented, use `append_agent_plan` to add final notes, then use `edit_agent_plan` to add the `completed` status tag.

* **Recognized values**: `completed`, `done`, `wip`, `draft`, `in-progress`, `archived`, `active`, `todo`, `pending`, `review`, `blocked`, `ready`, `inbox`

---

### 21. `get_context_overview`
Returns a **cheap progressive-disclosure index** of the entire knowledge base — the recommended first call of any agent session. Each entry is one compact line: title, slug, one-line summary, tags, and updated date, grouped into Wiki Articles / Agent Memories / Agent Plans / Agent Skills sections.

* **Arguments**:
  * `type` (string, **optional**): Section filter — one of `articles`, `memories`, `plans`, or `skills`. Omit for the full overview.
* **Behavior**:
  Built from a single metadata-only pass over the article directory (no per-article content reads, so it stays fast at any wiki size). The summary shown per entry is the article's `description` front-matter field, falling back to the first content line when no description is set. Orient yourself with this overview, then call `read_article` on only the entries you actually need.

* **Sample output**:
```
NexWiki Context Overview (42 articles total)
Each line: Title (slug) — summary [tags] (updated). Use read_article(slug) to load full content.

== Wiki Articles (30) ==
- Go (go) — Compiled, statically typed language by Google [programming language] (updated 2026-06-08)
...
== Agent Memories (5) ==
...
```

---

### 22. `get_backlinks`
Lists all articles whose content links to a given article via double-bracket `[[WikiLinks]]` — reverse traversal of the knowledge graph.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the target article to find inbound links for.
* **Behavior**:
  Scans all article bodies (including the `home` dashboard) on demand for WikiLinks resolving to the target slug, skipping self-links. Returns an indexed plain-text list with titles, slugs, summaries, and updated timestamps, sorted the newest first. Useful before editing or deleting a page to see what references it. `read_article` also appends a compact `Linked from:` section automatically.

---

### 23. `edit_agent_memory`
Replaces or corrects an existing protected AI Agent Memory **in place** — the core memory-hygiene tool. Prefer this over creating a near-duplicate memory when facts go stale.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the memory to edit.
  * `title` (string, **optional**): New title (preserves existing if omitted).
  * `content` (string, **optional**): Full replacement of the memory's Markdown content (preserves existing if omitted; cannot be blank — use `delete_agent_memory` to retire a memory entirely). Use `append_agent_memory` to add without replacing.
  * `description` (string, **optional**): New one-line summary (preserves existing if omitted).
  * `source` (string, **optional**): New provenance reference (preserves existing if omitted).
  * `tags` (array of strings, **optional**): Tags to set (replaces existing user tags; tool-managed `memory-<scope>` tags are always preserved).
  * `loaded_version` (integer, **required**): The current version number loaded by the agent, for optimistic locking.
  * `edit_summary` (string, **optional**): Summary of what was corrected.
* **Behavior**:
  Verifies the target is of OKF type `AI-Agent-Memory`, checks `loaded_version` against the disk version (conflict errors instruct the agent to re-read the memory), merges the provided fields over existing values, preserves tool-managed `memory-<scope>` tags, increments the version, snapshots history, and re-indexes.

---

### 24. `delete_agent_memory`
Permanently deletes an obsolete or fully superseded protected AI Agent Memory.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the memory to delete.
* **Behavior**:
  Verifies the target is actually a protected memory (refuses standard articles — use `delete_wiki_article` for those), then removes the Markdown file, history backups, and search index entry. Prefer `edit_agent_memory` to correct a memory rather than deleting and recreating it.

---

### 25. `get_recent_activity`
Queries the **durable activity log** (`data/activity.jsonl`) to see what changed in the wiki and when — the "what happened since my last session?" tool.

* **Arguments**:
  * `since` (string, **optional**): Only return events newer than this. Accepts a Go duration (`30m`, `24h`, `168h`) or an RFC3339 timestamp (`2026-06-10T00:00:00Z`).
  * `limit` (integer, **optional**): Maximum events returned, newest kept (default 50, max 500).
  * `action` (string, **optional**): Filter by `create`, `edit`, `delete`, `read`, or `revert`.
  * `source` (string, **optional**): Filter by origin — `mcp` (AI tool calls) or `api` (human web UI actions).
* **Behavior**:
  Reads the persisted JSON Lines activity log written by the primary server process (every REST and MCP mutation/read event, deduplicated within 2-second windows), **spanning the active file plus rotated archives** so durable history survives rotation. Falls back to the in-memory 200-event ring buffer when no durable log exists yet. At 10 MB the active log is rotated aside into a **non-destructive, timestamped archive** (`activity-<UTC>.jsonl`) — earlier archives are never overwritten (optional retention cap via `NEXWIKI_ACTIVITY_MAX_ARCHIVES`, default unlimited). The Activity Drawer also pages this durable history via `GET /api/activity/log` ("Load older history"). Events from a different MCP process may lag by milliseconds while being forwarded to the primary.

* **Sample output**:
```
Recent wiki activity (3 events, oldest first):

2026-06-11 14:01:07 [api/edit] web-ui → 'Go' (go) by User
2026-06-11 14:02:33 [mcp/create] create_wiki_article → 'New Page' (new-page) by Claude
2026-06-11 14:03:22 [mcp/edit] edit_agent_memory → 'Build Quirk' (build-quirk) by Claude
```

### 26. `export_okf_bundle`
Exports the entire knowledge base as a conformant **Open Knowledge Format (OKF v0.1) bundle** (a `.zip`).

* **Arguments**: none.
* **Behavior**:
  Native files are already OKF YAML, so export synthesizes the **bundle hierarchy** from each document's `type` (`wiki/`, `aimemories/`, `aiplans/`, `aiskills/`), the reserved per-directory and root `index.md` files (the root carries `okf_version: "0.1"`), a date-grouped `log.md` built from the durable activity log, and translates `[[WikiLinks]]` into bundle-relative concept paths (`/wiki/<slug>.md`, OKF §5.1). The archive is written into the data directory and its path is returned. REST equivalent: `GET /api/okf/export` (streams the `.zip` as a download).

### 27. `import_okf_bundle`
Imports an **OKF v0.1 bundle** (`.zip`) from a filesystem path into the knowledge base.

* **Arguments**:
  * `path` (string, **required**): Filesystem path to the `.zip` bundle.
* **Behavior**:
  Walks the bundle, parses each non-reserved `.md` as an OKF concept document, maps its `type` (reserved value → agent class; otherwise `Wiki`), translates bundle-relative Markdown links back to `[[WikiLinks]]`, and creates/updates each article via the storage layer (dedup by slug; reserved `index.md`/`log.md` are consumed). The importer is **permissive** (OKF §9): a document with a missing/unknown type defaults to `Wiki` and is flagged in the returned conformance report rather than rejected. REST equivalent: `POST /api/okf/import` (multipart `file` upload).

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
        "-mcp-only",
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
claude mcp add nexwiki -- /path/to/your/compiled/nexwiki -mcp-only -data /path/to/your/wiki-data -name "My Personal Brain"
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
        "-mcp-only",
        "-data", "/path/to/your/wiki-data",
        "-name", "My Personal Brain"
      ]
    }
  }
}
```
