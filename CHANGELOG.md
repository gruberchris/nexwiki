# Changelog

All notable changes to NexWiki are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and NexWiki adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). NexWiki is pre-1.0: breaking changes may land in minor releases, and are always called out below.

## [Unreleased]

### Fixed
- **The link graph ignored the link form the house style prefers.** `ScanLinkGraph` read only `[[WikiLinks]]`, but the corpus is overwhelmingly written in absolute `[text](/articles/slug)` Markdown links — and `nexwiki-agent-guidelines` §5 tells every connected agent to write them that way. Measured on an 84-document wiki: **293 Markdown links against 57 WikiLinks, and the home page's 48 internal links were 100% invisible.** Three consumers were wrong as a result, and all three are fixed by one scanner change:
  - `wiki_health` reported **0 broken links against 26 real ones** (14 of them pointing at a `/articles/rust` that had been renamed).
  - `wiki_health` called **44 of 84 documents orphans (52%)**; the true number is 2. §6.5 had already tuned this check once after it fired on 84% of the wiki, and the remaining cause was that the majority link form was never counted.
  - `get_backlinks` — which the guidelines tell agents to run *before* a rename or delete — under-reported inbound references, the dangerous direction for that use.
- **Renaming an article now heals inbound Markdown links too**, not just `[[WikiLinks]]`. Only the destination is rewritten (`](/articles/old)` → `](/articles/new)`); the link text is left exactly as the author wrote it, the same guarantee `[[old|display]]` already had. Unhealed Markdown links are presumably how `/articles/rust` came to be broken in 14 places.
- **The editor's quick-fix action pasted prose into the article.** `LintDiagnostic.suggestion` carried two different things — replacement text for rules like `MD034`, and human guidance for a broken WikiLink — and neither quick-fix path (the CodeMirror lint action or the right-click menu) could tell them apart. Both inserted it verbatim, so choosing **Fix** on a broken `[[Foo]]` replaced it with the sentence *"Click to create this page."* The field is now split into `fix` (replacement text, the only thing a quick-fix will insert) and `hint` (displayed, never inserted), which makes the mistake unrepresentable rather than merely documented. A test asserts every `fix` a document produces is Markdown rather than prose.
- **The viewer opened the wiki's own pages in a new tab.** `Viewer.tsx` had a branch for `wikilink:` URLs and a fallback for external links, and nothing in between, so an absolute `/articles/<slug>` link rendered with `target="_blank"` and full-reloaded the app. Both internal forms now navigate in place, and both render the same red-dotted create prompt when the target does not exist.

### Added
- **`MDLINK_BROKEN` editor diagnostic** — the linter now underlines absolute `[text](/articles/slug)` links whose target does not exist, alongside the existing `WIKILINK_BROKEN`. Both link rules now skip fenced code blocks, matching the server-side scanner, so a documented `[Title](/articles/slug)` example is not reported as a link.
- **`broken_links[].form`** on `wiki_health` and `get_wiki_statistics` — `"wikilink"` or `"markdown"`. Reports print the link in the syntax it was written in, because an agent told to fix `[[rust]]` in a file that actually says `[Rust](/articles/rust)` will not find it.

## [0.9.0] — 2026-08-09

### Added
- **Agent attribution is real.** `get_article_history` now reports who made each revision and through which tool, joined from the activity log, alongside the article's own `source` — so a revision reads as "Claude Desktop 1.4.2 wrote this on 2026-08-09 via `edit_wiki_article`, citing X". The History drawer shows it too. Identity is resolved most-specific-first: per-request `clientInfo` from the `2026-07-28` `_meta` envelope, then the `clientInfo` a legacy client sent at `initialize` **on stdio**, then the new `NEXWIKI_AGENT_NAME` / `-agent-name` setting, then `AI Agent`. The stdio restriction is deliberate: HTTP is sessionless by design, so caching a handshake there would credit one client's writes to another.
- **`NEXWIKI_AGENT_NAME` / `-agent-name`** — the attribution recorded for MCP clients that do not identify themselves. Distinct from `-name`, which is the wiki's display title.
- **`wiki_health` gained three memory-hygiene checks**, without adding a tool — the count stays at 28. *Cold memories*: an `AI-Agent-Memory` neither read nor edited within `cold_days` (default 90); reads count, because a memory the agent keeps consulting is alive even if nobody has edited it in a year. *Duplicate memories*: pairs within one `memory-<scope>` whose titles overlap heavily, excluding pairs that already link to each other, since an author who cross-linked two documents has already decided to keep them apart. *Parked plans*: `parked`, `deferred`, `tabled`, `on-hold`, and `someday` now end a plan's staleness the way `completed` does, but are reported as a count rather than silently hidden.
  - The cold check **refuses to run when it cannot be trusted.** Recency comes from the activity log, so on a fresh install — or after `NEXWIKI_ACTIVITY_MAX_ARCHIVES` pruning — the log can be younger than `cold_days`, and then every memory looks untouched. Rather than report all of them it is skipped, with `cold_memory_scan_ran: false` and a reason.
- Benchmarks covering large-corpus behavior at 1,000 / 5,000 / 10,000 documents. They are benchmarks, so `go test ./...` does not pay for them.

### Changed
- **BREAKING: `NEXWIKI_NAME` no longer sets the activity log's `agent` field.** It is the wiki's display title and was being copied into attribution, so on any deployment that set it — including the compose examples in the README — every agent write was credited to the wiki itself, and the Activity drawer's agent filter had one value for everything. Attribution now comes from the sources listed above. Existing log entries keep the values they were written with; they are history, not data to migrate.
- **Boot indexing is batched.** `SyncSearchIndex` committed one Bleve transaction per document, making startup linear at roughly 24 ms per document — and the server answers nothing until it finishes. Documents are now indexed in batches of 500, bounded rather than one batch for the whole corpus so a large wiki does not trade a startup delay for a startup allocation spike. Measured: **1,000 documents 26.3 s → 0.28 s; 5,000 119.6 s → 1.19 s; 10,000 238.4 s → 2.28 s (~104×).** The orphan-reconciliation query in the same function also asked Bleve to size a collector for 1,000,000 hits regardless of corpus size, and now uses the real document count.

### Fixed
- **Tool calls that failed were recorded in the activity log as if they had succeeded.** A tool that refuses its work returns an error *inside* a well-formed JSON-RPC result rather than as a JSON-RPC error, and the logging hook only checked for the latter. So an edit rejected by optimistic locking left the article untouched and still appeared in the log as a completed edit by whoever attempted it — and `get_recent_activity`, which agents are told to call at session start, would report work that never happened.
- **`get_article_history` attribution no longer credits one edit to several revisions.** Article timestamps are stored at one-second resolution, and an agent produces several revisions well inside a second, so matching each revision to its nearest log event independently handed the same event to more than one. Assignment is one-to-one, oldest revision first. Revisions with no matching event report no author rather than a guessed one, which is the normal case for any wiki older than its activity log.

### Security
- Documented in `SECURITY.md` that **agent attribution is not authentication.** The value is self-reported by the MCP client, and NexWiki is unauthenticated, so it is a convenience for telling your own agents apart — not evidence of who made a change. Self-reported names are length-capped and stripped of control characters before they reach the durable log.

## [0.8.0] — 2026-08-09

### Fixed
- **The container build was broken by the Tailwind v4 migration.** `Dockerfile` carried a `RUN npm install -D tailwindcss@3` that silently overrode `package.json`, so the image installed v4 and then downgraded to v3, and the build died on `@import "tailwindcss"` — postcss-import tried to read `tailwindcss/lib/index.js` as a stylesheet. The pin is gone; the version lives in `package.json` alone. Nothing caught this because **CI never built the image** — only the release workflow did, on a tag push, so a broken Dockerfile stayed invisible until after a tag existed. CI now builds the image on every PR and smoke-tests that the running container serves the expected frontend assets.

### Changed
- **Migrated to Tailwind CSS v4.** Configuration moves out of `tailwind.config.js` and into CSS: `@import "tailwindcss"`, an `@theme` block for the colour tokens, and `@tailwindcss/postcss` as the PostCSS plugin. `darkMode: 'class'` becomes an `@custom-variant`. Verified by diffing every class selector the built stylesheets define — v3 emits 705, v4 emits 726, and nothing v3 generated is missing. Two utilities that v4 silently redefines were renamed to preserve the old rendering: `shadow-sm` → `shadow-xs` (v4's `shadow-sm` is visibly larger) and `outline-none` → `outline-hidden` (v4's `outline-none` drops the transparent outline that keeps focus visible in forced-colors mode). The stylesheet grows 10.8 kB → 15.1 kB gzipped, all of it v4's `color-mix()` fallbacks and `@property` declarations.
- Added a `.dockerignore`. Without one, `COPY frontend/ ./` copied the host's `node_modules` into the image on top of the ones just installed, landing darwin-arm64 binaries in a linux image and inflating the build context. The Dockerfile also now uses `npm ci` rather than `npm install`, so the image is built from exactly the dependency tree `package-lock.json` pins.
- Removed `autoprefixer`. Tailwind v4 prefixes its own output, so the second PostCSS pass was redundant — verified by rebuilding without it and diffing: the emitted stylesheet is byte-identical.
- **The 32 theme variants are now covered by tests.** 16 built-in themes × light/dark had no test at all; the ten theme colours are restated in four places that must agree, with nothing in the type system connecting them. Go asserts every theme defines every colour; the frontend asserts `:root` defaults exist, the Tailwind theme maps each one, applying a theme projects all ten onto `:root`, and every theme utility the components reference is actually declared.

### Security
- Bumped `golang.org/x/sys` to v0.44.0 for **GO-2026-5024** (integer overflow in `NewNTUnicodeString`, Windows only). NexWiki's own code never calls the affected symbol, so it was not exploitable here, but NexWiki ships a `windows-amd64` binary and the fix is a transitive version bump.

### Added
- **`create_agent_memory` and `create_agent_plan` now accept `tags`.** `create_wiki_article` and `create_agent_skill` always did, so an agent that wanted a plan marked `wip` had to follow the create with `update_article_tags` — a tool annotated `destructiveHint: true`, which makes a cautious client stop and ask the user to approve a second call that only existed because the first tool lacked an argument its siblings had. Tool-managed tags stay reserved: the `memory-<scope>` and project-context tags are still derived automatically, and a caller cannot forge a `memory-*` tag through the new argument.

### Fixed
- **`import_okf_bundle` rejected every document in a bundle NexWiki did not produce itself.** The front-matter parser required a `slug`, but `slug` is a NexWiki *custom* key, not an OKF canonical one — so a conformant bundle from any other tool had all of its documents skipped with a warning, and the interoperability feature only worked against NexWiki's own exports. The slug is now derived from the title when absent, which is exact rather than a guess: articles are always written as `Slugify(title).md` with that same value in the front matter, so the two can never disagree on disk.
- **The OKF import report never flagged documents whose type it had coerced.** `import_okf_bundle` documents itself as permissive — a missing or unrecognized `type` defaults to `Wiki` and is *flagged* rather than rejected — but the check compared against the already-normalized type, so it could never fire and `MissingType` was always empty. The coercion happened; only the report of it was missing, which is the half that makes permissiveness auditable.
- **The activity log never rotated in a long-running server.** The 10 MB threshold was only checked when the log was opened, which happens once at startup — so a deployment that stays up (`docker compose up -d`, the documented setup) grew `activity.jsonl` without bound until the next restart. It now rotates on append as well. This also bounded a compounding read cost: `get_recent_activity` stops early across *archives* but always parses the active file end to end, so an unbounded log made the call slower every time it ran. Measured on a synthetic log: 10 MB → 119 ms, 50 MB → 613 ms, growing linearly. A rotation that cannot rename now degrades to appending to the existing file rather than dropping events.

## [0.7.0] — 2026-08-09

### Added
- **`wiki_health` — a new 28th MCP tool.** One call audits the knowledge base for maintenance work: orphan wiki articles nothing links to, broken WikiLinks, agent memories recorded without a `source`, and plans left unfinished and untouched (`stale_days`, default 30). Archived documents are skipped, `home` is never an orphan, orphan detection covers wiki articles only (nobody WikiLinks a memory), and a plan tagged `completed`, `done`, or `superseded` is never stale however old.
- **Structured tool output.** Twelve read tools (`search_wiki`, `read_article`, `list_articles`, the three `list_agent_*` tools, `get_backlinks`, `get_article_history`, `get_wiki_statistics`, `get_status_tags`, `get_recent_activity`, `wiki_health`) now declare an `outputSchema` and return a `structuredContent` object alongside their prose, so an agent parses data instead of scraping sentences — `read_article` hands back `version` as a number to pass straight to `edit_wiki_article` as `loaded_version`. The human-readable text is still emitted and is rendered from the same value, so the two halves cannot disagree; tools without a schema are byte-identical on the wire.
- **Sidecar proxy mode.** A `-mcp-only` process beside a running web server now forwards MCP traffic to it instead of failing on the search-index lock, so the documented Claude Desktop stdio configuration works. Writes land in the live wiki, and subscription streams are relayed to stdout — live subscriptions a standalone stdio server cannot provide.
- **MCP Resources.** Every document is exposed at `nexwiki://article/{slug}` via `resources/list`, `resources/read`, and `resources/templates/list`, so a user can `@`-mention a wiki page in their client instead of spending a tool call on it.
- **`subscriptions/listen`.** A long-lived notification stream delivers `notifications/resources/updated` and `notifications/resources/list_changed` off the existing EventBus, so an agent learns the moment a page is edited in the browser or another agent writes a memory.

### Changed
- **`get_wiki_statistics` now scans the home page's WikiLinks too.** It built its document set from the article listing, which excludes `home` — so links written on the home page, the page a user is most likely to link from, were never checked. Both it and `wiki_health` now share one cached link-graph scan, replacing a read-every-file-in-full loop.
- `frontend/src/components/Editor.tsx` decomposed into `useSplitPane`, `useTagEditor`, and an `editorExtensions` module (984 → 776 lines). No behavior change.

### Fixed
- **Graceful shutdown blocked on open SSE streams.** `http.Server.Shutdown` waits for connections to become *idle*, and an SSE stream never does — so a single browser tab on the wiki held shutdown open until its own deadline, past the 10 s grace a container runtime allows, and the process was SIGKILLed before the Bleve index could be closed. That is exactly the corruption graceful shutdown exists to prevent, so the mechanism added in 0.6.0 only appeared to work when nothing was connected. Measured with one browser stream open: `docker stop` went from **10,200 ms / exit 137 with the index left open** to **195 ms / exit 0**. Every long-lived stream — the browser activity stream, the MCP GET keepalive, and `subscriptions/listen` — now selects on a shutdown signal, and the shutdown deadline drops from 15 s to 5 s so it sits below the container stop grace.
- **`search_wiki` returned one fewer result than the requested `limit`** on every search that did not pass a `type` or `tags` facet — including the documented default of 40. Bleve applies its size cap before NexWiki's own filters run, so the search over-fetches to compensate, but the over-fetch was gated on a facet being present; three filters (`home` is always excluded, archived documents are excluded by default, deleted files are skipped) apply to *every* search. Measured before: `limit` 4→3, 5→4, 8→7, 40→39. Now exact at every value.
- **Tool argument errors named the wrong field.** 18 of 22 handlers folded the JSON decode into their required-field check, so any malformed payload was reported as a missing required field. Passing `search_wiki` a string `type` where the schema wants an array answered `Missing or invalid 'query' argument` — for a request whose `query` was correct — sending the agent to fix the one argument that was already right. Errors now name the offending field in JSON Schema vocabulary: `Invalid arguments: 'type' expects array, got string`.
- **The stdio MCP server died permanently on any message over 64 KB**, which a `create_wiki_article` call carrying an article body passes easily. The read loop used `bufio.Scanner`'s 64 KB default with no `Buffer()` call, and the failure was silent in the worst way: standalone (`-mcp-only`) the process exited with **status 0**, so a supervising client saw a clean shutdown rather than a crash, while alongside the web server the background loop died as HTTP kept serving 200s. Either way the agent got no response at all and the article was never written. Both stdio paths now share an 8 MB `MaxStdioLineBytes` limit, and overrunning it emits a JSON-RPC parse error naming the limit instead of going silently quiet.
- **`subscriptions/listen` was answered only for legacy-era clients on stdio** — exactly backwards, since the method was introduced by the `2026-07-28` revision. A modern client, the only kind that knows the method exists, got `-32601 Method not found`; a legacy client got the graceful acknowledgment. Stdio now intercepts the method before the era branch, mirroring the HTTP transport, and still validates modern `_meta` first so malformed requests fail identically across transports.

## [0.6.0] — 2026-08-09

### Added
- **MCP tool annotations.** All 27 tools now declare `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`, and a human-readable `title`, so clients can auto-approve safe reads rather than prompting for every call. `openWorldHint` is `false` on every tool — NexWiki never reaches outside the local wiki.
- **MCP protocol revision `2026-07-28` (dual-era).** NexWiki now serves the current per-request-metadata revision *and* older `initialize`-based clients on the same endpoint, choosing per request. Includes `server/discover`, `resultType` result envelopes, `MCP-Protocol-Version` / `Mcp-Method` / `Mcp-Name` header validation, and the `-32020` / `-32022` error codes.
- **`search_wiki` facets:** `type`, `tags`, `limit`, and `include_archived` arguments.
- **`-bind` flag / `NEXWIKI_BIND`** to restrict the listening interface (e.g. `127.0.0.1`). Defaults to all interfaces so Docker deployments keep working.
- `X-Accel-Buffering: no` on SSE responses, so reverse proxies stop buffering the stream.
- Open-source scaffolding: this `CHANGELOG.md`, a pull-request template, Dependabot configuration, and [`CONTRIBUTING.md`](./CONTRIBUTING.md) covering the docs structure and the Documentation Integrity Rule.

### Changed
- **`README.md` restructured** to lead with positioning — *Why NexWiki? → Quick Start → Connect your AI agent → Features* — collapsing three overlapping quickstarts into one.
- **`search_wiki` now searches every document type by default.** Previously memories, plans, and skills were hidden unless the *query text* contained the words "memory", "plan", or "skill" — so a memory about Elasticsearch was invisible to a search for "elasticsearch". Human/browser search is unchanged and still hides agent documents.
- Article metadata and the WikiLink graph are cached, validated by file modification time so edits made outside NexWiki are still picked up. `ListArticles` is ~7.8× faster and `GetBacklinks` ~17.6× faster on a 200-article wiki.
- MCP tools moved to a registry that pairs each schema with its handler, so the two cannot drift apart. `server/mcp.go` dropped from 2,323 to 590 lines.
- `frontend/src/App.tsx` decomposed into `useTheme`, `useArticleActions`, and `useRouter` (1,191 → 821 lines; 29 → 18 `useState`). No behavior change.
- Dependencies updated across Go and the frontend; the unused `react-router-dom` dependency was removed.

### Fixed
- **A `-mcp-only` sidecar sharing a data directory hung forever at startup**, silently — this was the documented Claude Desktop stdio configuration. It now fails fast with an actionable message naming the Streamable HTTP transport.
- Unbounded request bodies and OKF bundle decompression (zip-bomb exposure); both are now capped, returning `413` where appropriate.
- Path containment now uses `filepath.Rel` instead of a string-prefix check, which accepted a sibling directory sharing the expected prefix.
- MCP `edit_wiki_article`'s optimistic-locking check is now atomic with its write. Previously the read-check-write had no lock spanning it, so a concurrent writer could land in the gap and the guard would still pass.
- MCP `protocolVersion` reported `2024-11-05` while the documentation advertised the 2025 specification.
- The Streamable HTTP transport committed `200` before dispatching, so no outcome could report a non-200 status; responses are now buffered and notifications return `202`.
- Broken-WikiLink affordance is a real `<button>`, reachable by keyboard and announced to screen readers; nine icon-only buttons gained `aria-label`, and the sidebar disclosure gained `aria-expanded`.

## [0.5.2] — 2026-08-08

### Fixed
- Enhanced CORS handling, added a mutex for concurrent writes, and tightened security ([#17](https://github.com/gruberchris/nexwiki/pull/17)). In detail:
  - **Wildcard CORS on every route.** With no authentication, any website you visited could read and delete your entire wiki over `localhost`. Origins are now validated against an allow-list (`NEXWIKI_ALLOWED_ORIGINS`).
  - **Stored XSS** in the search-snippet fallback path, which emitted unescaped article content into a `dangerouslySetInnerHTML` sink.
  - **Lost revisions under concurrent writes.** `SaveArticle` assigned version numbers without a lock; a measured test lost 8 of 13 revisions with 12 concurrent writers.
  - **No graceful shutdown.** SIGTERM (every `docker stop`) killed the process with the Bleve index open — the likely cause of search-index corruption on restart. (Completed in 0.7.0, which fixed the open-stream case.)
  - SVG uploads are served with `Content-Disposition: attachment` and a restrictive CSP so they cannot execute as same-origin scripts.
  - HTTP server read/idle timeouts (Slowloris) plus `X-Content-Type-Options`, `Referrer-Policy`, and `X-Frame-Options`.
  - Search results no longer render a hardcoded `http://localhost:8080` URL.

### Security
- Added [`SECURITY.md`](./SECURITY.md) with the trust model and a private vulnerability reporting path.
- **NexWiki has no authentication.** This is now stated explicitly in the README and SECURITY.md rather than left implicit.

## [0.5.1] — 2026-08-02

### Changed
- Updated agent integration and MCP server configuration documentation.

## [0.5.0] — 2026-08-02

### Changed
- Simplified the NexWiki AI skill.

## [0.4.0] — 2026-06-20

### Added
- OKF-compliant content import and export, replacing the proprietary export format.

### Changed
- `edit_agent_plan` supports full content editing with validation and type safety.

## [0.3.0] — 2026-06-20

### Changed
- `edit_agent_plan` supports full content editing with validation and type safety.

## [0.2.5] — 2026-06-20

### Added
- Second brain enhancements, including support for the Open Knowledge Format (OKF) specification.

## [0.2.3] — 2026-06-11

### Added
- Archive tag support.
- Theme expansion.

## [0.2.1] — 2026-06-07

### Added
- Contributor files and issue templates.
- Expanded unit test coverage.

### Fixed
- Container publishing under an "unknown" tag.
- Contributor workflow execution.

## [0.2.0] — 2026-06-05

### Added
- CI/CD pipeline.

[Unreleased]: https://github.com/gruberchris/nexwiki/compare/v0.9.0...HEAD
[0.9.0]: https://github.com/gruberchris/nexwiki/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/gruberchris/nexwiki/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/gruberchris/nexwiki/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/gruberchris/nexwiki/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/gruberchris/nexwiki/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/gruberchris/nexwiki/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/gruberchris/nexwiki/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/gruberchris/nexwiki/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/gruberchris/nexwiki/compare/v0.2.5...v0.3.0
[0.2.5]: https://github.com/gruberchris/nexwiki/compare/v0.2.3...v0.2.5
[0.2.3]: https://github.com/gruberchris/nexwiki/compare/v0.2.1...v0.2.3
[0.2.1]: https://github.com/gruberchris/nexwiki/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/gruberchris/nexwiki/releases/tag/v0.2.0
