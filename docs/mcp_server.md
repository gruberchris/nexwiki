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
| Result caching | `ttlMs` + `cacheScope` on cacheable results | not available |
| Protocol errors | real HTTP status (`400`/`404`) | `200` with an error body |
| Change notifications | `subscriptions/listen` stream | standalone `GET` SSE stream |
| `resources.subscribe` capability | ✅ advertised | ❌ not advertised — see below |
| `ping` | ❌ removed by the revision | ✅ answered |
| Pagination | ✅ `cursor` / `nextCursor` | ✅ `cursor` / `nextCursor` |
| Completion | ✅ `completion/complete` | ✅ `completion/complete` |

**How NexWiki decides:** a request is modern if its `params._meta` carries `io.modelcontextprotocol/protocolVersion`, **or** — over HTTP, where the field is mirrored and required — if the `MCP-Protocol-Version` header names a revision NexWiki implements as modern. Anything else takes the legacy path.

That second signal matters. A modern client whose body is missing the required `_meta` used to fall through to the legacy switch and get a plausible-looking legacy answer with no hint that anything was wrong. It is now recognised as modern and rejected as malformed (`-32602`), which is what the specification requires.

Both eras share the same 7 tools and the same 2 prompts — only the envelope differs.

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
| `Mcp-Name` | `tools/call`, `prompts/get`, `resources/read` | `params.name` (or `params.uri` for `resources/read`) |

`Mcp-Name` values may use the spec's Base64 sentinel form (`=?base64?...?=`); NexWiki decodes before comparing.

#### Error codes

| Code | Name | HTTP | Raised when |
|---|---|---|---|
| `-32020` | `HeaderMismatch` | `400` | a mirrored header disagrees with the body, or is missing |
| `-32022` | `UnsupportedProtocolVersion` | `400` | the requested revision is not implemented; `data.supported` lists what is |
| `-32602` | `InvalidParams` | `400` | a required `_meta` field is missing, a prompt name or argument is wrong, a resource does not exist, or a pagination cursor is not one this server issued |
| `-32601` | `MethodNotFound` | `404` | unknown method (the `404` is how a dual-era client tells a modern server from a legacy one) |

> **`-32602`, not `-32601`, for an unknown prompt or resource.** The method exists and was found; it is the *name* that is wrong. The distinction is not academic here: `-32601` is required to surface as HTTP `404`, so returning it for a typo'd prompt name made the MCP endpoint itself look like it had disappeared.

#### `server/discover`

Modern servers must implement it. One request returns supported versions, capabilities, and identity — no handshake needed:

```bash
curl -X POST http://localhost:5808/api/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: server/discover" \
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{
        "io.modelcontextprotocol/protocolVersion":"2026-07-28",
        "io.modelcontextprotocol/clientCapabilities":{}}}}'
```

**Capabilities are declared per era, because the same word promises a different method in each.**

| Capability | Modern (`server/discover`) | Legacy (`initialize`) |
|---|---|---|
| `tools` | `{}` | `{}` |
| `prompts` | `{}` | `{}` |
| `completions` | `{}` | `{}` |
| `resources.listChanged` | ✅ | ✅ |
| `resources.subscribe` | ✅ | ❌ |

`resources.subscribe` is the reason for the split. In the modern revision it means the server honours `resourceSubscriptions` on a `subscriptions/listen` stream, which NexWiki does. In the initialize-based revisions it means the server implements the `resources/subscribe` **RPC** — a method the 2026-07-28 revision replaced and that NexWiki deliberately does not implement. Advertising one capability set to both eras therefore pointed legacy clients at a method that answers `-32601`. A capability is a promise, so each era is told only what is true for it.

`tools` and `prompts` stay bare in both: the registry is compiled into the binary and cannot change while the process runs, so claiming `listChanged` would promise a notification that can never arrive.

> The `Mcp-Session-Id` header was removed by the 2026-07-28 revision; NexWiki ignores it and never mints one. The standalone `GET` SSE stream is kept for legacy-era clients, and is where they receive `notifications/resources/list_changed`.

#### Result caching (modern era only)

The `2026-07-28` revision lets a server tell clients how long a result stays fresh, so an agent stops re-fetching a tool list that cannot change. NexWiki attaches two fields to every cacheable `resultType: "complete"` result:

* **`ttlMs`** — how many milliseconds the client may treat the result as fresh. Analogous to HTTP `Cache-Control: max-age`.
* **`cacheScope`** — `"public"` when the result holds no user data and a shared proxy may serve one copy to anyone, `"private"` when it must never cross an authorization boundary.

| Method | `ttlMs` | `cacheScope` | Why |
|---|---|---|---|
| `server/discover` | `3600000` (1h) | `public` | identity and capabilities are compiled in |
| `tools/list` | `3600000` (1h) | `public` | the 7 tools are compiled in and identical for every caller |
| `prompts/list` | `3600000` (1h) | `public` | the 2 prompts are compiled in |
| `resources/templates/list` | `3600000` (1h) | `public` | a single static URI template |
| `resources/list` | `30000` (30s) | `private` | your article slugs and titles |
| `resources/read` | `30000` (30s) | `private` | your article content |

`tools/call` and `prompts/get` carry **no** caching hints — the spec does not list them as cacheable, and a tool call is not a repeatable read.

Caching and notifications are complementary. NexWiki advertises `listChanged` and `subscribe`, so a client holding a [`subscriptions/listen`](#-resources---mention-a-wiki-page) stream is told the moment an article changes, which invalidates a cached entry long before its 30-second TTL runs out. A client that does not subscribe still stays correct — it just re-fetches on the TTL instead.

> ⚠️ **These fields are mandatory, not advisory.** A conformant client validates a list result against a schema in which `ttlMs` and `cacheScope` are **required**, so omitting them rejects the entire response. The failure looks confusing from the outside: the client connects, reports the server healthy, and then lists zero tools. Legacy-era results correctly carry neither field.

#### Pagination

The four list operations the specification paginates — `tools/list`, `prompts/list`, `resources/list`, and `resources/templates/list` — accept a `cursor` and return a `nextCursor` when more results remain. Both eras support it.

```bash
# first page
curl -s -X POST http://localhost:5808/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}' | jq '.result.nextCursor'

# follow it
curl -s -X POST http://localhost:5808/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{"cursor":"bmV4d2lraToxMDA"}}'
```

Only `resources/list` realistically pages: the tool, prompt, and template sets are compiled in and fit one page, while a knowledge base is unbounded. That last point is why this exists — `resources/list` used to project **every** document into a single response, and on stdio that response has to fit on one 8 MB line.

* The cursor is **opaque**. Do not parse it, and treat any non-null value — including an empty string — as "there is more".
* A missing `nextCursor` means the end of the list. NexWiki omits the field rather than sending an empty one, which would loop a conformant client forever.
* A cursor NexWiki did not issue, or one left over from a list that has since shrunk, is rejected with `-32602`. Re-request without a cursor to start over.
* Each page is cached independently and carries its own `ttlMs`, but every page of one list shares the same `cacheScope`.

#### Argument completion

`completion/complete` suggests values as a user fills in an argument, the way an IDE completes code. NexWiki advertises the `completions` capability in both eras.

**This is what makes `@`-mentioning a page practical.** The `nexwiki://article/{slug}` template lets a client build a URI for a slug it already knows — completion is how it *discovers* one without paging the whole resource list.

```bash
curl -s -X POST http://localhost:5808/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"completion/complete","params":{
        "ref":{"type":"ref/resource","uri":"nexwiki://article/{slug}"},
        "argument":{"name":"slug","value":"blev"}}}' | jq .result.completion
# → { "values": ["bleve-decision", ...], "total": 2, "hasMore": false }
```

| Reference | Argument | Completes from |
|---|---|---|
| `ref/resource` (`nexwiki://article/{slug}`) | `slug` | every document's slug |
| `ref/prompt` (`article_creation_workflow`) | `title` | existing article titles |
| `ref/prompt` (`project_planning_workflow`) | `project` | project contexts already used by plans |

Prefix matches rank above substring matches, and each band is sorted alphabetically so the list is stable and cacheable. Responses are capped at 100 values, with `total` and `hasMore` reporting what was cut. An argument with nothing to suggest — a free-text `description`, say — returns an **empty list, not an error**: a user typing into a free-form field should never see a failure. A prompt name that does not exist *is* an error (`-32602`).

> **Why complete `project` from existing plans:** so an agent reuses the project context that is already there instead of coining a near-duplicate and splitting one project's plan history across two names.

### 🏷️ Tool Annotations — fewer approval prompts

Every tool carries MCP `annotations` telling your client what calling it actually does. Clients use these to **auto-approve safe reads and confirm destructive writes**, so an agent isn't interrupting you to run `get_wiki_overview` — the tool the agent skill says to call first in every session.

This matters because the spec's defaults are **pessimistic**: an unannotated tool is assumed `destructiveHint: true` and `openWorldHint: true`. Shipping no annotations tells every client that all 7 tools might destroy data and reach arbitrary external systems.

| Hint | NexWiki's values |
|---|---|
| `readOnlyHint` | `true` on **4** tools that never modify the wiki |
| `destructiveHint` | `false` for additive appends; `true` for document saves and deletes |
| `idempotentHint` | `true` on `delete_article` — deleting an already-deleted document changes nothing further |
| `openWorldHint` | **`false` on every tool, without exception.** The entire surface operates on the local wiki directory and never reaches an external system |
| `title` | A human-readable display name, e.g. `Get Wiki Overview` |

**Read-only (4):** `search_wiki` · `read_article` · `get_wiki_overview` · `wiki_health`

**Additive write (1)** — appends new text, never overwrites: `append_article`.

**Destructive writes (2)** — can overwrite or remove existing content: `save_article` (can revise existing documents when given an existing `slug`) and `delete_article`.

> **Annotations are hints, not guarantees.** The specification is explicit that clients must treat them as untrusted from untrusted servers. They describe intent; the actual guards are the optimistic-locking checks, `delete_article`'s inbound-link refusal, and the reserved-type rules enforced inside the handlers.
>
> Note that every successful call — reads included — appends to the durable activity log. That is server-side audit bookkeeping, not a change to the content the tool operates on, so it does not disqualify `readOnlyHint`.

### 📤 Structured output — parse data, don't scrape prose

All four read tools declare an **`outputSchema`** and return a **`structuredContent`** object alongside their text. An agent that needs an article's version number to pass as `loaded_version` reads an integer instead of pulling one out of a sentence.

| | |
|---|---|
| Tools with `outputSchema` | `search_wiki` · `read_article` · `get_wiki_overview` · `wiki_health` |
| Prose only | `save_article` · `append_article` · `delete_article` |

Three properties hold across all of them:

- **The text is still there.** `structuredContent` is emitted *in addition to* `content`, and both are rendered from the same value, so they cannot disagree. A client that predates structured output sees no change. `read_article` provides the full Markdown body in both `content[0].text` and `structuredContent.article.content`, ensuring universal compatibility across text-only MCP clients (Claude Desktop, Cursor, Antigravity, and custom agent harnesses) and structured-output clients (Claude Code).
- **Error results carry no `structuredContent`.** A payload that fails its own published schema is worse than no payload: every consumer would have to handle a shape the schema says cannot occur.
- **Field names match the REST API.** A document read over MCP and the same document read from `GET /api/articles` have identical keys, so an agent that has seen one already knows the other.

`search_wiki` answers both a search and a listing with one shape — `{ "count": N, …, "next_cursor"? }`, carrying `results[]` for a query and `documents[]` for the index, both paged with `cursor`/`next_cursor` — so a NexWiki listing is learned once. Listings carry metadata only; the body is what `read_article` is for.

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
> The structured payload also echoes the applied facets (`type`, `tags`, `status`, `memory_kind`, `include_archived`), so an agent can tell "no such knowledge" from "my filter excluded it" without re-reading the prose.

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
curl -X POST http://localhost:5808/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}'

# Read one
curl -X POST http://localhost:5808/api/mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"nexwiki://article/home"}}'
```

`resources/templates/list` advertises `nexwiki://article/{slug}`, so a client that already knows a slug can build the URI without paging the whole list.

A missing resource returns `-32602` with the URI echoed in `data` — never an empty `contents` array, which the spec forbids because it cannot be distinguished from a resource that exists but is empty.

---

## 📡 Subscriptions — a live, subscribable knowledge base

`subscriptions/listen` opens a long-lived stream. **An agent holding one learns the moment you edit a page in the browser, or another agent writes a memory** — no polling.

The events already existed: the same `EventBus` has been driving the browser's live activity drawer all along. This wires that signal to a second consumer.

```bash
curl -N -X POST http://localhost:5808/api/mcp -H "Content-Type: application/json" \
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

Every message carries `io.modelcontextprotocol/subscriptionId` in `_meta` so concurrent subscriptions can be demultiplexed.

**Overflow markers:** a bulk write (an OKF import, a global tag deletion) can outpace a subscription's buffer. Rather than silently drop notifications, the stream collapses the backlog and prompts a re-sync through the notification types you asked for: you receive `notifications/resources/updated` for each subscribed resource (and `notifications/resources/list_changed` if you requested it) with no document change behind them. Treat such a burst as the signal that per-document updates were missed — re-read the subscribed resources, and re-list if you watch the list. The durable state (the article store and the activity log via `get_wiki_overview`) is always complete; only the live stream fell behind.

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
-mcp-only: web server detected on port 5808; running as a proxy to it.
           The primary owns the data directory; this process forwards MCP traffic to it.
```

**What this buys you beyond "it starts":**

- **Writes land in the live wiki.** The call executes *inside* the primary, so the browser sees the change immediately and the activity log records it once, in the process that did the work.
- **A sidecar's client still gets live subscriptions.** The primary answers `subscriptions/listen` with an SSE stream, and the proxy relays each notification to stdout as its own JSON-RPC line. A *standalone* stdio server serves subscriptions from its own `EventBus`; a sidecar has none to serve from, because the primary owns the data directory — so relaying is how its client gets them.
- **No second index, no lock contention, no divergence.** There is one owner of the data directory, always.

The proxy synthesizes the modern era's mirrored headers (`MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`) from the body it forwards, since stdio carries no headers and the primary validates them. If the primary is unreachable, the proxy answers with a JSON-RPC error carrying the original request id, so the failure is attributable to the call that caused it.

> Streamable HTTP is still the simplest option when your client supports it — one process, no subprocess at all. Proxy mode exists so the stdio path is no longer a trap.

### 🔒 Log Safety Guarantee
To prevent stdio pipe corruption (which breaks JSON-RPC communication in tools like Claude Desktop), **NexWiki redirects all internal system and web application logs exclusively to standard error (`Stderr`)**. Only valid JSON-RPC envelopes are ever output to `Stdout`.

### 📏 Stdio message size

A single JSON-RPC line on stdio may be up to **8 MB**, in both the standalone stdio server and the sidecar proxy. This is far above bufio's 64 KB default because MCP payloads carry whole article bodies — a `save_article` call with a long document passes 64 KB easily.

Exceeding the cap is **not recoverable**: the read loop ends and the stdio channel stops answering for the life of the process. NexWiki emits a JSON-RPC parse error naming the limit before it goes quiet, so the failure is attributable rather than silent, but the session is over. The Streamable HTTP transport caps a request body at the same **8 MB**; the difference is what happens on the way over — an oversized HTTP request is refused with `413` naming the limit and the session (and every other request) continues, while an oversized stdio line ends the session.

---

## 🔐 Secret scanning — every write is checked, and a hit is refused

Every MCP write path scans the `content`, `description` and `source` it is about to store, and **refuses the write** if it finds credential-shaped text. This covers `save_article` and `append_article`.

**A key in a plan is no safer than a key in a memory**, so the check sits at one chokepoint rather than on the class that prompted it.

**Refuse, not redact.** A scanner on a live output stream redacts because the turn has to continue. A write can safely fail: the caller gets a recoverable error and rewrites. A redacted document is worse than a refused one — it reads as complete, and the hole is invisible to every later reader.

**The refusal never quotes the match.** It gives the pattern class, the byte offset and the length. A tool error is transcript content — sent to the model provider and persisted — so quoting the value to explain the refusal would reproduce the exposure the check exists to prevent.

```
Error: refusing to write this memory — it appears to contain a credential.

  - GitHub token in 'source' at byte offset 31 (length 40)

The matched text is deliberately not repeated here: a tool error is transcript content
that is sent onward and persisted, so quoting it would reproduce the exposure this check
exists to prevent.
…
```

**What to do about it**: remove the value and describe it instead ("the deploy token for X, in 1Password"), or use a placeholder — `<your-token>`, `REDACTED`, and AWS's published example key are recognized and allowed, so documentation *about* credentials stays writable.

| | |
|---|---|
| Detected | AWS, GitHub, Slack, Anthropic, OpenAI-style and Google key prefixes; PEM private-key headers; JWTs; assignment-shaped `key = value` |
| Not detected | Anything whose format is not on that list. There is no entropy scoring — a wiki is full of hashes and long identifiers that carry no secret |
| Scanned fields | `content`, `description`, `source`. `source` matters most: it is the natural home for a URL with an embedded token |
| Append | Only the appended text is scanned, not the whole document — this closes the intake without making a legacy document unappendable |
| Configuration | `NEXWIKI_SECRET_SCAN` = `refuse` (default), `warn`, `off`. An unrecognized value falls back to `refuse`. No per-document opt-out |
| Not scanned | REST and web-UI writes (a human is exercising judgment) |

See [SECURITY.md](../SECURITY.md#secret-scanning-on-agent-writes) for the full limits.

## 🔁 The repeat-lookup damper

`search_wiki` notices when the **same agent asks the same question twice** inside two minutes, and prepends an escalating note to the result. It **never blocks** — a false positive that refused a lookup would break real work, while one that adds a sentence costs nothing.

| Occurrence | Response |
|---|---|
| 1st | normal |
| 2nd | one line: this repeats a lookup you ran *N* seconds ago |
| 3rd+ | explicit: states the check is complete and points at `save_article` |

**Rewordings are the point.** The fingerprint lowercases, strips punctuation, drops stop words, applies a crude stem, sorts the tokens and hashes — so *"docker build error"* and *"error building docker"* are the same question. An agent asking an identical question twice is easy to catch and is **not** the failure mode this exists for: the 31-minute livelock on this wiki was rewordings.

**A successful write clears that agent's history**, because a write is progress and the loop being damped is read-only by nature.

Two deliberate limits:

- **The query text is never persisted.** A fingerprint lives in memory for 120 seconds and is dropped. Recording queries in the activity log was considered and rejected — that log is durable, append-only, rendered in the UI, and governed by `SECURITY.md`, while query strings are free text that may carry anything a user typed.
- **`structuredContent` is never touched.** It is a machine contract; a client parsing it should not have to handle a field that is sometimes an essay. The notice goes in the text block only.

State is per resolved agent, bounded (8 lookups each, 64 agents, least-recently-used eviction), and entirely in memory — nothing survives a restart, and losing it costs only a missed notice.

## 🛠️ Exposed MCP Tools

> **Native OKF storage & document `type`.** Every NexWiki `.md` file is a conformant Open Knowledge Format (OKF v0.2) concept document at rest (real YAML front matter, with dual-era OKF v0.1 backward compatibility). Each document carries a `type` — `Wiki`, `Attested Computation`, or one of the reserved **`AI-Agent-Memory`** / **`AI-Agent-Plan`** / **`AI-Agent-Skill`** classes. The type is set at creation and changes only when a write names a new one explicitly (see [changing a document's type](#changing-a-documents-type)). The legacy `aiagent-*` *class* tags are gone; the class is now the `type`. System tags remain: **status tags** (e.g. `wip`, `completed`, `inbox`) and tool-managed **memory-scope tags** (`memory-<scope>`).

> **Stdio alongside a web primary (`-mcp-only`).** A normal launch binds the web port; if it cannot bind, it halts rather than silently falling back. To run a stdio MCP server next to an always-running web primary — e.g., a Claude Desktop subprocess — start NexWiki with the **`-mcp-only`** flag (or `NEXWIKI_MCP_ONLY=true`); it skips the port bind entirely. If it detects a running NexWiki web server on its `-port`, it proxies all MCP traffic to it and never opens the data directory, so the primary executes every call and records its successful tool calls in the activity log (see [Sidecar proxy mode](#-sidecar-proxy-mode)). With no NexWiki web server, it opens the data directory itself, serves all tools from the in-process storage layer, and persists the activity log directly. The clean single-process recommendation remains Streamable HTTP (`claude mcp add --transport http ...`).

The NexWiki MCP server registers and exposes **seven** powerful tools for AI agents:

### 1. `search_wiki`
Both the search and the index of the **entire** knowledge base — wiki articles *and* your agent memories, plans, and skills. **With a `query`** it runs a high-speed, scored full-text search using the built-in **Bleve Search** engine. **Without one** it returns the document index: every matching document's metadata, most recently updated first. The filters and cursor paging mean the same thing in both modes.

* **Arguments**:
  * `query` (string, *optional*): The search keywords or query string. Supports wildcards, quotes for exact matches, and boolean terms. Omit it (or pass only whitespace) to list the index instead.
  * `type` (string, *optional*): Restrict to one document type: `articles`, `memories`, `plans`, `skills`, or a canonical OKF type (`Wiki`, `AI-Agent-Memory`, `AI-Agent-Plan`, `AI-Agent-Skill`, `Attested Computation`). Omit to cover every type. An unknown value is an error naming the valid ones in both modes — never an empty result.
  * `tag` (string, *optional*): A tag every result must carry (case-insensitive), e.g. `wip` or `memory-nexwiki`.
  * `status` (string, *optional*): Lifecycle status, matched case-insensitively (e.g. `draft`, `implementing`, `completed`, `ready`). `archived` matches every archived document, including wiki articles and memories archived by tag.
  * `memory_kind` (string, *optional*): Only memories of this kind: `project`, `reference`, `user`, or `feedback`. An unknown kind is an error.
  * `include_archived` (boolean, *optional*): Include archived documents. The default depends on the mode: **`true` without a query** (the index lists everything), **`false` with one** (a search leaves archived documents out). A `tag` or `status` of `archived` includes them even over an explicit `false`, since otherwise the filter would discard every document asked for.
  * `limit` (integer, *optional*): Page size. Without a query: default `50`, no ceiling (an index entry is metadata only). With a query: default `40`, maximum `200`.
  * `cursor` (string, *optional*): The `next_cursor` from the previous page. Keep the other arguments the same. A cursor NexWiki did not issue, or one past the end of a list that has since shrunk, is rejected with `-32602`; re-request without a cursor.
  * `include_history` (boolean, *optional*): **Requires a non-empty `query`** — without one the call is rejected with `-32602`, rather than answered with an index and an empty `history_matches` an audit would read as "the text is gone". Also scans **every stored revision** of every document — version history plus the live files — for the query as a case-insensitive literal substring over the whole file, front matter included, and returns the matching `(slug, version)` pairs. One pair of surrounding double quotes is stripped; no other syntax is interpreted. The `type`, `tag`, `status`, `memory_kind`, and archived filters **do not narrow** this scan, and the response says so. Use it to verify a redaction: an empty list means no revision anywhere contains the text. A query the Bleve parser rejects (say `/[/`) still runs the history scan; the index half is reported as failed.
* **Behavior**:
  * **Query mode** executes the query against the local Bleve index and converts scored matches into a readable text block, reporting each hit's document `Type` so you can tell a memory from an article. HTML `<mark>` highlights become Markdown bold (`**`) to save context. Paging runs over the **200 best-scoring hits**, filtered: pages are disjoint, together they are the whole result, and the first page is exactly what a call without a cursor returns. `count` is the number of matches across all pages (within those 200), not the length of the page.
  * **Index mode** scans the document metadata, applies the filters, sorts matches by most recently updated first, and pages by cursor. Each entry shows its title, slug, type, status, last-edited time, tags, and description. `count` is every matching document across all pages.
  * In both modes, applied facets are echoed in the response header line, so an empty result set is distinguishable from an over-narrow filter, and a `Next page cursor:` line closes any page that has more after it.
* **Annotations**: Title: `Search Wiki`, `readOnlyHint`: `true`, `openWorldHint`: `false`.
* **Structured output**: `structuredContent` as `{query?, count, type?, tags?, status?, memory_kind?, include_archived, results[]?, documents[]?, next_cursor?, include_history?, history_matches?, history_match_count?}`. Query mode carries `query` and `results[]` — each result carries `title`, `slug`, `type`, `score`, `timestamp`, `tags`, and plain-text `snippets`. Index mode carries `documents[]` instead: document metadata with the same keys as `GET /api/articles`, no body. `include_archived` is the value actually applied, so the mode's default is visible. `next_cursor` is absent on the last page. With `include_history`, `history_matches` is always present (empty when nothing matched) and lists `{slug, title, version, current}` — `title` is the document's current title and `current` is true for the live revision — sorted by slug then version and capped at 500; `history_match_count` is the uncapped total.

> **Search spans everything by default — agents and browser alike.** Search spans every document type unless you narrow it with `type`, in both the MCP tool and the browser search view (`GET /api/search`).

**Examples**

```jsonc
// Everything about a topic, across articles and your own memories
{ "query": "elasticsearch" }

// Only what you remember about this project
{ "query": "retrieval", "type": "memories", "tag": "memory-nexwiki" }

// In-flight plans about a topic, newest handful
{ "query": "migration", "type": "plans", "status": "implementing", "limit": 5 }

// The index: every plan being implemented, 50 per page, newest first
{ "type": "plans", "status": "implementing" }

// ...and the next page, with the same filters
{ "type": "plans", "status": "implementing", "cursor": "<next_cursor from the previous page>" }

// Every memory of kind feedback, archived ones left out
{ "type": "memories", "memory_kind": "feedback", "include_archived": false }

// Does any revision of any document still mention this name?
{ "query": "Acme Corp", "include_history": true }
```

---

### 2. `read_article`
Retrieves the raw Markdown content, front-matter configurations, and inbound backlinks of a document by its URL slug. The backlinks are **every** document linking to this one, which makes a read the way to see what references a page before editing or deleting it. Optionally specify `version` to load a historical revision.

* **Arguments**:
  * `slug` (string, **required**): The clean URL-safe slug of the target document (e.g. `home` or `setup-guide`).
  * `version` (integer, *optional*): Optional historical revision number to load. Omit to load the latest version.
* **Behavior**:
  Reads the Markdown file on disk and parses the front-matter metadata conforming to OKF v0.2. The text block starts with a metadata header — Type, Title, Slug, **Version**, Created, Updated, plus Description, Resource, Source, Sources (`[id] Title (resource)`), Tags, **Trust Tier** (`🟢 Human-Reviewed`, `🔵 Machine-Confirmed`, `⚪ Unverified`), and Attested Computation details (`Runtime`, `Parameters`) when present. If the concept has exceeded its freshness date (`stale_after`), a prominent warning is included: `⚠️ STALE CONCEPT: this document passed its freshness expiration on YYYY-MM-DD`. This is followed by the complete Markdown body content. If other documents link to this page — via `[[WikiLinks]]` or absolute `[text](/articles/<slug>)` Markdown links — a `Linked from:` section is appended listing **every** one as `Title (slug)`, uncapped, so agents can traverse the knowledge graph in reverse; a text-only client sees the same complete list as the structured `backlinks`. Self-links are not listed. When the backlink scan skipped unreadable entries or misplaced documents that link to this page, a `Note:` line says the `Linked from:` list may be incomplete and points to `wiki_health` for the detail.
* **Annotations**: Title: `Read Article`, `readOnlyHint`: `true`, `openWorldHint`: `false`.
* **Structured output**: `structuredContent` as `{article, backlinks[], skipped_document_count?, skipped_documents[]?}`. `backlinks` lists every linking document as `{title, slug}` and is always an array — empty when nothing links here. `skipped_document_count` and `skipped_documents` (the first 20 skipped entries, sorted by path) are present only when the backlink scan skipped something. The raw Markdown body is in `article.content`. The article also includes `version` (to pass to `save_article` or `append_article` as `loaded_version`), plus OKF v0.2 metadata: `sources`, `trust_tier`, `generated`, `verified`, `stale_after`, `is_stale`, and computation fields when applicable. Both `content[0].text` and `structuredContent.article.content` carry the body, guaranteeing that both text-only clients and structured-only clients can read and edit documents seamlessly.

---

### 3. `save_article`
Create or update any document (wiki article, agent memory, plan, or skill). If `slug` is provided and matches an existing document, it updates/revises it; if omitted or not existing, it creates a new document. Supports optimistic concurrency locking via `loaded_version`. On an update, `type` changes the document's type, and `purge_history` redacts its earlier revisions.

The schema states three rules every caller needs, each exactly once — the first on the `content` argument, the other two in the tool description:

* **`content` is always a full replacement of the body.** To change only metadata or status, pass the current body back unchanged.
* **A `[[WikiLink]]` must name a document that exists in this wiki** — never an agent's own local memory files, instruction files, tools, or scratch paths. External references are plain URLs.
* **Creating a memory requires `memory_kind`, `description`, and `source`** — see [the memory write gate](#the-memory-write-gate).

* **Arguments**:
  * `title` (string, **required**): Human-readable title of the document. Never use a bare tool verb.
  * `content` (string, **required**): The complete Markdown body. Replaces the existing body in full; it is never merged or appended — to change only metadata or status, pass the current body back unchanged.
  * `slug` (string, *optional*): Optional slug. If provided and matches an existing document, updates/revises it; if omitted or not existing, creates a new document.
  * `type` (string, *optional*): Document type: `Wiki` (default on create), `AI-Agent-Memory`, `AI-Agent-Plan`, `AI-Agent-Skill`, or `Attested Computation` (the aliases `articles`, `memories`, `plans`, `skills` are accepted too). On an update, omit it to keep the current type, or pass one to change it — see [changing a document's type](#changing-a-documents-type). An unknown value is an error naming the valid ones, never a silent `Wiki`.
  * `description` (string, **required when creating a memory**, otherwise optional): One-line summary, shown in list indexes and overview. Omit on an update to keep the current one.
  * `source` (string, **required when creating a memory**, otherwise optional): Provenance: URL, document, ticket, or session context. Omit on an update to keep the current one.
  * `status` (string, *optional*): Optional lifecycle status for plans (`draft`, `implementing`, `blocked`, `completed`, `superseded`, `parked`, `evergreen`, `archived`) or skills (`draft`, `ready`, `archived`).
  * `tags` (array of string, *optional*): Optional tags for topics and context. Lifecycle status belongs in `status`, not tags.
  * `memory_kind` (string, **required when creating a memory**): Kind of an `AI-Agent-Memory`: `project` (goals and constraints not derivable from the repo), `reference` (a pointer to an external resource), `user` (who the operator is), or `feedback` (a correction the operator gave). Omit on an update to keep the current one.
  * `memory_type` (string, *optional*): Optional scope for `AI-Agent-Memory` (e.g. `nexwiki` or `docker`). Generates `memory-<memory_type>` tag.
  * `project_context` (string, *optional*): Optional project context for `AI-Agent-Plan`. Generates custom project tag.
  * `loaded_version` (integer, *optional*): Optional active version number from `read_article` to enforce optimistic concurrency locking on edits.
  * `edit_summary` (string, *optional*): Optional revision log description summarizing the edit.
  * `purge_history` (boolean, *optional*): Update only, and requires `loaded_version`. After the save succeeds, permanently delete every earlier revision of the document from version history — body and front matter — keeping only the revision this call wrote, and retitle the document's earlier activity-log entries to its current title and slug. It is **not** a deletion of anything a reader sees today: the document, slug, type, status, backlinks, and version counter are unchanged. See the [History Redaction & Audit Guide](./history_redaction_guide.md).
* **Behavior**:
  Automatically handles slug generation from title (or uses provided slug), validates document type and title against bare tool verbs, and verifies secret scanning on content, description, and source. When revising an existing document, validates `loaded_version` against disk for optimistic concurrency control, increments version, creates a gzipped history backup snapshot, updates flat-file storage, and refreshes Bleve search indexing.
  When saving an `AI-Agent-Memory`, validates `memory_kind` and applies the scoped `memory-<memory_type>` tag. A new memory must pass [the memory write gate](#the-memory-write-gate). (Near-duplicate memories are reported by `wiki_health`, not at write time.)
  The success response names the document's type — `Type: AI-Agent-Plan`, or `Type: Wiki → AI-Agent-Plan` when this save changed it. With `purge_history`, it also reports `revisions_removed: [..]`, `activity_entries_retitled: N`, and which revision survives.
* **Annotations**: Title: `Save Article`, `readOnlyHint`: `false`, `destructiveHint`: `true`, `idempotentHint`: `false`, `openWorldHint`: `false`.
* **Structured output**: None (prose response confirming success or reporting validation/concurrency conflict errors).

#### Changing a document's type

A document created with the wrong type — a plan saved as a `Wiki`, say — is invisible to `search_wiki(type: "plans")` while reading perfectly well by slug. Fix it in place with `save_article` (its `slug`, `loaded_version`, and the right `type`); never delete and recreate it, which destroys its history and backlinks. The same rules apply to `PUT /api/articles/{slug}` with a `"type"` field:

* **Omitting `type` keeps the current one.** An ordinary edit can never reclassify a document; an explicit `type` is the instruction to relabel it.
* The type is resolved **first**, and `status`, `memory_kind`, `memory_type`, and `project_context` are validated and applied against the type the document is becoming.
* Into `AI-Agent-Plan` with no `status`: a status already valid for a plan is kept, otherwise it enters at `draft`.
* Into `Wiki` or `AI-Agent-Memory`: the lifecycle status is dropped unless `status` is passed.
* A plan or skill status the new type does not accept (`implementing` onto a skill, `ready` onto a plan) with no `status` passed is **refused** — a lifecycle state is never silently converted. Pass the status you want.
* Leaving `AI-Agent-Memory` drops the `memory-<scope>` tags and the `memory_kind`.
* Into `AI-Agent-Memory`: the change must pass [the memory write gate](#the-memory-write-gate). The document's existing `description` and `source` count when the call omits them; `memory_kind` must be passed.
* A same-type re-save is an ordinary edit. Verify a change through `search_wiki(type: …)`, not a read.

```jsonc
{ "slug": "kimmydb-cold-start", "title": "KimmyDB Cold Start", "content": "...", "loaded_version": 3,
  "type": "AI-Agent-Plan", "project_context": "kimmydb", "status": "implementing" }
// → Type: Wiki → AI-Agent-Plan
```

#### The memory write gate

A memory comes into existence with the metadata that makes it useful, or not at all. `save_article` refuses to create an `AI-Agent-Memory` — or to change a document's type into one — unless:

* `memory_kind` is one of `project`, `reference`, `user`, `feedback`;
* `description` is non-blank — it is what the `search_wiki` index and `get_wiki_overview` show, so a memory without one is invisible when an agent orients;
* `source` is non-blank — a fact with no recorded origin cannot be re-verified, and its origin cannot be recovered later.

Whitespace-only values count as blank. The refusal names every missing field and says what belongs in it. `POST /api/articles` with a memory type, and a `PUT /api/articles/{slug}` that changes the type into a memory, apply the same rule and answer `400`.

An **ordinary edit of an existing memory is not gated**: omitted fields are preserved, and a memory written before the gate existed stays editable. `wiki_health` reports that backlog as `unsourced_memories` and `unkinded_memories`.

> **A conflict names the value to retry with.** Every optimistic-locking failure reports the version on disk *and* the exact `loaded_version` to send next:
> ```
> Error: version conflict on 'home'. The document is at version 15 on disk; you sent loaded_version 14. Retry once with loaded_version: 15. Re-read only if you need the current content before overwriting it — sending 14 again will fail identically.
> ```

---

### 4. `append_article`
Append text or logs to the end of an existing document without modifying its metadata, status, or tags. Supports optimistic locking via `loaded_version`.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the document to append to.
  * `content` (string, **required**): The Markdown text to append to the end of the document.
  * `loaded_version` (integer, *optional*): Optional active version number to detect concurrent edit collisions.
  * `edit_summary` (string, *optional*): Optional summary outlining what details were appended.
* **Behavior**:
  Locates the target document by slug, checks optimistic locking against `loaded_version` if provided, appends the new text cleanly with double newlines (`\n\n`), scans appended content for secrets, saves the updated document, commits a gzipped history backup snapshot, and updates the search index.
* **Annotations**: Title: `Append Article`, `readOnlyHint`: `false`, `destructiveHint`: `false`, `idempotentHint`: `false`, `openWorldHint`: `false`.
* **Structured output**: None (prose response confirming append and new version number).

---

### 5. `delete_article`
Permanently delete any document (wiki article, agent memory, plan, or skill) by slug. **Irreversible**: it destroys the document's version history and assets. It **refuses while other documents link to the target** unless `break_links: true` is passed, so a delete never breaks links silently. It is a last resort — to change a document's type, pass `type` to `save_article`; to remove text from history, use `save_article` with `purge_history: true`.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the document to delete.
  * `break_links` (boolean, *optional*, default `false`): Delete even though other documents link to the target, or the inbound-link check could not see every document. Their links to it become broken. Prefer fixing the links first.
* **Behavior**:
  Before deleting, runs the same inbound-link scan `read_article` runs, so a refusal names exactly the documents a read of the target lists as `Linked from:`. Self-links never block.
  * **Linked, without `break_links`**: refused with an error result — `Refused: nothing was deleted.` — naming **every** linking document as `- Title (slug)`, and suggesting to fix those links or call again with `break_links: true`.
  * **Incomplete check, without `break_links`**: refused too, even when no link was found — an empty backlink list only means "nothing links here" when the scan saw every document. This covers a scan that failed outright and one that skipped unreadable files or misplaced documents that may link to the target; the refusal lists what was skipped and points to `wiki_health` for the detail.
  * **With `break_links: true`**: deletes the Markdown file, all historical `.md.gz` backup snapshots, and any uploaded media assets associated with the slug, and de-indexes the document from Bleve search. The success response lists the documents that now hold broken links to it, plus anything the check could not see.
  * **Nothing links here and the check was complete**: deletes as above, with no `break_links` needed.

  A slug that does not exist is reported as not found and changes nothing (`idempotentHint: true`). To remove text from a document's history **without** deleting the document, use `save_article` with `purge_history: true` instead.
* **Annotations**: Title: `Delete Article`, `readOnlyHint`: `false`, `destructiveHint`: `true`, `idempotentHint`: `true`, `openWorldHint`: `false`.
* **Structured output**: None (prose response confirming deletion, or naming the links that blocked it).

```jsonc
{ "slug": "old-decision" }
// → Refused: nothing was deleted. 2 documents link to 'old-decision', and deleting it would break those links:
//   - Architecture Index (architecture-index)
//   - New Decision (new-decision)
//   Fix or remove those links first (wiki_health reports unreadable and misplaced entries in detail), or call delete_article again with break_links: true to delete anyway.

{ "slug": "old-decision", "break_links": true }
// → Success! … permanently deleted …
//   These 2 documents now hold broken links to 'old-decision': …
```

---

### 6. `get_wiki_overview`
Session orientation in one bounded call: document counts by type and plan status, the `user` and `feedback` memories that apply to any task, the plans in flight, recent activity since a duration, and the status vocabularies. This is the recommended first call of any agent session. It is **not** an index and does not list every document: use `search_wiki` without a query (with `type`, `status`, or `tag` filters and cursor paging) for the full listing, and `search_wiki` with a query to find documents on a topic. Broken links and other link-graph problems are `wiki_health`'s job, not the overview's.

* **Arguments**:
  * `since` (string, *optional*): Optional filter for recent activity. Accepts a Go duration (e.g. `24h`, `48h`) or RFC3339 timestamp. Defaults to `48h`.
* **Behavior**:
  Performs a metadata-only pass over the knowledge base and returns a payload whose size does not grow with the number of documents:
  * **Counts** of wiki articles, memories, plans, and skills (plus Attested Computations when there are any), and the number of plans in each lifecycle status.
  * **Pinned memories**: memories of kind `user` or `feedback`, newest-updated first, at most 25. Archived memories are excluded. `pinned_memory_total` is the count before the cap.
  * **Active plans**: plans with status `implementing` or `blocked`, newest-updated first, at most 25, with their tags (at most 10). `active_plan_total` is the count before the cap.
  * **Recent activity**: at most 20 events since `since`, from the durable activity log (`data/activity.jsonl`).
  * **Status vocabularies** for plans and skills.
  * **Next steps**: a pointer to `search_wiki` (without a query for the index, with one for a topic) and `read_article`.

  Each compact entry's description is its `description` (or the first line of its body when it has none), collapsed to one line and truncated to 200 characters with an ellipsis, so one long description cannot inflate the response. It does not scan the link graph: broken links, unreadable files, and misplaced documents are reported by `wiki_health`. The prose and the structured output are rendered from the same data. Example prose:

  ```text
  NexWiki Knowledge Base Overview (660 articles total)
  Documents: 197 wiki · 42 memories · 414 plans · 7 skills
  Plans by status: draft 30 · implementing 12 · blocked 1 · completed 180 · archived 191
  Plan statuses: draft, implementing, blocked, completed, superseded, parked, evergreen, archived
  Skill statuses: draft, ready, archived

  == Pinned Memories (user and feedback: 9) ==
  - Operator Profile (operator-profile) <user> — Who the operator is and how they work
  - No Issues In Pull Requests (no-issues-in-pull-requests) <feedback> — PRs describe the change; raise concerns in conversation

  == Active Plans (implementing or blocked: 13) ==
  - Compact Wiki Overview (compact-wiki-overview) [implementing] — Bound the orientation payload [nexwiki] (updated 2026-09-21)

  == Recent Activity (3 events since 48h) ==
  - 2026-09-21 10:02:11 [mcp/edit] save_article → 'Compact Wiki Overview' (compact-wiki-overview)

  == Next Steps ==
  This overview does not list every document. For the full index call search_wiki without a query, filtered by type ('articles', 'memories', 'plans', 'skills'), status, or tag and paged with cursor. To find documents about a topic give search_wiki a query. Open any entry with read_article(slug).
  ```
* **Annotations**: Title: `Get Wiki Overview`, `readOnlyHint`: `true`, `openWorldHint`: `false`.
* **Structured output**: `structuredContent` as `{total_articles, counts{wiki, memories, plans, skills, computations?}, plan_status_counts{<status>…, other?}, pinned_memories[{title, slug, description?, memory_kind}], pinned_memory_total, active_plans[{title, slug, status, description?, timestamp, tags?}], active_plan_total, recent_activity[], status_tags, next_steps}`. `plan_status_counts` carries every status in the plan vocabulary, zero included. `status_tags` carries the closed plan and skill status vocabularies. There is no `articles` field — the full listing is `search_wiki` without a query — and no link-graph `statistics` object: that is `wiki_health`.

---

### 7. `wiki_health`
Audits the knowledge base for maintenance work in one call: unreadable files, misplaced documents, orphan pages, broken internal links, agent memories recorded without a `source` or without a `memory_kind`, in-flight plans that have gone stale, unreferenced skills, cold memories, and near-duplicate memories.

* **Arguments**:
  * `stale_days` (integer, *optional*): How many days an in-flight plan may go untouched before counting as stale (default `30`).
  * `cold_days` (integer, *optional*): How many days a memory may go unread and unedited before counting as cold (default `90`).
  * `limit` (integer, *optional*): Maximum items reported per category (default `50`, maximum `500`). Counts are always complete even when lists are capped.
* **Behavior**:
  Runs twelve comprehensive checks over a single cached pass of the article directory:
  | Check | Finds | Why it matters |
  |---|---|---|
  | **Unreadable files** | An article file that cannot be read or parsed (malformed front matter, permissions), or an article folder that cannot be listed | Missing from listings and checks |
  | **Misplaced documents** | A document that parses but is not stored as `articles/<slug>.md` directly | Cannot be opened or edited by slug |
  | **Orphan pages** | A **wiki article** no other article links to | Unreachable by graph traversal |
  | **Broken internal links** | Target does not exist | Names `target_slug` to create and link `form` |
  | **Memories with no `source`** | An `AI-Agent-Memory` with empty provenance | Unverifiable facts |
  | **Unkinded memories** | An `AI-Agent-Memory` with no `memory_kind` | Cannot be recalled via kind-filtered search |
  | **Contested memories** | A memory tagged `contested` | Conflicting claims awaiting human decision |
  | **Stale plans** | An `AI-Agent-Plan` untouched for `stale_days`, not finished or parked | Work that quietly stalled |
  | **Stale concepts** | Any document past its `stale_after` date | Outdated assumptions |
  | **Unreferenced skills** | An `AI-Agent-Skill` no live document links or references | Skills nothing points an agent at |
  | **Cold memories** | An `AI-Agent-Memory` neither read nor edited within `cold_days` | Unconsulted knowledge |
  | **Duplicate memories** | Memories in the same scope with closely matching titles | Drifting parallel answers |
* **Annotations**: Title: `Wiki Health`, `readOnlyHint`: `true`, `openWorldHint`: `false`.
* **Structured output**: `structuredContent` as `{total_documents, stale_days, limit, truncated, unreadable_file_count, unreadable_files[], misplaced_document_count, misplaced_documents[], orphan_count, orphans[], total_links, broken_link_count, broken_links[], unsourced_memory_count, unsourced_memories[], unkinded_memory_count, unkinded_memories[], contested_memory_count, contested_memories[], stale_plan_count, stale_plans[], stale_concept_count, stale_concepts[], unreferenced_skill_count, unreferenced_skills[], cold_days, cold_memory_scan_ran, cold_memory_skipped_reason?, cold_memory_count, cold_memories[], duplicate_memory_count, duplicate_memories[], parked_plan_count, plan_status_census}`. `total_links` is every internal link scanned, broken or not, in either form (it was `get_wiki_overview`'s `statistics.total_links`).

> **OKF Bundle Operations:** OKF bundle import and export functionality is available via the NexWiki CLI (`nexwiki okf export`, `nexwiki okf import`) and REST API (`GET /api/okf/export`, `POST /api/okf/import`).

---

## 🔌 Connecting Clients

To connect your AI agents (Claude Desktop, Cursor, Copilot CLI, Claude Code, or Google `agy` CLI) to NexWiki, you can choose between two transport models:

1. **Streamable HTTP (Recommended 🚀)**:
   Connects the client directly to your active running web server on port `5808` (at `http://localhost:5808/api/mcp`).
   * **Advantages**: Zero process overhead, and **completely avoids database file lock contentions** (since the active running Go server process maintains exclusive locks, and all clients share it over HTTP).
2. **Stdio (Process-Based Alternative 📦)**:
   The client spawns its own `nexwiki -mcp-only` process on demand. At startup, the process checks for a NexWiki web server on `127.0.0.1` at its `-port` (default `5808`):
   * **Web server running**: The process proxies all MCP traffic to that server's `/api/mcp` and never opens the data directory, so there is no lock contention. See [Sidecar proxy mode](#-sidecar-proxy-mode).
   * **No web server**: The process opens the data directory itself and holds the search index lock until the client exits. It serves MCP only: no web UI, no plan lifecycle worker, and no live subscriptions.
   * **Disadvantages**: An extra process per client, and the check runs only once, at startup. While a standalone stdio process runs, a web server (or another standalone process) started on the same data directory cannot open the search index and exits with an error after 15 seconds. Each stdio message is capped at 8 MB (see [Stdio message size](#-stdio-message-size)).

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
   * **URL**: `http://localhost:5808/api/mcp`
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
      "url": "http://localhost:5808/api/mcp"
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
claude mcp add --transport http nexwiki http://localhost:5808/api/mcp
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
      "url": "http://localhost:5808/api/mcp"
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
