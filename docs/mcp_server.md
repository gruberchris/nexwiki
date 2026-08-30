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

**How NexWiki decides:** a request whose `params._meta` carries `io.modelcontextprotocol/protocolVersion` is served under the modern revision; anything else takes the legacy path. Both eras share the same 29 tools and the same 2 prompts — only the envelope differs.

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

> **Capabilities** advertise `tools`, `prompts`, and `resources` (with `listChanged` and `subscribe`) — each only because it is genuinely served. The standalone `GET` SSE stream and `Mcp-Session-Id` were removed by the 2026-07-28 revision; NexWiki ignores `Mcp-Session-Id` and keeps the `GET` stream only for legacy-era clients that open one.

### 🏷️ Tool Annotations — fewer approval prompts

Every tool carries MCP `annotations` telling your client what calling it actually does. Clients use these to **auto-approve safe reads and confirm destructive writes**, so an agent isn't interrupting you to run `get_context_overview` — the tool the agent skill says to call first in every session.

This matters because the spec's defaults are **pessimistic**: an unannotated tool is assumed `destructiveHint: true` and `openWorldHint: true`. Shipping no annotations tells every client that all 29 tools might destroy data and reach arbitrary external systems.

| Hint | NexWiki's values |
|---|---|
| `readOnlyHint` | `true` on **14** tools that never modify the wiki |
| `destructiveHint` | `false` for creates and appends; `true` for edits, deletes, tag replacement, revert, and bundle import |
| `idempotentHint` | `true` on the deletes — deleting an already-deleted document changes nothing further |
| `openWorldHint` | **`false` on every tool, without exception.** The entire surface operates on the local wiki directory and never reaches an external system |
| `title` | A human-readable display name, e.g. `Get Context Overview` |

**Read-only (14):** `search_wiki` · `read_article` · `list_articles` · `get_article_history` · `get_wiki_statistics` · `list_agent_memories` · `list_agent_plans` · `list_agent_skills` · `get_status_tags` · `get_recent_activity` · `get_backlinks` · `get_context_overview` · `export_okf_bundle` · `wiki_health`

**Additive writes (6)** — create new content, never overwrite: the four `create_*` tools plus `append_agent_memory` and `append_agent_plan`.

**Destructive writes (9)** — can overwrite or remove existing content: `edit_wiki_article` · `edit_agent_memory` · `edit_agent_plan` · `edit_agent_skill` · `update_article_tags` · `delete_wiki_article` · `delete_agent_memory` · `revert_article_version` · `import_okf_bundle`

> **Annotations are hints, not guarantees.** The specification is explicit that clients must treat them as untrusted from untrusted servers. They describe intent; the actual guards are the optimistic-locking checks and the reserved-type rules enforced inside the handlers.
>
> Note that every successful call — reads included — appends to the durable activity log. That is server-side audit bookkeeping, not a change to the content the tool operates on, so it does not disqualify `readOnlyHint`.

### 📤 Structured output — parse data, don't scrape prose

Twelve read tools declare an **`outputSchema`** and return a **`structuredContent`** object alongside their text. An agent that needs an article's version number to pass as `loaded_version` reads an integer instead of pulling one out of a sentence.

| | |
|---|---|
| Tools with `outputSchema` | `search_wiki` · `read_article` · `list_articles` · `list_agent_memories` · `list_agent_plans` · `list_agent_skills` · `get_backlinks` · `get_article_history` · `get_wiki_statistics` · `get_status_tags` · `get_recent_activity` · `wiki_health` |
| Prose only | every write tool, plus `get_context_overview` (progressive-disclosure prose is its whole purpose), `export_okf_bundle`, and `import_okf_bundle` |

Three properties hold across all of them:

- **The text is still there.** `structuredContent` is emitted *in addition to* `content`, and both are rendered from the same value, so they cannot disagree. A client that predates structured output sees no change.
- **Error results carry no `structuredContent`.** A payload that fails its own published schema is worse than no payload: every consumer would have to handle a shape the schema says cannot occur.
- **Field names match the REST API.** A document read over MCP and the same document read from `GET /api/articles` have identical keys, so an agent that has seen one already knows the other.

Document listings share one shape — `{ "count": N, "documents": [ … ] }` — across `list_articles` and the three `list_agent_*` tools, so a NexWiki listing is learned once. Listings carry metadata only; the body is what `read_article` is for.

```jsonc
// tools/call → read_article {"slug": "home"}
{
  "content": [{ "type": "text", "text": "Type: Wiki\nTitle: Home\n…" }],
  "structuredContent": {
    "article": {
      "type": "Wiki", "title": "Home", "slug": "home",
      "version": 4,                       // pass this as loaded_version when editing
      "timestamp": "2026-08-09T12:00:00Z",
      "tags": ["index"], "content": "# Home\n\n…"
    },
    "backlinks": [{ "title": "Guides", "slug": "guides" }]
  }
}
```

> `search_wiki`'s structured snippets are **plain text with Markdown bold**, not the HTML the browser sidebar renders. Handing an agent `<mark>` markup invites it to paste that markup back into an article.
>
> The structured payload also echoes the applied facets (`type`, `tags`, `include_archived`), so an agent can tell "no such knowledge" from "my filter excluded it" without re-reading the prose.

## 📎 Resources — `@`-mention a wiki page

Tools are *model*-controlled: the agent decides to call them. **Resources are application-driven** — your client surfaces them for *you* to pick, which is what makes `@`-mentioning a wiki page work in Claude Desktop or Cursor. That path costs no tool call and no tokens spent on tool-result prose, so it is a different affordance from `read_article`, not a duplicate.

Every document — wiki articles, agent memories, plans, and skills — is exposed as a resource:

| | |
|---|---|
| URI | `nexwiki://article/{slug}` |
| `mimeType` | `text/markdown` |
| `name` / `title` | slug / article title |
| `description` | the article's description, falling back to its first line |
| `annotations` | `lastModified`, plus `audience: [user, assistant]` |

A custom scheme rather than `file://`: an article's identity here is its **slug**, not its path on disk, and encoding the path would leak the data directory layout into every client.

```bash
# List every document as a resource
curl -X POST http://localhost:8080/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}'

# Read one
curl -X POST http://localhost:8080/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"nexwiki://article/home"}}'
```

`resources/templates/list` advertises `nexwiki://article/{slug}`, so a client that already knows a slug can build the URI without paging the whole list.

A missing resource returns `-32602` with the URI echoed in `data` — never an empty `contents` array, which the spec forbids because it cannot be distinguished from a resource that exists but is empty.

---

## 📡 Subscriptions — a live, subscribable knowledge base

`subscriptions/listen` opens a long-lived stream. **An agent holding one learns the moment you edit a page in the browser, or another agent writes a memory** — no polling.

The events already existed: the same `EventBus` has been driving the browser's live activity drawer all along. This wires that signal to a second consumer.

```bash
curl -N -X POST http://localhost:8080/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":77,"method":"subscriptions/listen","params":{"notifications":{
        "resourcesListChanged": true,
        "resourceSubscriptions": ["nexwiki://article/bleve-decision"]}}}'
```

Edit that article in your browser, and the stream delivers:

```
notifications/subscriptions/acknowledged     sub=77
notifications/resources/updated              sub=77  nexwiki://article/bleve-decision
notifications/resources/list_changed         sub=77
```

| Filter field | Supported | Fires when |
|---|---|---|
| `resourceSubscriptions` | ✅ | one of the listed articles is edited |
| `resourcesListChanged` | ✅ | any document is created or deleted |
| `toolsListChanged` | ❌ | — |
| `promptsListChanged` | ❌ | — |

**Why the last two are declined rather than accepted silently:** NexWiki's tool and prompt sets are compiled into the binary and cannot change while the process runs. Acknowledging them would promise a notification that can never arrive. The acknowledgment reports only what the server will actually deliver, so a client knows immediately rather than waiting on silence. A subscription that asks *only* for those is closed gracefully with the spec's empty result instead of holding an idle socket open.

Every message carries `io.modelcontextprotocol/subscriptionId` in `_meta` so concurrent subscriptions can be demultiplexed. Cancellation is closing the stream.

> **Transport:** a *standalone* stdio server cannot hold subscriptions open — its loop is strictly request/response on one channel — so it acknowledges and closes gracefully. A stdio **sidecar next to a running web server does** get live subscriptions, because it proxies to the primary and relays the stream. See [Sidecar proxy mode](#-sidecar-proxy-mode) below.

## 🔀 Sidecar proxy mode

Only one process can own a wiki: the Bleve index takes an exclusive lock on the data directory. So a `-mcp-only` sidecar pointed at a *running* instance cannot open it.

That is exactly the documented Claude Desktop stdio configuration. It used to hang forever on the lock; then it failed fast with an explanation — honest, but the setup still did not work. **Now it works:** when a sidecar detects a primary on the configured port, it does not open storage at all. It becomes a pipe, forwarding each stdio JSON-RPC message to the primary's `/api/mcp` and writing the reply back to stdout.

```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "docker",
      "args": ["exec", "-i", "personal-wiki", "/app/nexwiki", "-mcp-only", "-data", "/app/data"]
    }
  }
}
```

```
-mcp-only: web server detected on port 8080; running as a proxy to it.
           The primary owns the data directory; this process forwards MCP traffic to it.
```

**What this buys you beyond "it starts":**

- **Writes land in the live wiki.** The call executes *inside* the primary, so the browser sees the change immediately and the activity log records it once, in the process that did the work.
- **stdio gets live subscriptions.** The primary answers `subscriptions/listen` with an SSE stream, and the proxy relays each notification to stdout as its own JSON-RPC line. A standalone stdio server cannot offer this at all.
- **No second index, no lock contention, no divergence.** There is one owner of the data directory, always.

The proxy synthesizes the modern era's mirrored headers (`MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`) from the body it forwards, since stdio carries no headers and the primary validates them. If the primary is unreachable, the proxy answers with a JSON-RPC error carrying the original request id, so the failure is attributable to the call that caused it.

> Streamable HTTP is still the simplest option when your client supports it — one process, no subprocess at all. Proxy mode exists so the stdio path is no longer a trap.

### 🔒 Log Safety Guarantee
To prevent stdio pipe corruption (which breaks JSON-RPC communication in tools like Claude Desktop), **NexWiki redirects all internal system and web application logs exclusively to standard error (`Stderr`)**. Only valid JSON-RPC envelopes are ever output to `Stdout`.

### 📏 Stdio message size

A single JSON-RPC line on stdio may be up to **8 MB**, in both the standalone stdio server and the sidecar proxy. This is far above bufio's 64 KB default because MCP payloads carry whole article bodies — a `create_wiki_article` call with a long document passes 64 KB easily.

Exceeding the cap is **not recoverable**: the read loop ends and the stdio channel stops answering for the life of the process. NexWiki emits a JSON-RPC parse error naming the limit before it goes quiet, so the failure is attributable rather than silent, but the session is over. Use the Streamable HTTP transport for payloads that large — it has no per-message line limit (its own body caps are separate and much higher).

---

## 🛠️ Exposed MCP Tools

> **Native OKF storage & document `type`.** Every NexWiki `.md` file is a conformant Open Knowledge Format (OKF v0.1) concept document at rest (real YAML front matter). Each document carries a `type` — exactly one of **`Wiki`** (regular articles) or the reserved **`AI-Agent-Memory`** / **`AI-Agent-Plan`** / **`AI-Agent-Skill`** classes, which only the agent tools set. The legacy `aiagent-*` *class* tags are gone; the class is now the `type`. System tags remain: **status tags** (e.g. `wip`, `completed`, `inbox`) and tool-managed **memory-scope tags** (`memory-<scope>`).

> **Stdio alongside a web primary (`-mcp-only`).** A normal launch binds the web port (and is the primary that persists the activity log); if it cannot bind, it halts rather than silently falling back. To run a stdio MCP server next to an always-running web primary — e.g., a Claude Desktop subprocess — start NexWiki with the **`-mcp-only`** flag (or `NEXWIKI_MCP_ONLY=true`); it skips the port bind entirely and serves all tools from the in-process storage layer. If it detects a running NexWiki web server, it forwards its activity events to it; with no NexWiki web server, it persists the log itself. The clean single-process recommendation remains Streamable HTTP (`claude mcp add --transport http ...`).

The NexWiki MCP server registers and exposes twenty-nine powerful tools for AI agents:

### 1. `search_wiki`
Performs a high-speed, full-text search across the **entire** knowledge base using the built-in **Bleve Search** engine — wiki articles *and* your agent memories, plans, and skills.

* **Arguments**:
  * `query` (string, **required**): The search keywords or query string. Supports wildcards, quotes for exact matches, and boolean terms.
  * `type` (array of string, *optional*): Restrict to document types — `articles`, `memories`, `plans`, `skills`. Canonical OKF names (`AI-Agent-Memory`) also work. Omit to search every type.
  * `tags` (array of string, *optional*): A result must carry **all** of these tags (case-insensitive), e.g. `["wip"]` or `["memory-nexwiki"]`.
  * `limit` (integer, *optional*): Maximum results. Default `40`, maximum `200`.
  * `include_archived` (boolean, *optional*): Include archived documents, which are excluded by default.
  * `memory_kind` (string, *optional*): Narrow to agent memories of one kind — `project`, `reference`, `user`, or `feedback`. Only memories carry a kind, so supplying this necessarily excludes every other document class rather than matching them on an empty field. An unrecognized value is reported, not answered with an empty result.
* **Behavior**:
  Executes the query against the local Bleve index and converts scored matches into a readable text block, reporting each hit's document `Type` so you can tell a memory from an article. HTML `<mark>` highlights become Markdown bold (`**`) to save context. When facets are applied they are echoed in the response header line, so an empty result set is distinguishable from an over-narrow filter. An unrecognized `type` value is reported as an error rather than silently returning nothing.

> **Agents search everything by default.** Earlier versions hid memories, plans, and skills unless the *query text* happened to contain the words "memory", "plan", or "skill". That meant a memory recording *"we chose Bleve over Elasticsearch"* was invisible to `search_wiki("elasticsearch")`, and the agent would re-derive a decision it had already stored. Agent-facing search now spans every type unless you narrow it with `type`. The browser sidebar is unchanged and still hides agent documents from human searches.

**Examples**

```jsonc
// Everything about a topic, across articles and your own memories
{ "query": "elasticsearch" }

// Only what you remember about this project
{ "query": "retrieval", "type": ["memories"], "tags": ["memory-nexwiki"] }

// In-flight plans, newest handful
{ "query": "migration", "type": ["plans"], "tags": ["wip"], "limit": 5 }
```
* **Structured output**: `structuredContent` as `{query, count, type, tags, memory_kind, include_archived, results[]}`. Each result carries `title`, `slug`, `type`, `score`, `timestamp`, `tags`, and plain-text `snippets`.

---

### 2. `read_article`
Retrieves the raw Markdown content and Yaml-style front-matter configurations of a specific article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target article (e.g. `home` or `setup-guide`).
* **Behavior**:
  Reads the Markdown file on disk, parses the front-matter metadata, and returns a plain text document listing the article Title, Slug, Created timestamp, Updated timestamp, Description and Source (when set), and the complete raw Markdown body. If other articles link to this page — via `[[WikiLinks]]` or absolute `/articles/<slug>` Markdown links — a `Linked from:` section is appended (capped at 15 entries) so agents can traverse the knowledge graph in reverse.
* **Structured output**: `structuredContent` as `{article, backlinks[]}`. The article includes `version` — pass it straight to `edit_wiki_article` as `loaded_version`. Unlike the prose, `backlinks` is not capped at 15.

---

### 3. `list_articles`
Lists all articles currently available in your knowledge base. This acts as a directory index for the agent to understand what documentation exists.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the database and returns a bulleted plain text index containing the titles, URL-safe slugs, last-edited timestamps, and one-line summaries (when a `description` is set) for all active articles. For a sectioned, orientation-friendly index, prefer `get_context_overview`.
* **Structured output**: `structuredContent` as `{count, documents[]}`, the shared listing shape. Metadata only; bodies come from `read_article`.

---

### 4. `create_wiki_article`
Creates a new wiki article with a given title and raw Markdown content body.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the new article (e.g. "React Hooks Guide"). Use the subject's own name — never a tool name, an action verb, or a placeholder, since the slug is derived from it and a wrong title strands the article at a meaningless URL.
  * `content` (string, **required**): The raw Markdown content of the article body.
  * `description` (string, **optional**): A one-line summary shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — the URL, document, or reference this knowledge came from. AI-created articles SHOULD cite their source.
  * `tags` (array of strings, **optional**): Any tags you like — wiki articles are never policed and have no lifecycle status. Tool-managed `memory-<scope>` tags are reserved and will be ignored if provided.
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
  Employs **optimistic locking** to prevent write collision conflicts. If the `loaded_version` does not match the active version on disk, the write aborts with a conflict message. On success, it creates a new gzipped history backup snapshot (`.md.gz`), writes the updated flat Markdown file, and refreshes the search index.

> **A conflict names the value to retry with.** Every optimistic-locking failure — on `edit_wiki_article`, `edit_agent_memory`, `edit_agent_plan`, `edit_agent_skill`, and `update_article_tags` — reports the version on disk *and* the exact `loaded_version` to send next:
>
> ```
> Error: version conflict on 'home'. The article is at version 15 on disk; you sent
> loaded_version 14. Retry once with loaded_version: 15. Re-read only if you need the
> current content before overwriting it — sending 14 again will fail identically.
> ```
>
> **One corrected retry is the expected resolution**, not a re-read. The earlier wording said only "re-fetch and try again", which names no value and sets no bound: a client that mis-threads `loaded_version` re-reads, retries, and can mis-thread again, with nothing capping the cycle. That is the same unbounded-precondition shape that once had an agent alternating `read_article` and `search_wiki` for 31 minutes. The server knows the answer at the moment it rejects the call, so it gives it.
>
> The one exception is `edit_wiki_article` when the article cannot be read back after the conflict — there is genuinely no version to name, and the message says to re-read.

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
Retrieves the full revision history log of a wiki page, showing version numbers, timestamps, edit summaries, and **who made each revision**.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the target article.
* **Behavior**:
  Scans the gzip history directory and returns a structured, bulleted plain text revision list of all historical edits made to the page. Each revision is joined against the activity log to attribute it to the agent that made it.
* **Structured output**: `structuredContent` as `{slug, count, source, versions[]}`. Each version carries `version`, `timestamp`, and `edit_summary` — the three fields a revert decision needs — plus `agent`, `tool`, and `via` when the activity log has a matching event. Top-level `source` is the article's own OKF provenance field: where the knowledge came from, as distinct from who typed it.

> **Attribution is a hint, not an identity claim.** `agent` is whatever the MCP client reported as its `clientInfo`, or the server's configured `NEXWIKI_AGENT_NAME` for clients that report nothing. NexWiki is unauthenticated (see `SECURITY.md`), so nothing may treat this field as proof of who made a change.
>
> The fields are **omitted, not blank**, when no matching event exists. That is the normal case for revisions older than the activity log, or older than its retention window. Matching is by timestamp proximity within 5 seconds rather than by position, because the log can rotate or be pruned and an off-by-one would attribute every subsequent revision to the wrong agent.

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
Scans the entire knowledge base to compile total page stats and **autonomously scan for dead or broken internal links** — both `[[Missing Page]]` WikiLinks and absolute `[text](/articles/missing-page)` Markdown links.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the raw content of all wiki articles for internal links in **both** supported forms. It normalizes targets into slugs and matches them against active articles. It returns a summary text listing total pages, total internal links, total broken links, and details on exactly which pages contain dead references so the AI agent can autonomously fix them!
* **Structured output**: `structuredContent` as `{total_articles, total_links, broken_link_count, broken_links[]}`. Each broken link names `from_slug`, the raw `target` as the author wrote it, the `target_slug` a fix has to create, and the `form` it was written in (`"wikilink"` or `"markdown"`) — the two are repaired differently, and an agent told to fix `[[rust]]` in a file that actually says `[Rust](/articles/rust)` will not find it.

---

### 11. `create_agent_memory`
Creates a brand new protected AI Agent Memory document. Memories must be **succinct and high-value** — they are loaded into agent context windows, so keep them short, specific, and free of repetition.

> **A memory has two independent axes.** `memory_kind` is **what sort of fact** it is — a closed vocabulary of four, in a field. `memory_type` is **how far the fact reaches** — free-form, and it becomes the tool-managed `memory-<scope>` tag. Every combination is legal, and each is filterable on its own. The split follows the rule NexWiki learned with lifecycle status: closed vocabularies are fields, open vocabularies are tags.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the memory article (e.g. "NexWiki MCP Tag Preservation Rules").
  * `content` (string, **required**): The raw Markdown content of the memory document. Prefer bullet points over paragraphs. One clear insight per memory.
  * `memory_kind` (string, **required**): What sort of fact this memory holds. One of:
    | Kind | Holds |
    |---|---|
    | `project` | Goals and constraints **not derivable** from the repo or its git history |
    | `reference` | A pointer to an external resource — dashboard, ticket, host, URL |
    | `user` | Who the operator is: role, expertise, standing preferences |
    | `feedback` | A correction the operator gave — record the *why* and *how to apply it*, not just what was said |

    Anything outside the vocabulary is rejected with the list; an absent value is rejected too, because the classification is only cheap at intake. The agent writing a memory knows what sort of fact it is, and nobody reading it back later reliably does.
  * `memory_type` (string, **optional**): Scopes the memory. Use a **project name** (e.g. `nexwiki`) for project-specific knowledge, a **topic name** (e.g. `docker`) for reusable cross-project knowledge, or **omit** for general knowledge. Applies a tool-managed `memory-<memory_type>` scope tag (e.g. `memory-nexwiki`), or no scope tag if omitted. The OKF document `type` is always set to `AI-Agent-Memory` regardless.
  * `description` (string, **optional**): One-line summary shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — where this knowledge came from (URL, document, or session context).
  * `tags` (array of string, **optional**): Status or user tags to apply, e.g. `["review"]`. Call `get_status_tags` for the recognized status values. The tool-managed `memory-<memory_type>` scope tag is added automatically and **cannot be set here** — a caller-supplied `memory-*` tag is dropped.
  * `edit_summary` (string, **optional**): Optional description summarizing why this memory was created.
* **Behavior**:
  Validates `memory_kind` against the closed vocabulary, checks for slug collision, sets the OKF `type` to `AI-Agent-Memory`, applies a tool-managed `memory-<memory_type>` scope tag if a `memory_type` was provided, merges any caller `tags` on top of it, saves the Markdown file, commits the first version snapshot, and indexes the document in the search engine.
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
  * `memory_type` (string, **optional**): Filter by **scope** (the project name, topic name, or other free-form value used at creation). For example, `nexwiki` returns only memories tagged `memory-nexwiki`.
  * `memory_kind` (string, **optional**): Filter by **kind** — `project`, `reference`, `user`, or `feedback`. An unrecognized value is reported rather than answered with an empty list, since an empty result reads as "no such knowledge". Ask for `user` and `feedback` to load what is known about the operator and how they want work done.
* **Behavior**:
  Scans all active articles, isolates pages with OKF type `AI-Agent-Memory`, applies each filter on its own axis, and returns a bulleted index of matches including titles, slugs, kind, and active tags. **The two filters compose**: supplying both means both, so `memory_type: "nexwiki"` with `memory_kind: "reference"` returns the external-resource pointers for that project only.
* **Structured output**: `structuredContent` as `{count, documents[]}`, the shared listing shape. Kind is the `memory_kind` field; scope lives in each document's `memory-<scope>` tags.

---

### 14. `create_agent_plan`
Creates a new Collaborative AI Plan that can be collaboratively edited/viewed by both the user and the agent. Sets the OKF `type` to `AI-Agent-Plan` — the reserved type is immutable and must **NEVER** be relabelled.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the plan (e.g., "Go 1.22 Migration Plan").
  * `content` (string, **required**): The raw Markdown content of the plan document.
  * `project_context` (string, **required**): The name of the project this plan is for (e.g. "nexwiki"). Generates a custom project tag.
  * `description` (string, **optional**): One-line summary shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — where this plan originated (URL, ticket, or session context).
  * `status` (string, **optional**): Lifecycle status — `draft` (default), `implementing`, `blocked`, `completed`, `superseded`, `parked`, `evergreen`, or `archived`. Lets a plan be created already in flight in **one call**. An unrecognized value is rejected.
  * `tags` (array of string, **optional**): Project-context and topic tags. Lifecycle state does **not** go here; a status word passed as a tag is rejected. Tool-managed `memory-*` tags are reserved and dropped.
  * `edit_summary` (string, **optional**): Optional summary detailing the creation of the plan.
* **Behavior**:
  Checks for slug collision, sets the OKF `type` to `AI-Agent-Plan`, applies a tag for the project name, merges any caller `tags` on top of it, saves the Markdown file, commits the first version snapshot, and indexes the plan in Bleve for search.
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
  * `status` (string, **optional**): New lifecycle status, e.g. `completed` when the work ships. **Omit to preserve** the plan's current state — an edit that does not manage lifecycle can never reset it.
  * `tags` (array of strings, **optional**): Tags to set on the plan — project context and topics only (replaces existing tags; the `AI-Agent-Plan` OKF type is always preserved). A status word passed as a tag is rejected.
  * `loaded_version` (integer, **required**): The current version number loaded by the AI agent for optimistic locking checks.
  * `edit_summary` (string, **optional**): Description summarizing what changed.
* **Behavior**:
  Verifies that the target article is an AI-Agent-Plan, checks `loaded_version` against the disk version for optimistic locking, updates title/content/tags while preserving the plan type, increments the version number, creates a gzipped history backup snapshot, and updates the Bleve search index.

---

### 17. `list_agent_plans`
Lists all Collaborative AI Plans (OKF type `AI-Agent-Plan`) currently saved inside the knowledge base.

* **Arguments**:
  * `project_context` (string, **optional**): An optional project context name to filter plans by.
  * `status` (string, **optional**): Filter plans by lifecycle status, e.g. `implementing` or `completed`. Call `get_status_tags` for the vocabulary.
  * `tag` (string, **optional**): Filter plans by a project-context or topic tag.
* **Behavior**:
  Scans all active articles, isolates pages of OKF type `AI-Agent-Plan`, filters them by project context tag and/or additional tags if provided, and returns a bulleted index of matching plans.
* **Structured output**: `structuredContent` as `{count, documents[]}`, the shared listing shape. Lifecycle state lives in each document's status tags.

---

### 18. `create_agent_skill`
Creates a new Custom AI Skill, automatically making it part of the custom Skills Registry. Sets the OKF `type` to `AI-Agent-Skill` — the reserved type is immutable and must **NEVER** be relabelled.

* **Arguments**:
  * `title` (string, **required**): The title of the skill (e.g., "Docker Container Pruning").
  * `content` (string, **required**): The raw Markdown content of the skill instructions (procedural SKILL.md format).
  * `description` (string, **optional**): One-line summary of what the skill does, shown in list indexes and the context overview.
  * `source` (string, **optional**): Provenance — where this skill's procedure came from.
  * `status` (string, **optional**): Lifecycle status — `draft`, `ready`, or `archived`. Omit for none. An unrecognized value is rejected.
  * `tags` (array of strings, **optional**): Topic and grouping tags. Lifecycle state does **not** go here; a status word passed as a tag is rejected.
  * `edit_summary` (string, **optional**): Optional summary describing why the skill was created.
* **Behavior**:
  Checks for slug collision, sets the OKF `type` to `AI-Agent-Skill`, applies any user-provided tags, saves the Markdown file, commits the first version snapshot, and indexes the skill in Bleve.

---

### 19. `edit_agent_skill`
Modifies an existing Custom AI Skill in place. The reserved `AI-Agent-Skill` type is strictly preserved and must **NEVER** be relabelled.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the skill to edit.
  * `loaded_version` (integer, **required**): The active version the client read, for optimistic locking.
  * `title` (string, **optional**): New title; omit to preserve.
  * `content` (string, **optional**): Replacement Markdown body in SKILL.md format; omit to preserve.
  * `description` (string, **optional**): One-line summary. Pointer semantics — omit to preserve, empty string to clear.
  * `source` (string, **optional**): Provenance. Pointer semantics, as above.
  * `status` (string, **optional**): New lifecycle status — typically promoting `draft` → `ready`. **Omit to preserve** the skill's current state.
  * `tags` (array of strings, **optional**): Replaces existing user tags — topics and grouping only.
  * `edit_summary` (string, **optional**): What changed.
* **Behavior**:
  Rejects a target whose type is not `AI-Agent-Skill`, enforces optimistic locking, merges the optional fields, and writes a new version snapshot.
* **Why it exists:** `create_agent_skill` and `list_agent_skills` shipped without an edit counterpart, so revising a skill meant reaching for `edit_wiki_article` — which works but applies none of the type guarding its memory and plan equivalents do. That mattered most for `nexwiki-agent-guidelines`: the governance document every agent loads is itself a skill, so the one document explicitly designed to be revised had no first-class edit path.

---

### 20. `list_agent_skills`
Lists all Custom AI Skills (OKF type `AI-Agent-Skill`) currently saved in the knowledge base.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans all active articles, isolates pages of OKF type `AI-Agent-Skill`, and returns a bulleted index of matching skills.
* **Structured output**: `structuredContent` as `{count, documents[]}`, the shared listing shape.

---

### 21. `get_status_tags`
Returns the recognized values for the `status` **field**, which only agent plans and agent skills have.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Lifecycle state is a document field, not a tag. An `AI-Agent-Plan` has **exactly one** plan status; an `AI-Agent-Skill` has **at most one** skill status. Neither may use a lifecycle word from anywhere else — a plan with `status: "wip"` or a skill with `status: "implementing"` is rejected with a message naming the right value, and a lifecycle word passed as a *tag* on either is rejected too. **Wiki articles and agent memories have no status field and no tag rules at all.** The output also explains the completion workflow (append final notes with `append_agent_plan`, then set `status: "completed"` with `edit_agent_plan`) and the automatic tail of the lifecycle. See the [Plan Lifecycle Guide](./plan_lifecycle_guide.md).

* **Plan statuses**: `draft`, `implementing`, `blocked`, `completed`, `superseded`, `parked`, `evergreen`, `archived`
* **Skill statuses**: `draft`, `ready`, `archived`
* **Structured output**: `structuredContent` as `{status_tags[], plan_status_tags[], skill_status_tags[]}` — `status_tags` remains the union of the two for backward compatibility.

---

### 22. `get_context_overview`
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

### 23. `get_backlinks`
Lists all articles whose content links to a given article, in **either** internal link form — double-bracket `[[WikiLinks]]` or absolute `[text](/articles/<slug>)` Markdown links — reverse traversal of the knowledge graph.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the target article to find inbound links for.
* **Behavior**:
  Scans all article bodies (including the `home` dashboard) on demand for internal links resolving to the target slug, skipping self-links. Returns an indexed plain-text list with titles, slugs, summaries, and updated timestamps, sorted the newest first. Useful before editing or deleting a page to see what references it. `read_article` also appends a compact `Linked from:` section automatically.
* **Structured output**: `structuredContent` as `{slug, count, backlinks[]}`.

---

### 24. `edit_agent_memory`
Replaces or corrects an existing protected AI Agent Memory **in place** — the core memory-hygiene tool. Prefer this over creating a near-duplicate memory when facts go stale.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the memory to edit.
  * `title` (string, **optional**): New title (preserves existing if omitted).
  * `content` (string, **optional**): Full replacement of the memory's Markdown content (preserves existing if omitted; cannot be blank — use `delete_agent_memory` to retire a memory entirely). Use `append_agent_memory` to add without replacing.
  * `description` (string, **optional**): New one-line summary (preserves existing if omitted).
  * `source` (string, **optional**): New provenance reference (preserves existing if omitted).
  * `memory_kind` (string, **optional**): New kind (preserves existing if omitted). This is how a memory written before the kind axis existed gets classified — `wiki_health` lists those as `unkinded_memories`.
  * `tags` (array of strings, **optional**): Tags to set (replaces existing user tags; tool-managed `memory-<scope>` tags are always preserved).
  * `loaded_version` (integer, **required**): The current version number loaded by the agent, for optimistic locking.
  * `edit_summary` (string, **optional**): Summary of what was corrected.
* **Behavior**:
  Verifies the target is of OKF type `AI-Agent-Memory`, checks `loaded_version` against the disk version (conflict errors instruct the agent to re-read the memory), merges the provided fields over existing values, preserves tool-managed `memory-<scope>` tags, increments the version, snapshots history, and re-indexes.
  > **Omitting `memory_kind` preserves it**, exactly as omitting `status` preserves a plan's lifecycle state. Editing a memory's body can never silently declassify it — which would be invisible at the call site and would only surface later as a memory that kind-filtered recall can no longer find.

---

### 25. `delete_agent_memory`
Permanently deletes an obsolete or fully superseded protected AI Agent Memory.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the memory to delete.
* **Behavior**:
  Verifies the target is actually a protected memory (refuses standard articles — use `delete_wiki_article` for those), then removes the Markdown file, history backups, and search index entry. Prefer `edit_agent_memory` to correct a memory rather than deleting and recreating it.
* **Structured output**: `structuredContent` as `{count, events[]}`, each event carrying `timestamp`, `source`, `action`, `tool`, `slug`, `title`, and `agent`.

---

### 26. `get_recent_activity`
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

### 27. `export_okf_bundle`
Exports the entire knowledge base as a conformant **Open Knowledge Format (OKF v0.1) bundle** (a `.zip`).

* **Arguments**: none.
* **Behavior**:
  Native files are already OKF YAML, so export synthesizes the **bundle hierarchy** from each document's `type` (`wiki/`, `aimemories/`, `aiplans/`, `aiskills/`), the reserved per-directory and root `index.md` files (the root carries `okf_version: "0.1"`), a date-grouped `log.md` built from the durable activity log, and translates `[[WikiLinks]]` into bundle-relative concept paths (`/wiki/<slug>.md`, OKF §5.1). The archive is written into the data directory and its path is returned. REST equivalent: `GET /api/okf/export` (streams the `.zip` as a download).

### 28. `import_okf_bundle`
Imports an **OKF v0.1 bundle** (`.zip`) from a filesystem path into the knowledge base.

* **Arguments**:
  * `path` (string, **required**): Filesystem path to the `.zip` bundle.
* **Behavior**:
  Walks the bundle, parses each non-reserved `.md` as an OKF concept document, maps its `type` (reserved value → agent class; otherwise `Wiki`), translates bundle-relative Markdown links back to `[[WikiLinks]]`, and creates/updates each article via the storage layer (dedup by slug; reserved `index.md`/`log.md` are consumed). The importer is **permissive** (OKF §9): a document with a missing/unknown type defaults to `Wiki` and is flagged in the returned conformance report rather than rejected. REST equivalent: `POST /api/okf/import` (multipart `file` upload).

---

### 29. `wiki_health`
Audits the knowledge base for maintenance work in one call. Everything it reports is something the wiki already knows but never volunteers.

* **Arguments**:
  * `stale_days` (integer, *optional*): How many days an in-flight plan may go untouched before counting as stale. Default `30`.
  * `cold_days` (integer, *optional*): How many days a memory may go unread and unedited before counting as cold. Default `90`.
  * `limit` (integer, *optional*): Maximum items reported **per category**. Default `50`, maximum `500`. Counts are always complete even when the lists are capped.
* **Behavior**:
  Runs seven checks over a single cached pass of the article directory — the same `LinkGraph` scan `get_wiki_statistics` uses, so the two tools can never disagree about the same wiki:

  | Check | Finds | Why it matters |
  |---|---|---|
  | **Orphan pages** | A **wiki article** no other article links to | Unreachable by graph traversal, so an agent following links will never find it |
  | **Broken internal links** | The target does not exist | Names the `target_slug` a fix has to create, and the `form` the link was written in |
  | **Memories with no `source`** | An `AI-Agent-Memory` with empty provenance | A fact that cannot be re-verified later |
  | **Stale plans** | An `AI-Agent-Plan` untouched for `stale_days`, never marked finished, and not parked | Work that quietly stopped |
  | **Cold memories** | An `AI-Agent-Memory` neither read nor edited within `cold_days` | Knowledge nothing consults is either settled or quietly wrong |
  | **Duplicate memories** | Two memories in the same `memory-<scope>` with closely matching titles | Two answers to one question drift apart |
  | **Unkinded memories** | An `AI-Agent-Memory` with no `memory_kind` — written before the axis existed | Kind-filtered recall cannot find it. This is the backfill worklist; classify with `edit_agent_memory` |
  | **Unreferenced skills** | An `AI-Agent-Skill` no live document links *or* names in a `read_article` call | A skill nothing points an agent at will never be loaded |

  Several rules keep the report actionable rather than noisy:

  * **Archived documents are skipped entirely.** Archiving is you saying "this is done"; reporting it as needing attention inverts that.
  * **Both internal link forms are counted.** The graph reads `[[WikiLinks]]` *and* absolute `[text](/articles/<slug>)` Markdown links, so orphan detection, broken-link detection, and `get_backlinks` all see the same wiki. Counting only the double-bracket form reported 0 broken links against 26 real ones on an 84-document corpus, and called 44 of those documents orphans — most of them linked from the home page in Markdown syntax.
  * **Skills are checked for *references*, not links — they are different things.** An article is linked; a skill is *invoked*, by a `read_article(slug: "…")` call written into another document's prose or a backticked slug reference. Neither is a link, so the link graph never sees them. Measured on the real corpus: `nexwiki-agent-core-guidelines` named `enhanced-memory-decision-making-skill` four times in exactly those forms and `get_backlinks` still returned **0**, while `create-plan-skill` — live and wanted — also reports 0 inbound links. A check built on the link graph alone would flag the healthy skill and stay silent on the dead one, so this check counts links **and** in-code slug mentions. Those mentions are tracked separately from `InboundCount`, so `get_backlinks` and orphan detection keep their existing meaning.
  * **A reference from an archived document does not count.** Archiving is a retirement, and a skill whose only mention lives in a retired document is as unreachable as one with no mention at all. This is what makes the check useful rather than decorative: `enhanced-memory-decision-making-skill` looked referenced right up until the document naming it was archived.
  * **`nexwiki-agent-guidelines` is never unreferenced.** Three MCP tool descriptions name its slug in Go, so no document has to.
  * **Orphan detection covers wiki articles only.** Memories, plans, and skills are reached through their own list tools, the search facets, and `get_context_overview` — nobody links to a memory, so flagging every one of them is noise. On an 83-document corpus, scanning every type produced 70 findings, 27 of which were agent documents behaving exactly as designed.
  * **`home` is never an orphan.** Nothing links to a front page.
  * **A plan tagged `completed`, `done`, or `superseded` is never stale**, however old, even if it still also carries `wip`. The terminal tag wins.
  * **A plan tagged `parked`, `deferred`, `tabled`, `on-hold`, or `someday` is not stale either.** Parked is not finished — the work may still happen — but it *is* a decision, and re-reporting a decision teaches you to skip the report. Parked plans are reported as a count, so the number is not mistaken for plans that fell off the list by accident.
  * **The cold-memory check refuses to run when it cannot be trusted.** Recency comes from the activity log, so on a fresh install — or after `NEXWIKI_ACTIVITY_MAX_ARCHIVES` pruning — the log may be younger than `cold_days`, and then *every* memory looks untouched. Rather than report all of them, the check is skipped and `cold_memory_scan_ran` is `false` with `cold_memory_skipped_reason` saying why.
  * **Reads keep a memory warm.** A memory the agent keeps consulting is alive even if nobody has edited it in a year — that is what a good memory looks like.
  * **Duplicate detection is scoped, and skips pairs that already link to each other.** A "Deployment Notes" memory about `docker` and one about `nexwiki` are separate by design. And when two memories reference one another, their author already knows both exist and has decided to keep them apart. It reports similarity, not disagreement: telling the two apart needs semantics NexWiki deliberately does not have.

  A stale plan does **not** need an in-flight tag. Requiring `wip` sounds tidier but makes the check incapable of firing on a real wiki, where plans typically carry a project tag and nothing else — what matters is that the plan was never marked finished and nobody has touched it since. When an in-flight tag (`wip`, `in-progress`, `draft`, `active`, `todo`, `pending`, `review`, `blocked`) *is* present, the report names it.
* **Structured output**: `structuredContent` as `{total_documents, stale_days, limit, truncated, orphan_count, orphans[], broken_link_count, broken_links[], unsourced_memory_count, unsourced_memories[], unkinded_memory_count, unkinded_memories[], stale_plan_count, stale_plans[], unreferenced_skill_count, unreferenced_skills[], cold_days, cold_memory_scan_ran, cold_memory_skipped_reason, cold_memory_count, cold_memories[], duplicate_memory_count, duplicate_memories[], parked_plan_count}`. Counts are complete; the lists honour `limit`, and `truncated` says whether anything was cut. Each entry in `broken_links[]` carries `from_slug`, `target`, `target_slug`, and `form` (`"wikilink"` or `"markdown"`).

**Examples**

```jsonc
// The default audit
{}

// Only flag plans that have been idle for a quarter, and keep the report short
{ "stale_days": 90, "limit": 10 }
```

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
