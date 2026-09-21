# Changelog

All notable changes to NexWiki are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and NexWiki adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). NexWiki is pre-1.0: breaking changes may land in minor releases, and are always called out below.

## [Unreleased]

### Changed
- **BREAKING: `get_wiki_overview` is a bounded orientation, not an index**: the structured `articles` field is removed, and the prose no longer lists every document. On a wiki of 660 documents the default call returned about 926,000 characters — for the call every session is told to make first. It now returns, in both prose and `structuredContent`, a payload whose size does not grow with the wiki: `total_articles`, `counts` by type (`wiki`, `memories`, `plans`, `skills`, and `computations` when present), `plan_status_counts`, `pinned_memories` (memories of kind `user` or `feedback`, newest first, at most 25, with `pinned_memory_total`), `active_plans` (plans `implementing` or `blocked`, newest first, at most 25, with `active_plan_total`), `recent_activity`, `status_tags`, `statistics`, and `next_steps`. Compact entries carry a one-line description truncated to 200 characters. For the full document index call `list_articles` (with `type`, `status`, or `tag` filters and cursor paging); to find a topic call `search_wiki`. `since` and `include_stats` are unchanged.
- The connect-time MCP instructions, the seeded `nexwiki-agent-guidelines`, the `nexwiki` agent skill, and the guides describe the overview accurately and point at `list_articles` and `search_wiki` for the full index. An existing wiki keeps its own `nexwiki-agent-guidelines` page; edit its orientation section to match.

## [0.22.0] — 2026-09-21

### Added
- **History purge — redact without deleting**: `save_article` takes `purge_history: true` on an update (with `loaded_version`), and `PUT /api/articles/{slug}` takes `"purge_history": true`. After the save, every earlier revision of the document is permanently deleted from `data/history/` — body and front matter alike — and the document's earlier activity-log entries (durable log, rotated archives, and the live feed) are retitled to its current title and slug. The document, slug, type, status, backlinks, and version counter are unchanged. The response reports `revisions_removed` and `activity_entries_retitled`. See the [History Redaction & Audit Guide](docs/history_redaction_guide.md).
- **History search**: `search_wiki` takes `include_history: true` to scan every stored revision and live file for the query as a case-insensitive literal substring, returning `history_matches: [{slug, title, version, current}]` in `structuredContent` and in the prose. The type, tag, and archived filters do not narrow this scan.
- **`type` on REST creation**: `POST /api/articles` accepts `type` (and `memory_kind` for a memory); it no longer forces `Wiki`.

### Changed
- **`type` is honoured on update**: `save_article` and `PUT /api/articles/{slug}` change a document's type when `type` is passed, instead of silently discarding it. Omitting `type` still keeps the current one. `status`, `memory_kind`, `memory_type`, and `project_context` are validated against the new type. Into a plan without a status enters at `draft`; into Wiki or a memory drops the lifecycle status; a plan or skill status the new type rejects must be replaced explicitly; leaving a memory drops its `memory-<scope>` tags. The update response now echoes `Type:` and, on a change, `Type: Old → New`.
- **BREAKING: an unknown type is an error**: a caller that relied on a typo or an unrecognized value silently becoming `Wiki` now gets an error. `save_article` (create and update), `list_articles`, `POST`/`PUT /api/articles` (400), and the OKF importer over an existing slug reject an unrecognized type naming the valid values, instead of falling back to `Wiki`. `Attested Computation` is accepted by its canonical spelling.
- **The OKF importer relabels an existing document only by a declared type**: a bundle entry with no `type` keeps the existing document's type.

### Fixed
- **`search_wiki` advertised only half its filters**: `memory_kind` and `include_archived` were honoured but absent from the input schema, so an agent reading `tools/list` could not discover them. Both are now declared.
- **`get_wiki_overview` violated its own output schema**: `statistics.broken_links` serialized as `null` whenever `include_stats` was off or nothing was broken, so strict clients rejected every call. It is now always an array.
- **Agents were told to call tools that do not exist**: the connect-time MCP instructions, the seeded `nexwiki-agent-guidelines`, the MCP prompts, `wiki_health` findings, and the docs named retired tools (`create_agent_plan`, `get_context_overview`, `get_recent_activity`, `get_status_tags`, …). They now name only the nine registered tools, and a test fails if any agent-facing text names an unregistered one.

## [0.21.0] — 2026-09-19

### Changed
- **Consolidate MCP Tool Surface from 29 Tools to 9 High-Efficiency Tools**:
  - The exposed Model Context Protocol (MCP) tool surface is consolidated from 29 granular tools down to 9 high-efficiency tools using a Unified Document Model. This reduces prompt context overhead by ~80% (from ~9,250 tokens down to ~1,850 tokens in standard MCP clients) and eliminates LLM tool-choice hesitation and ambiguity.
  - The 9 exposed tools are: `search_wiki`, `read_article`, `save_article`, `append_article`, `list_articles`, `delete_article`, `get_wiki_overview`, `get_backlinks`, and `wiki_health`.
  - Canonical documentation across `docs/mcp_server.md`, `README.md`, `AGENTS.md`, and all guides updated to reflect the 9-tool surface.
  - The `nexwiki` agent skill and reference guides (`remember.md`, `plan.md`, `search.md`, `ingest.md`) updated to direct agents to the consolidated workflows.

### Added
- **`save_article` Unified Document Upsert**:
  - Replaces 9 individual create, edit, and tag-update tools (`create_wiki_article`, `edit_wiki_article`, `create_agent_memory`, `edit_agent_memory`, `create_agent_plan`, `edit_agent_plan`, `create_agent_skill`, `edit_agent_skill`, `update_article_tags`).
  - Supports all OKF document types (`Wiki`, `AI-Agent-Memory`, `AI-Agent-Plan`, `AI-Agent-Skill`), optimistic locking via `loaded_version`, plan lifecycle status validation, memory scope tagging (`memory-<scope>`), secret redaction, and revision logging.
- **`append_article` Direct Content Appending**:
  - Replaces `append_agent_memory` and `append_agent_plan`, enabling additive note-taking and progress logging to any document with optimistic locking support.
- **`get_wiki_overview` Unified Orientation Tool**:
  - Replaces `get_context_overview`, `get_recent_activity`, `get_wiki_statistics`, and `get_status_tags`. Combines the progressive disclosure article/memory index, recent activity within a `since` window, recognized status vocabulary, and optional link-graph statistics (`include_stats`).
- **Historical Revision Retrieval in `read_article`**:
  - `read_article` accepts an optional `version` parameter to load historical snapshots directly, superseding `get_article_history`.
- **Filtering and Pagination in `list_articles`**:
  - `list_articles` accepts `type` (filtering by `Wiki`, `AI-Agent-Memory`, `AI-Agent-Plan`, or `AI-Agent-Skill`), `status`, and `tag` filters, along with cursor pagination.
- **Unified Deletion in `delete_article`**:
  - `delete_article` handles deletion across all document types.
- **Consolidated Tools End-to-End Integration Suite**:
  - `TestConsolidatedToolsEndToEnd` exercises all 9 tools across complete document creation, revision, locking, query, and deletion lifecycles.

### Removed
- **Redundant MCP Micro-Tools and Typed CRUD Endpoints**:
  - Retired 20 duplicate tools from the MCP tool surface: `create_wiki_article`, `edit_wiki_article`, `delete_wiki_article`, `update_article_tags`, `get_article_history`, `revert_article_version`, `create_agent_memory`, `append_agent_memory`, `edit_agent_memory`, `delete_agent_memory`, `list_agent_memories`, `create_agent_plan`, `append_agent_plan`, `edit_agent_plan`, `list_agent_plans`, `create_agent_skill`, `edit_agent_skill`, `list_agent_skills`, `get_status_tags`, and `get_recent_activity`.
  - Removed `export_okf_bundle` and `import_okf_bundle` from the real-time MCP server tool registry (retained for CLI and REST usage).

### Fixed
- **MCP Activity Logging and Real-Time Event Dispatch for `save_article`**:
  - `mcpToolAction` in `server/mcp.go` now recognizes the `save_` tool prefix as a mutation rather than falling back to `"read"`. New documents created via `save_article` publish `article-added` and edits publish `article-edited` WebSocket/SSE events with accurate revision numbers.

## [0.20.0] — 2026-09-19

### Added
- **Unreadable Files and Folders in `wiki_health` and `get_wiki_statistics`**:
  - `wiki_health` has an eleventh check: `unreadable_file_count` and `unreadable_files` (`[{path, error}]`, sorted by path, a folder's `path` ending in `/`), capped by `limit` and flagged in `truncated`. It is listed first in the prose report, counts toward the wiki needing attention, and gives files and folders different remedies. The `error` values contain no absolute server paths.
  - `get_wiki_statistics` gains `unreadable_file_count`, and its summary says when files or folders were left out of the counts.
- **Misplaced Documents in `wiki_health` and `get_wiki_statistics`**:
  - `wiki_health` has a twelfth check: `misplaced_document_count` and `misplaced_documents` (`[{path, slug, remedy}]`, sorted by path, capped by `limit`), reporting documents that parse but are not stored as `articles/<slug>.md` — one in a subfolder, a copy whose filename differs from its slug, or one whose slug is not in slug form. It is listed after the unreadable files in the prose report and counts toward the wiki needing attention.
  - Every `remedy` is safe to follow: it names a destination that is free, never suggests overwriting another document, and never tells two misplaced documents to move to the same place.
  - `get_wiki_statistics` gains `misplaced_document_count`.
- **Skipped-Document Indicators in `get_backlinks` and `read_article`**:
  - A backlink scan that skipped unreadable or misplaced documents returned a partial answer indistinguishable from a complete one. The structured output of both tools now carries `skipped_document_count` and `skipped_documents` — the first 20, sorted by path, each with `path` and a `reason` of `unreadable` or `misplaced` (misplaced entries also carry their declared `slug`) — and a prose `Note:` line points at `wiki_health` for the full picture.
  - Both fields are absent when nothing was skipped; a nonzero count means the backlink list is a lower bound. REST responses are unchanged.

### Changed
- **Atomic Writes for Articles, History Snapshots, and Uploaded Assets**:
  - Saves used to truncate the live file and write into it, so a scan racing a save could read an empty or half-written article and drop it from its results, a history read could catch a snapshot before its gzip footer was written, and a download racing a re-upload could receive a truncated asset.
  - Each write now goes to a `.nexwiki-<n>.tmp` file in the same directory, is synced to disk, and is renamed into place. The file keeps its permission mode where the filesystem allows; if a mount rejects the `chmod` needed to keep it (some SMB/CIFS and FUSE mounts), the save still succeeds with the new file's default mode and logs a warning.
  - Temp files left behind by a crash are removed at startup: from the article directory before the home page is seeded, and from the history and asset trees in the background.
  - Side effects of replacing a file rather than rewriting it: sync clients such as Syncthing or Dropbox may briefly see a `.nexwiki-*.tmp` file, hard links to an article file keep the old content, the saved file is owned by the user NexWiki runs as, and on Unix a read-only article file is replaced on save. Documented in `docs/production_deployment.md`.
- **Misplaced Documents Are Rejected, and Rename Healing Keeps to the File It Read**:
  - A document is valid only at `articles/<slug>.md` at the top level. A file anywhere else — in a subfolder, or with a front-matter slug that differs from its filename — is misplaced: it is left out of listings, the link graph, backlinks, asset referrers, and the search index; reads and writes by slug return not found; and a create that would overwrite one is refused (REST answers `409 Conflict`). Each is warned about once per file version. Previously such files were listed but could not be opened, searched, or exported.
  - Filename comparison follows the article directory's case sensitivity, detected once at startup.
  - Renaming an article healed links by rewriting any file whose name looked right, so `cp bar.md bar-copy.md` followed by a rename could heal links into `bar-copy.md`, creating a duplicate or overwriting another article. Healing now rewrites only canonical documents in place, uses front-matter slugs, warns about the referrers it skips, and never writes a file other than the one it read. The plan lifecycle worker keeps plans that a misplaced document links to.
- **Documentation Corrections for stdio, `-mcp-only`, Dry-Run, and the Data Directory**:
  - `-mcp-only` is described accurately everywhere: it proxies to a running web server without opening the data directory, opens storage itself when none is found, and only successful tool calls are logged to the activity log.
  - The failure mode of a stdio launch without `-mcp-only` is the one that actually happens: the process starts as a second web server and exits — with a bind error pointed at a different data directory, or a search-index lock error pointed at the running server's data directory.
  - Dry-run mode writes nothing to the activity log, including `delete-refused` events for plans the backlink guard keeps.
  - The data directory documentation lists everything NexWiki writes there: `history/`, rotated activity archives, OKF export zips, and `.nexwiki-*.tmp` temp files.
  - False claims were dropped from tool descriptions: the storage-footprint claim on `get_wiki_statistics` and the event-lag claim on `get_recent_activity`.

### Security
- **The Host Header Is Validated on Every Request**:
  - The web server now accepts a request only when its `Host` header names this NexWiki instance: loopback names and IPs (including `*.localhost`), IP literals, the hostname of an origin listed in `NEXWIKI_ALLOWED_ORIGINS`, and the configured `-bind` hostname are accepted, as is any Host when `NEXWIKI_ALLOWED_ORIGINS` includes the `*` opt-in. Any other Host is refused with `403` before origin checks and handlers run, with a sanitized hint to add the site's origin to `NEXWIKI_ALLOWED_ORIGINS`.
  - Clients that reach NexWiki by a hostname — a Docker service name, `host.docker.internal`, an mDNS `.local` name, Tailscale MagicDNS, or Kubernetes service DNS — must list that origin (e.g. `http://nexwiki:5808`) in `NEXWIKI_ALLOWED_ORIGINS`, or those requests receive `403`.
  - The browser `Origin` rules are unchanged.

### Fixed
- **Unreadable Article Files and Folders Are No Longer Skipped Silently**:
  - An article file that could not be read or parsed (e.g. malformed front matter) was left out of listings, link scans, and health reports without a trace. It is still skipped, but the server now logs `Warning: skipping unreadable article file <path>: <error>` to stderr, with the path relative to the article directory, once per file version per server run: again only if the file changes and still fails or breaks again after being fixed, and once more at startup after a restart if it is still broken.
  - One bad entry no longer fails an article listing or link scan. An article folder that cannot be listed, or a file that cannot be stat'd, is skipped and logged the same way (a folder as `Warning: skipping unreadable article directory <path>/: <error>`), and a file deleted or renamed mid-scan is skipped silently. Previously each of these could fail the whole listing, and at boot, server startup.
  - The startup search index sync and the asset-embed scan run after a rename log the files they skip too, and the asset scan no longer stops silently at the first folder it cannot list.
  - An article directory that can be listed but not searched (read permission without execute) fails every scan with `article directory is not searchable`. NexWiki confirms this with a probe first, so a single entry whose stat is denied on its own (an SELinux label, a macOS ACL, a FUSE mount) is skipped and reported instead.
- **Scan Errors From `wiki_health` and `get_wiki_statistics` No Longer Expose Server Paths**:
  - When a scan fails outright, the error text either tool returns no longer contains the article directory's absolute path on the server.
- **Plan Lifecycle Worker Refuses to Delete Plans After an Incomplete Backlink Scan**:
  - The worker permanently deletes a long-archived plan only if no document links to it, but the backlink scan skipped files it could not read or parse, so a plan linked only from such a file looked unlinked. It now refuses whenever the scan skipped any file or folder, logs why and, outside dry-run mode, records a `delete-refused` activity event, as it already did for a plan that is still linked. Later sweeps check again.
- **Global Tag Deletion No Longer Blocks Every Write for the Whole Sweep**:
  - `DELETE /api/tags/{tag}` held the storage's write lock for the entire sweep — measured at about 63 ms per tagged document, about 200 s for 3,000 of them — so web edits, MCP writes, and the plan lifecycle worker all waited behind it, and an interrupted sweep left the tag removed from only some documents with no indication.
  - The sweep now takes the write lock per document: each document's rewrite stays atomic (re-read, save, history snapshot, and index update under one hold), but other writes interleave, waiting out one document rather than the whole corpus. A document edited mid-sweep is re-read when the sweep reaches it, so the edit and the sweep never write the same document at once.
  - The operation reports what it did: the REST response carries `rewritten`, `skipped`, and `failed` counts on the success and failure exits alike, a sweep stopped by a closing storage or a failed save logs what completed, and rerunning the deletion finishes the remainder — documents already rewritten are skipped, and deleting a tag nothing carries is a clean no-op.
  - The per-document announcements no longer come from a pre-scan of carrier versions, which an edit landing mid-sweep could make announce a document the sweep never rewrote; they come from the sweep's own record of what it saved.
- **Activity Archives Are Ordered Chronologically and Matched Exactly**:
  - Archives were ordered by reversing their file names, which placed every `activity.jsonl.<N>` fallback ahead of the timestamped archives and `.9` ahead of `.10`. Once a fallback existed, `NEXWIKI_ACTIVITY_MAX_ARCHIVES` pruned the newest archives, and `since`/`limit` reads and older-history paging stopped early and dropped events.
  - Archives are now ordered by the UTC time in their name, with fallbacks placed by modification time, and only names that exactly match the two archive forms are treated as archives, so an unrelated file such as `activity-backup.jsonl` is never pruned or read.
- **Custom Theme Saves Are Serialized and Atomic**:
  - The theme handlers loaded the custom theme list, changed it, and saved it in separate locked calls, so concurrent creates or deletes could each start from the same list and discard each other's changes. `ThemeStore.UpdateCustomThemes` now holds the store lock across load, modify, and save.
  - `custom_themes.json` is written with the atomic temp-file-and-rename path, so a crash mid-write cannot leave it truncated.
- **Client-Facing Errors No Longer Expose the Data Directory**:
  - MCP tools, MCP resources, and REST handlers returned storage and OS errors verbatim, exposing the server's absolute data directory. A shared sanitizer now rewrites paths under the data directory as relative paths — matching only at path boundaries, in every form the directory can be spelled — and is applied to every client-facing error that can carry a storage or OS error, including OKF import warnings. Server logs keep full paths.
  - `wiki_health` and `get_wiki_statistics` use the same sanitizer as every other tool (their scan errors are data-dir-relative), and OKF import warnings sanitize only the error half, so an entry name like `wiki/broken.md` survives when the data directory is relative.
- **Shutdown Drains the Worker, stdio Server, and Writes Before the Index Closes**:
  - Shutdown cancelled the plan lifecycle worker without waiting for it, never stopped the in-process stdio MCP server, and could close the search index while a save was still writing.
  - The HTTP server, the worker (which stops between plans), and the stdio server now stop in parallel within a writers deadline, then storage closes within the overall shutdown deadline: `Close` waits for in-flight writes and refuses later ones with `ErrStorageClosed` before they touch disk. Storage closes before the activity log, and `-mcp-only` handles `SIGINT`/`SIGTERM` the same way.
- **The Listener Binds Before Storage Opens, and Standalone `-mcp-only` Seeds the Guidelines**:
  - A launch that could not bind the web port had already created and seeded its data directory. The frontend loads and the listener binds before storage opens, so a bind failure exits without touching the data directory, and a signal received while storage opens is handled by closing it cleanly.
  - A standalone `-mcp-only` process now seeds the `nexwiki-agent-guidelines` skill, which it skipped before; seeding never overwrites an existing guidelines file that fails to load.
- **CORS Preflight Allows the MCP Protocol Headers**:
  - CORS preflight responses allowed only `Content-Type` and `Authorization`, so browsers blocked modern MCP requests from allowed origins that send `MCP-Protocol-Version`, `Mcp-Method`, and `Mcp-Name`.
  - The allowed request headers are now the explicit set the MCP endpoint reads — `Content-Type`, `Authorization`, `Accept`, `MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`, and `X-NexWiki-Client-Name` — and responses that do not echo an origin no longer send allow lists.
- **Sidecar and Bulk Activity Are Attributed and Announced Correctly**:
  - Tool calls through an `-mcp-only` sidecar lost the stdio client's name. The sidecar now forwards it in `X-NexWiki-Client-Name`, and attribution prefers the per-request `_meta` client info, then the transport's own identity (the stdio handshake or the forwarded header), then `-agent-name`/`NEXWIKI_AGENT_NAME`, then `AI Agent`.
  - Web OKF import and global tag deletion changed documents without logging or broadcasting anything; both now publish one activity event and one live update per changed document. MCP OKF import is no longer logged as a single read, verify revisions are attributed, and MCP reverts are logged as `revert` rather than `edit`.
  - The `get_recent_activity` `action` and `source` enums now cover every value that is logged (including `verify`, `delete-refused`, and `lifecycle`), and the Activity Drawer shows `verify`, `delete-refused`, and lifecycle events.
- **IPv6 Binds Work, and a Sidecar Finds a Specifically-Bound Web Server**:
  - `-bind` with an IPv6 address produced an invalid listen address and URLs. Listen addresses and URLs are now built with proper IPv6 handling (`::` binds all interfaces).
  - An `-mcp-only` sidecar only ever looked for the web server on `127.0.0.1`, missing one bound to a specific address such as a LAN IP or `::1`. It now probes its own `-bind` host — or `127.0.0.1` and `::1` concurrently for wildcard binds — and proxies to the host that answered, connecting directly rather than through `HTTP_PROXY`.
  - Signals received while storage opens are acknowledged at once, a second signal no longer kills the process, and the shutdown deadline counts from the signal.
- **Startup Auto-Delete Respects Backlinks and Leaves Plans to the Lifecycle Worker**:
  - With `NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS` set, startup permanently deleted every document archived past the delay — even ones other documents still link to — and stopped at the first failed delete.
  - The startup sweep now applies the same backlink guard the plan lifecycle worker uses (a document that is still linked, or that a scan which skipped unreadable entries or found misplaced linkers cannot show unlinked, is kept with a warning and checked at the next startup), leaves plans to the lifecycle worker, checks all due documents with a single directory walk, and keeps going past failed deletes without affecting startup.
- **Revert and Metadata-Only Saves Keep the Current Slug**:
  - Reverting an article across a title change restored the old title and renamed the article, contrary to the documented behavior. Revert now restores the snapshot's content and metadata but keeps the current title — and therefore the slug — documented in `docs/version_control.md`.
  - The same rule covers every metadata-only save: status changes, tag updates and global tag deletion, status migration, verify, OKF updates, and append/edit without a title all keep the slug; only an explicit title edit renames. Healing rewrites title-mismatched referrers in place, an unknown version answers `404` instead of `500` (REST and MCP), and a create over a misplaced file answers `409 Conflict`.
- **Symlinked Articles Are Fingerprinted by Target, and a Symlinked Article Root Is Resolved**:
  - Symlinked article files were fingerprinted by the link, so edits made through the link served stale metadata, backlinks, and search results, and a symlinked `articles` directory made the wiki appear empty.
  - Walks now stat the target only for entries that are symlinks, so regular files cost nothing extra, and every cache writer fingerprints the target; the article root is resolved once before walking, so the wiki lists, serves, and searches everything through the link. Dangling links are reported as unreadable once per target version, and links to directories are skipped.
  - The startup auto-delete backlink guard's walk resolves the root and stats targets the same way, so a linker reached through a symlinked file is seen, and through a symlinked corpus the guard keeps the linked archived article and deletes the unlinked one.
- **Day-Value Retention Settings Are Capped Instead of Overflowing**:
  - Day settings of `106752` or more overflow `time.Duration` when converted to a delay, wrapping negative and making auto-delete and the plan lifecycle remove everything immediately.
  - One shared day-to-duration helper now caps every conversion — plan lifecycle interval and sweep timers, archived-article retention, and the `wiki_health` `stale_days` and `cold_days` cutoffs — at a value meaning effectively never. Deletion compares strictly older-than, so a capped delay never comes due and a 0-day retention never deletes a document created now.
- **JSON-RPC ids Round-Trip Exactly, and Batches and Invalid Envelopes Answer `-32600`**:
  - The primary's JSON-RPC handling decoded ids into `interface{}`, so large integers came back rounded, batches were answered with a parse error, invalid requests lost their id, and `"id": null` was treated as a notification. Ids are now kept as raw JSON and echoed byte-exact through responses, errors, and subscriptions; batches, non-object requests, and a bare `null` answer `-32600` instead of `-32700`, which is reserved for malformed JSON; invalid requests keep their id when one is parseable; null, fractional, exponent, boolean, and composite ids are invalid rather than notifications; and member names match case-sensitively, so an id spelled with the wrong case is no id at all.
  - The `-mcp-only` sidecar agrees with the primary on envelopes: it uses the primary's own id validation, and a non-JSON-RPC answer from the web server (a `403` from host or origin validation, an HTML error page, an empty body) is answered on stdout as a JSON-RPC error under the request's id instead of a line the client cannot parse. A probe that finds a server answering without `200` logs why it is not used before falling back to standalone mode.
- **Loops Log the Documents They Skip Instead of Dropping Them Silently**:
  - Loops over listed articles that could not be opened skipped the document with no indication. OKF export, the skills and plans listings, the skills registry, status migration, the plan lifecycle worker, global tag deletion, and the tag-deletion announcement loop now warn once per file version, through one shared helper with the same shape as the unreadable-file warnings.
  - Status migration no longer writes its done marker when anything was skipped, so it retries on the next boot, and deleting a tag globally removes every case-insensitive variant of the tag instead of just the first match.
- **A Rename Writes the New Article Before Removing the Old File**:
  - A slug rename renamed the article file before the new front matter was durable, so a reader between the steps saw the new filename carrying the old slug and title. The completed new file is now written atomically first and the old file removed after, so a reader sees only the old state or the new state.
  - Startup sweeps leftover `.nexwiki` temp files from the data directory root, where custom theme saves can leave them.
- **Live-Subscriber Overflow Is Marked, and the Activity Dedup Is Version-Qualified**:
  - Live subscribers had a 100-slot buffer and silently dropped messages when it filled, so bulk operations reached open clients only in part. Buffers are larger now, and when one still overflows the backlog is collapsed into a marker the subscriber cannot miss: browsers reload their views and the activity drawer says the list may be incomplete, and MCP subscribers get a re-sync prompt through the notification types they asked for. The durable log is always complete.
  - The activity dedup window, which hid a second legitimate change to the same document within two seconds, is now qualified by the document's revision, so only true duplicates collapse and every revision is attributed.

## [0.19.0] — 2026-09-18

### Added
- **MCP Pagination on Every List Method**:
  - `tools/list`, `prompts/list`, `resources/list`, and `resources/templates/list` now accept a `cursor` and return a `nextCursor` while results remain, in both protocol eras.
  - `resources/list` used to project **every** document into one response. On stdio that response must fit on a single 8 MB line, so a large enough knowledge base stopped being listable at all.
  - Cursors are opaque; one this server did not issue — or one left over from a list that has since shrunk — is rejected with `-32602` telling the client to restart without a cursor. A finished list omits `nextCursor` rather than sending an empty one, which a conformant client would read as "there is more" and loop on.
- **MCP Argument Completion (`completion/complete`)**:
  - Completes the `{slug}` of the `nexwiki://article/{slug}` resource template, so a client discovers a page to `@`-mention instead of paging the whole resource list. Also completes prompt arguments: `title` from existing article titles, and `project` from project contexts already used by plans, so an agent reuses a project instead of coining a near-duplicate and splitting its plan history.
  - Prefix matches rank above substring matches and each band is sorted, so the list is stable and cacheable. Capped at the spec's 100 values with `total` and `hasMore` reporting the remainder.
  - An argument with nothing to suggest returns an empty list, not an error — a user typing into a free-text field should never see a failure. An unknown prompt name is `-32602`.
- **Live Subscriptions on stdio**:
  - `subscriptions/listen` now streams real notifications over stdio, not just an acknowledgment. The specification defines `io.modelcontextprotocol/subscriptionId` precisely because stdio multiplexes every subscription onto one channel; NexWiki serializes stdout behind a single lock and runs each subscription in its own goroutine.
  - `notifications/cancelled` naming the `subscriptions/listen` request id ends a stdio subscription, which is the only cancellation signal that transport has — there is no per-request stream to close.
- **Legacy `ping`**: answered again for `initialize`-based clients that use it as a liveness probe. It stays absent from the modern era, which removed it.
- **MCP Conformance Test Suite** (`server/mcp_conformance_test.go`): one assertion per normative requirement of the `2026-07-28` revision, each subtest named for the specification section it comes from, so a failure names the rule that broke rather than the symptom.

### Fixed
- **`subscriptions/listen` Never Answered Its Request on stdio**:
  - The acknowledgment was sent and nothing followed. The acknowledgment is a *notification* and carries no `id`, so the client waited on a response to the long-lived request that was never coming and only a timeout ended it. Every stdio exit path now writes the closure result.
- **CORS Preflight Rejected Modern Browser Clients**:
  - `Access-Control-Allow-Headers` omitted `Mcp-Method` and `Mcp-Name`, which the `2026-07-28` revision *requires* a client to send on every POST. A browser will not send a header the preflight did not allow, so browser-hosted modern clients were refused before a single JSON-RPC message was exchanged — the request never reached the handler that would have validated them. `Accept` and `Authorization` are allowed too; `Mcp-Session-Id` and `Last-Event-ID` remain for legacy clients.
  - The value is chosen in the `EnableCORS` middleware, by path. That middleware answers every preflight itself and returns before the mux runs, so headers set inside the MCP handler never reach a browser's `OPTIONS` request at all — the endpoint has to be recognized in the middleware or its requirements stay invisible. The REST API keeps its own narrower set.
- **A Modern Request With a Malformed Body Was Served as Legacy**:
  - Era detection looked only at `params._meta`. A request whose `MCP-Protocol-Version` header named `2026-07-28` but whose body omitted the mirrored `_meta` fell through to the legacy switch and got a plausible-looking legacy answer with no indication anything was wrong. The header is now an era signal in its own right, and such a request is rejected with `-32602` / HTTP 400 naming the missing field, as the specification requires.
- **Legacy Clients Were Advertised a Capability With No Method Behind It**:
  - Both eras shared one capability set, so `initialize` clients were told `resources.subscribe: true`. In those revisions that promises the `resources/subscribe` RPC — which the `2026-07-28` revision replaced with `subscriptions/listen` and NexWiki does not implement, so the call answered `-32601`. Capabilities are now declared per era: modern keeps `subscribe`, legacy does not.
  - The legacy standalone `GET` SSE stream carried nothing but keep-alives, leaving `listChanged` equally unbacked for those clients. It now delivers `notifications/resources/list_changed` when a document is created or deleted, which is the channel those revisions define for it.
- **`prompts/get` Reported an Unknown Prompt as a Missing Method**:
  - An unrecognized prompt name returned `-32601`, which the modern era is required to surface as HTTP `404` — so a typo'd prompt name made the MCP endpoint itself look like it had disappeared. It is now `-32602`, per the prompts specification, and missing required arguments are validated and named rather than silently interpolated as empty strings.
- **A Missing `Mcp-Name` Blamed the Header When the Body Was at Fault**:
  - A `tools/call` with no `params.name` returned `-32020 Missing required header: Mcp-Name`, sending the client to fix a header that had no value to carry. An empty body value is now `-32602`, and `-32020` is reserved for a genuine header/body disagreement.
- **CI Skipped Stacked Pull Requests Entirely**:
  - `.github/workflows/ci.yml` filtered `pull_request` to `branches: [main]`, so a PR opened against another feature branch — the normal shape when a fix is in review and follow-up work stacks on it — ran no tests at all. Only DCO reported.
  - The gap is silent in the worst way: a PR with no checks looks identical to one whose checks have not started, and the tests only appear once the base branch merges and GitHub retargets the PR. The filter is removed; every pull request is tested regardless of its base.
- **Subscription Results Omitted `serverInfo`**:
  - The acknowledgment and graceful-closure results were built by hand rather than through the shared result envelope, so they were the only modern results that identified no server.
- **Modern-Era MCP Results Were Missing Mandatory Caching Hints**:
  - The `2026-07-28` revision requires `ttlMs` and `cacheScope` on every `resultType: "complete"` result for `server/discover`, `tools/list`, `prompts/list`, `resources/list`, `resources/templates/list`, and `resources/read`. NexWiki returned `resultType: "complete"` without them.
  - Conformant clients validate these results against a schema in which both fields are required, so the omission rejected the **entire** response. Claude Code 2.1.273 negotiated `2026-07-28`, reported the server connected and healthy, and then exposed **zero** of the 29 tools — failing with `Invalid result for tools/list`.
  - Static results (`server/discover`, `tools/list`, `prompts/list`, `resources/templates/list`) now declare a 1-hour TTL with `cacheScope: "public"`; they are compiled into the binary and hold no user data.
  - Article results (`resources/list`, `resources/read`) declare a 30-second TTL with `cacheScope: "private"`, so one caller's cache entry is never reused across authorization contexts. Existing `listChanged` notifications still invalidate them immediately.
  - `tools/call` and `prompts/get` deliberately carry no hints — the spec does not list them as cacheable.
  - Legacy-era (`initialize`-based) results are unchanged and correctly carry neither field.
- **"Export as PDF" Appended the Live Activity Log to Every Article**:
  - The exported PDF carried the whole activity drawer — "Live Activity Log" and its entries — after the end of the article body.
  - Export as PDF is `window.print()`, so what reaches the PDF is decided entirely by the `@media print` block. That block flattens the app's layout containers by utility class (`.flex`, `.h-screen`, `.overflow-y-auto`) to stop them clipping the page, and it set `position: static` on everything it matched. The activity drawer uses those same utility classes, so the rule rewrote its `position: fixed` and dropped it into normal document flow. Because the drawer is mounted at all times and merely translated off-screen, it landed directly after the article on every single print.
  - Print now removes `fixed`-positioned elements outright. In this app `position: fixed` means viewport-anchored chrome — drawers, modals, backdrops, toasts, the editor's autocomplete popup — and nothing inside the article uses it. `display: none` rather than `visibility: hidden`: a flattened drawer keeps its full height, so hiding it any other way trades the activity log for a run of blank pages.
  - Export as Word and Export as Markdown were never affected. Word serializes the `.wiki-content` subtree and Markdown writes the stored source, so both were already scoped to the article.

## [0.18.0] — 2026-09-11

### Added
- **Copy as Rich Text in Share & Export Dropdown**:
  - Added "Copy as Rich Text" button in the article header dropdown, writing rendered HTML and plain text to the clipboard via the modern Clipboard API (`ClipboardItem`) with fallback to `document.execCommand`.
  - Enables seamless, styled pasting into WYSIWYG rich-text composers such as Microsoft Teams, Outlook, Slack, and Microsoft Word without displaying raw Markdown syntax.
  - Added dedicated companion user guide `docs/rich_text_sharing_guide.md`.

### Changed
- **Go Toolchain Upgraded to 1.27.1**:
  - Upgraded Go language version declaration in `go.mod` to `1.27.1`.
  - Updated Dockerfile multi-stage backend builder base image to `golang:1.27-alpine`.
  - Updated developer guides and contributing documentation to reflect Go 1.27+ prerequisites.
  - Bumped CI `golangci-lint` to `v2.13.2` for Go 1.27 compatibility.

## [0.17.1] — 2026-09-11

### Fixed
- **OKF Bundle Import and Export Path Resolution in MCP Tools**:
  - `export_okf_bundle` now always returns an absolute path via `filepath.Abs`, preventing relative path resolution errors across different working directories.
  - `import_okf_bundle` now accepts relative paths that already point within the wiki data directory without erroneously prefixing `DataDir` again.
  - Fixes `no such file or directory` errors during MCP bundle round-trips when running with relative data directories.

### Testing
- **Comprehensive MCP Tool Suite Coverage**:
  - Added full end-to-end test suite in `server/mcp_suite_test.go` exercising all 29 MCP server tools and explicit regression tests for relative data directory OKF export and import round-trips.

## [0.17.0] — 2026-09-11

### Added
- **Open Knowledge Format (OKF v0.2) Specification Support**:
  - Full native support for conformant OKF v0.2 concept documents with YAML front matter metadata, retaining transparent dual-era backward compatibility with OKF v0.1 documents.
  - Implemented the 5 OKF trust signals: provenance (`sources`), trust verification (`verified`), freshness (`stale_after`), lifecycle (`status`), and attested computations.
  - Added human verification tiers (`human-reviewed`, `machine-confirmed`, `unverified`) with prominent responsive reader badges and a one-click verification action (`POST /api/articles/{slug}/verify`).
  - Upgraded bidirectional OKF bundle import and export (`export_okf_bundle`, `import_okf_bundle`, `/api/okf/*`) to OKF v0.2.
  - Added `docs/okf_v02_guide.md` documentation manual.
- **Cross-Platform Release Install & Runner Scripts**:
  - Added `scripts/install.sh` (macOS/Linux) and `scripts/install.ps1` (Windows) to download and install the latest released binary with automatic SHA256 checksum verification.
  - Added `scripts/docker-run.sh` (macOS/Linux) and `scripts/docker-run.ps1` (Windows) to pull the latest multi-arch GHCR image and mount the OS-correct data directory.
  - Added companion uninstallers `scripts/uninstall.sh` and `scripts/uninstall.ps1` that remove the binary while leaving all user wiki data untouched.
  - Added `docs/install_scripts_guide.md`.
- **Browser Auto-Launch**:
  - Added `-launch-in-browser` CLI flag and `NEXWIKI_LAUNCH_BROWSER` environment variable to automatically open the wiki URL in the system default browser once the server is ready.
- **Documentation Hub Reorganization & 5 Dedicated Guides**:
  - Expanded `docs/README.md` and added 5 focused technical guides:
    - `docs/configuration.md`: Complete CLI flag, environment variable, precedence, and OS storage path reference.
    - `docs/docker_deployment.md`: Container runtime, persistent volumes (`/app/data`), and Docker Compose setup.
    - `docs/production_deployment.md`: Caddy and Nginx reverse proxy configs with mandatory unbuffered SSE/MCP stream directives.
    - `docs/developer_guide.md`: Developer workflow, local build commands, frontend HMR dev mode, and Makefile cross-compilation.
    - `docs/release_guide.md`: Pre-1.0 SemVer rules, CI release gating, changelog stamping, and release pipeline.
- **Visual Demonstrations**:
  - Embedded animated Web UI demo (`images/create_article_demo.gif`) and VHS terminal demo (`images/agent_mcp_demo.gif` with reproducible tape `assets/vhs/agent_mcp_demo.tape`) in `README.md`.

### Changed
- **Default HTTP Port Changed to 5808**:
  - Switched default server listening port from `8080` to `5808` across binary defaults, Docker entrypoints, and documentation.
- **OS-Aware Default Storage Directory for Native Executables**:
  - Native binary executions now resolve standard OS data directories: `~/.config/nexwiki/nexwiki-data` on macOS/Linux and `%AppData%\nexwiki\nexwiki-data` on Windows (with `./data` fallback), preserving existing setups without data loss.
- **Default Loopback Binding for Native Executables**:
  - Native executions default to binding `127.0.0.1` for local machine security, while containerized executions auto-detect containers and default to `0.0.0.0`.
- **README Redesign**:
  - Redesigned `README.md` into a concise, focused overview (~150 lines) centering NexWiki as the personal context consolidation repository and AI verification foundation.
  - Added copy-pasteable remote one-liners (`curl ... | bash` and `irm ... | iex`) for binary and Docker installations.

### Security
- **Filesystem Jailing for OKF Bundle Imports**:
  - Confined `import_okf_bundle` MCP tool path access strictly to the server's data directory (`srv.Storage.DataDir`), mitigating arbitrary filesystem reads and path traversal escapes.
- **Baseline Content Security Policy & Framing Restrictions**:
  - Enforced baseline `Content-Security-Policy` header (`default-src 'self'`, `frame-ancestors 'none'`) and updated `X-Frame-Options` to `DENY` across web application and API endpoints.

### Fixed
- **`read_article` MCP tool text body restoration**:
  - Restored Markdown body in `content[0].text` alongside `structuredContent.article.content` for standard text-reading MCP clients.

## [0.16.0] — 2026-09-07

### Added

- **Asset upload allowlist expanded to accept text and data files alongside images.** Articles often summarize data files (test round outputs, logs, CSV tables, metrics series); tools like `kimmydb-testkit` and human authors can now attach raw data directly to an article rather than leaving records stranded on remote hosts.
  - Newly supported MIME types and extensions:
    - `text/csv` (`.csv`)
    - `application/x-ndjson`, `application/jsonl`, `application/x-jsonlines` (`.jsonl`, `.ndjson`)
    - `application/json` (`.json`)
    - `text/plain` (`.txt`, `.log`)
    - `text/markdown`, `text/x-markdown` (`.md`)
  - The filename extension must match the declared MIME type, preserving strict pairing validation.
  - Web executables (`text/html`, `application/javascript`, `application/xhtml+xml`) remain strictly rejected.
  - Non-image assets (`.csv`, `.jsonl`, `.ndjson`, `.json`, `.txt`, `.log`, `.md`) are served with `Content-Disposition: attachment; filename="..."` alongside `X-Content-Type-Options: nosniff` to prevent active interpretation and avoid dumping megabytes of raw text inline. Active SVG documents retain their dedicated `Content-Security-Policy: default-src 'none'; sandbox`.

### Changed

- **The home page leads with search, and the browser tab carries an NX monogram.** The hero's search field is larger and no longer separated from the content below it by a rule, so the first thing the page offers is the thing most visits are for. The generic Vite mark in `frontend/public/favicon.svg` is replaced by a typographic NX favicon, which is also what the README now shows beside the project name.

## [0.15.1] — 2026-08-31

Two fixes to the article write path, both found while auditing the git-backed storage design against the code it will replace. Neither is new in 0.15.0; both are long-standing.

### Fixed

- **Renaming an article no longer breaks every image on it.** A slug rename moves `data/assets/<slug>/`, but nothing rewrote the `/api/assets/<slug>/<file>` URLs that point into it, so the rename succeeded, the page rendered, and every embedded picture 404'd.
  - `RewriteAssetPathLinks` heals the URL in all three forms it can be written in — `![alt](…)`, `[text](…)`, and an inline-HTML `src="…"` — rewriting only the slug segment and never the filename. Unlike article links, the image form is *included*: an article link had to distinguish navigation from an embedded picture, but an asset URL always names a file the rename moved.
  - The renamed document's **own** body is healed during the save itself, because that is the common case and it is the one place link healing could never reach — `healRenamedLinks` visits other documents.
  - Other documents are found by two scans, not one. `GetBacklinks` reports what *links* to the renamed page; an embedded image is not a link and earns no backlink, so a page that merely displays another page's diagram was invisible to it. `findAssetReferrers` finds those separately, and the healer works the union.

- **A document's version no longer resets when its history directory does.** The version counter was derived by counting snapshots in `data/history/<slug>/`, which made it a property of a local cache rather than of the document. Pruning the history directory, restoring from a partial backup, or importing a document without its snapshots silently restarted a long-lived article at version 1.
  - Optimistic locking went with it: a reset counter compares equal to a stale `loaded_version`, so a genuinely conflicting write was accepted as a clean one.
  - Front matter is now the source, which is what every consumer already treated as authoritative — `loaded_version`, the REST API, and the MCP output schemas all speak this integer. The directory scan survives as a fallback for documents written before the version field existed, and the state a save supersedes is re-archived when no snapshot of it is on disk, so the timeline the numbers promise can still be walked back.

## [0.15.0] — 2026-08-30

Completes the memory-enforcement work begun in 0.14.0. That release moved three memory-quality rules out of documentation and into the write path; this one finishes the remaining four workstreams, all of which move work an agent was asked to do onto the server that already had the answer.

### Added

- **`get_context_overview` lists `user` and `feedback` memories first.** These two kinds apply *regardless of the task an agent is about to start* — who it is working with, and the corrections that person has already given — while every other kind is only relevant once the task is known. This is the first call the server's own instructions tell an agent to make, so it is the right place for them.
  - It is an **ordering, not a separate pinned block**. A block above the index was the obvious shape and is worse: this tool exists to be cheap, and listing a memory twice spends context to say one thing. Because nothing is displaced or repeated, the pinned set cannot crowd out the index however large it grows, and needs no cap.
  - Each memory's kind renders inline as `<kind>`, before the tags — it is the field that decides whether a memory is worth reading for the task at hand, and an agent scanning the index reads left to right.
  - A wiki with no `user` or `feedback` memories says nothing about the ordering rather than explaining one that is not visible.

- **`create_agent_memory` checks for near-duplicates, and reports the outcome either way.** The same comparison `wiki_health` has always run, moved to write time — where it catches a duplicate while there is still only one document.
  - The larger point is *where the work happens*. The agent-facing rule was a retrieval chain performed before every write; an earlier plan specified four sequential lookups, and the memory rules later had to cap it at one because **the chain is livelock-shaped**. The server already owned the comparison.
  - **The negative outcome is reported too**, and that is what makes the one-lookup rule enforceable: an agent told *"compared against 6 memories in scope `nexwiki` — no near-duplicate; that check is complete"* has a completed check. A silent negative is indistinguishable from a check that never ran, and an agent that cannot tell will search again.
  - **Advisory, never blocking.** It is a *title* heuristic, and parallel-by-design documents legitimately share titles, so a false positive that refused a write would break real work while one that adds a sentence costs nothing. A warning names the sibling's slug and the measured overlap; a pair that already links to each other is suppressed.
  - The threshold and the suppression now live in one function shared with `wiki_health`, so the report and the gate cannot come to disagree about what counts as a duplicate.

- **`search_wiki` and `list_agent_memories` notice a repeated lookup.** When the same agent asks the same question twice inside two minutes, the result carries an escalating note: one line on the second, and on the third an explicit one naming §0 of the guidelines, stating the check is complete, and pointing at `create_*`.
  - **Rewordings are the point.** The fingerprint lowercases, strips punctuation, drops stop words, applies a crude stem, sorts the tokens and hashes — so *"docker build error"* and *"error building docker"* are the same question. An agent asking an identical question twice is easy to catch and is not the failure mode that happened: the 31-minute livelock on this wiki was rewordings.
  - **A successful write clears that agent's history**, because a write is progress and the loop being damped is read-only by nature.
  - Two deliberate limits. **The query text is never persisted** — a fingerprint lives in memory for 120 seconds and is dropped, because the activity log is durable, rendered and `SECURITY.md`-governed while query strings are free text. And **`structuredContent` is never touched**: it is a machine contract, and a client parsing it should not have to handle a field that is sometimes an essay.
  - State is per resolved agent and bounded (8 lookups each, 64 agents, least-recently-used eviction), entirely in memory. Nothing survives a restart, and losing it costs only a missed notice.

- **`wiki_health` reports `contested_memories`** — memories holding an unresolved conflict, so they surface where an agent already looks for what needs attention.

### Changed

- **⚠️ `edit_agent_memory` requires `change_intent` when it replaces `content`.** Optimistic locking protects against *concurrent* edits. It does nothing about an agent that has loaded the current version and knowingly replaces a fact with an incompatible one — that is a clean, successful, **silent** overwrite. Git history retains the old assertion, but history is not where anyone looks, and a contradiction nobody surfaces is a contradiction nobody resolves.
  | Intent | Meaning | Behaviour |
  |---|---|---|
  | `refine` | Clarifies without altering the claim | Normal replacement |
  | `correct` | The prior claim was wrong | Proceeds; **`edit_summary` becomes required** |
  | `contradict` | New evidence conflicts and you cannot adjudicate | **Content is not replaced** — the conflicting claim is appended as a dated `Contested` block and the memory is tagged `contested` |
  - **`correct` requires a summary** so the intent is not a checkbox: an agent that can declare `correct` and leave no record of what the prior claim was has performed the same silent overwrite with a label on it.
  - **`contradict` says plainly in the response that nothing was replaced.** Without that an agent assumes its edit landed and continues believing the memory says something it does not.
  - **Migration:** pass `change_intent` whenever you pass `content`. A **metadata-only edit needs none** — changing a tag, a description or a kind makes no claim about the fact. `append_agent_memory` is unaffected.
  - Named `change_intent` rather than `change_kind` deliberately: `memory_kind` already exists on this tool, and two arguments ending in `_kind` with unrelated closed vocabularies on the same call is a mistake worth making hard to express.
  - Two honest limits: `contested` is an **ordinary user tag**, not tool-managed, so an agent could strip it — if that is observed, promote it to a field. And **nothing detects an *undeclared* contradiction**; `change_intent` is self-reported, and real detection needs semantic comparison. This makes the honest path cheap and available; it does not make the dishonest one impossible.

- The MCP tool count is unchanged at **twenty-nine**. No tool was added or removed in this release.

## [0.14.0] — 2026-08-30

### Added

- **Agent memories gained a `memory_kind` axis alongside scope.** A memory answers two different questions and NexWiki could only store the answer to one. `memory_type` is *scope* — how far a fact reaches — and it is free-form, so it rides as the tool-managed `memory-<scope>` tag. Nothing recorded what **sort** of fact a memory holds.
  - The consequence was visible in the corpus: every memory was a technical fact about a system. The two categories entirely absent were **who the operator is** and **corrections the operator has given about how to work** — both of which existed, but in one MCP client's local memory directory, reachable by that client alone and invisible to every other agent on the server. The second brain was split in half, and the half about the person was the unshared one.
  - `memory_kind` is a closed four-value field: **`project`** (goals and constraints not derivable from the repo or its git history), **`reference`** (a pointer to an external resource), **`user`** (who the operator is), **`feedback`** (a correction the operator gave, plus why, plus how to apply it). It follows the rule 0.12.0 already learned with lifecycle status — **closed vocabularies are fields, open vocabularies are tags** — so kind is a field and scope stays a tag. The axes are independent and the full cross-product is legal.
  - **Filterable on both paths, composing on their own axes**: `list_agent_memories` takes `memory_kind` and `memory_type` together, and `search_wiki` takes `memory_kind` (which necessarily excludes every non-memory class, since nothing else carries the field). An unrecognized value is *reported* rather than answered with an empty list — an empty result reads as "no such knowledge", which is the wrong conclusion to hand an agent that typoed a filter, and is the same reasoning `search_wiki` already applies to unknown document types.
  - **`wiki_health` gained an `unkinded_memories` finding**, mirroring `unsourced_memories`. This is the whole migration strategy: new writes require a kind, existing memories stay valid and editable, and the backlog is *reported* rather than guessed at by a script. Nothing backfills — deciding `project` versus `reference` for an existing memory is a judgment call per memory. **The first run after upgrading will report every existing memory**, which is the expected output and the backfill worklist.
  - **Omitting `memory_kind` on edit preserves it**, exactly as omitting `status` preserves a plan's lifecycle state. Editing a body can never silently declassify a memory — an error invisible at the call site that would surface later as a memory kind-filtered recall can no longer find.
  - Carried through the **OKF bundle importer** and the **REST update path**, either of which would otherwise have been silent data loss: a bundle that dropped the axis would declassify the whole corpus on restore, and without the REST path the web editor could not classify at all. A kind arriving on a non-memory is stripped rather than stored.
  - **Frontend**: a badge beside the status badge on dashboard cards and in the article header, a **Kind** dropdown in the editor (memories only), and the filter bar matches kind alongside title, status and tags — which is what makes it a facet with no new UI, reusing the existing boolean grammar, so `feedback || user` finds everything known about the operator.
  - Internally, `saveArticleLocked`'s parameter list had reached ten and this would have made eleven, so the classification fields moved into an `ArticleOverrides` struct with omitted-means-preserve semantics. `SaveArticleWithStatus` keeps its signature and delegates.

- **🔐 Secret scanning on every agent write, which refuses rather than redacts.** NexWiki had no secret checking anywhere — `grep -niE "secret|redact|credential|api[_-]?key"` over `server/` returned nothing outside comments and tests. The agent-facing rules said a memory must never contain credentials, and nothing checked.
  - The exposure is specific to how this system stores things: a document is Markdown on disk, kept in compressed version history, indexed into Bleve, rendered in the web UI, and served to every connected MCP client. A key written once is then in the index, in the history, and in every agent's context on recall — there is no single place to delete it from. The only cheap moment is before the write.
  - **Refuse, not redact.** This inverts the usual disposition of an egress scanner on purpose. A scanner on a live output stream redacts because the turn has to continue; a *write* can safely fail, and the caller gets a recoverable error. A redacted document is worse than a refused one — it reads as complete, and the hole is invisible to every later reader.
  - **The refusal never quotes the match.** It reports pattern class, byte offset and length, and nothing else. A tool error is transcript content — sent to the model provider and persisted — so quoting the value to explain the refusal would reproduce the exact exposure the check prevents. Asserted by a test, including that no substring of the value appears either.
  - Covers `content`, `description` and `source` across all ten create/edit/append handlers for the four document types, **plus `import_okf_bundle`** — a bundle is a write path too, and leaving it out would be a one-call bypass. The importer refuses at *document* granularity and skips with a warning rather than aborting, so one bad document does not cost an operator the whole restore. On append only the appended text is scanned, which closes the intake without making a pre-existing document unappendable.
  - **High-signal issuer prefixes rather than entropy scoring** (AWS, GitHub, Slack, Anthropic, OpenAI-style, Google, PEM private keys, JWTs, assignment-shaped `key = value`). A wiki is full of hashes, slugs and long identifiers carrying no secret; an entropy threshold makes every one an argument, and a control people argue with gets turned off. A **placeholder allowlist** keeps documentation *about* credentials writable — `<your-token>`, `REDACTED`, AWS's own published example key — for the same reason.
  - **`NEXWIKI_SECRET_SCAN`** = `refuse` (default), `warn`, or `off`. An unrecognized value falls back to `refuse` with a warning on stderr, because a typo in the mode is exactly when failing open would matter most. There is deliberately **no per-document opt-out**: a flag an agent can set on the call being checked is not a control. REST and web-UI writes are out of scope (a human is exercising judgment), as is `revert_article_version` (restoring stored content is not intake). See [SECURITY.md](./SECURITY.md#secret-scanning-on-agent-writes) for the full limits.

### Changed

- **⚠️ `read_article` ships the article body in `structuredContent.article.content`, and no longer in the text block.** 0.13.0 removed the body from `structuredContent` to stop the Markdown crossing the wire twice, keeping the text block on the stated premise that it is *"the copy every MCP client renders, while structuredContent is optional and newer"*.
  - **That premise was false, and the resulting failure was total.** A client that reads the structured result of a tool declaring an `outputSchema` — which Claude Code does — received the metadata header and the backlinks and **no body at all**. It could not read an article, and so could not safely call `edit_wiki_article`, which replaces the whole body. The failure was also silent: the published schema and the payload agreed with each other perfectly, both omitting `content`, so every existing assertion passed against it.
  - Shipping the body **once** was the right goal; which copy survives was the error. Wire size stays at one copy — better than the duplication 0.13.0 removed, and better than 0.13.0 itself, which sent a body no structured client could reach.
  - **Migration:** read the body from `structuredContent.article.content`. A client that renders only the text block reaches it through the `nexwiki://article/<slug>` resource, which the metadata header now names. This departs deliberately from MCP's backwards-compatibility SHOULD, because honouring it means shipping the body twice; the reasoning is recorded at the call site so it is not "restored" without resolving the duplication that reintroduces.
  - **The text header now carries `Version`.** It never did, so a client reading only the text had no way to obtain `loaded_version` and could not complete the documented read-then-edit loop at all.
  - Guarded by `TestReadArticleBodyReachesAStructuredOnlyClient`, which reads the structured payload and discards the text block entirely — the one vantage point from which this defect is visible. Generic schema validation is structurally incapable of catching it. The two tests that passed against the broken behaviour were inverted rather than deleted.

- **⚠️ `create_agent_memory` now requires `memory_kind`, `description` and `source`.** All three are refused if absent, and `description` and `source` are refused if blank after trimming.
  - `description` and `source` were already documented as mandatory in the seeded guidelines, and `wiki_health` has always reported memories missing a source — a check on the wrong side of the write, since a fact whose origin was never recorded cannot have its origin *recovered* by a later report. The schema now agrees with the guidance, so the rule holds whether or not the agent loaded the guidelines. `description` is included because it powers `get_context_overview`, the first call the server's own instructions tell an agent to make: a memory without one is invisible at exactly the moment orientation happens.
  - Whitespace-only values are refused, so the write gate and the health check agree on what counts as present. Each rejection says **why** the field matters — an agent told only "this is required" learns to pass `"x"`, which satisfies the gate and defeats its purpose.
  - **Migration:** pass all three on creation. `edit_agent_memory` and `append_agent_memory` are unchanged and keep pointer semantics, so a caller fixing a body is never forced to restate provenance. **Existing memories are untouched** — this closes the intake, it does not rewrite history, and `unsourced_memories` keeps reporting what is already stored. Plans and skills are deliberately out of scope.
  - The two gates report in a stable, tested order (kind first, then provenance), and fixing the reported problem reveals the next one rather than dead-ending.

- The MCP tool count is unchanged at **twenty-nine**. No tool was added or removed in this release.

## [0.13.0] — 2026-08-26

### Added

- **The home dashboard uses the display.** 0.12.0 gave the article reading column a responsive ladder but left the dashboard behind: it stayed pinned at `max-w-4xl` (896px) with a card grid that stopped at two columns from 768px upward. A wide monitor rendered a narrow ribbon of cards with dead space either side of it, and far more scrolling than the content needed. The dashboard is a card grid rather than prose, so it is not bound by the reading measure that caps an article at 1024px — it now runs deliberately **wider than an article**, to 1536px, with the card columns scaling on the same breakpoints: three from 1280px, four from 1536px. The full-text search bar stays centered at its original width, and the quick-action cards already filled the wider container.
  - Also fixes a latent bug the wider grid would have exposed: each section's empty-state panel spanned a hardcoded two columns, so it would have sat short of the row at three or four. It now spans the full row at any width.

### Changed

- **⚠️ `read_article` no longer repeats the article body in `structuredContent`.** The tool returned the full Markdown **twice** in a single response — once as prose in `content[0].text`, and again as `structuredContent.article.content` — so every read crossed the wire at roughly twice the article's size. That halved the effective ceiling on how large an article an agent could read in one call: MCP clients cap tool-result size, and on exceeding it they truncate what the model sees and spill the full payload to a file for the agent to dig back out. A 63 KB article tripped that cap at about half the size it should have, and the cost was legibility, not just bytes.
  - The body now ships **once, in the text block** — the copy every MCP client renders, where `structuredContent` is optional and newer. The published `outputSchema` drops its `content` property to match, since advertising a field that is never sent is the schema drift that makes a published schema worse than none.
  - **Migration:** an agent or client reading the body from `structuredContent.article.content` must read `content[0].text` instead. Every other structured field is unchanged — `version` above all — so the documented read-then-edit loop through `loaded_version` is unaffected. No other tool changed, and the tool count stays 29.
  - Guarded by `TestStructuredOutputCarriesRealData`, which previously asserted the body *was* in the structured payload — that assertion was the duplication. It now pins the property from both sides: absent from `structuredContent`, present in the text block.

## [0.12.3] — 2026-08-23

### Fixed

- **The startup index-lock deadline covered far more than the lock, and killed a migration mid-run.** `main.go` wrapped the whole of `NewStorage` — index open, home seeding, the one-time status migration, and the boot index sync — in a 15-second timeout that exists solely to bound Bleve's exclusive bbolt lock. On a Synology NAS the 0.12.0 status-field migration ran past that budget and the process was killed with *"could not open the search index … another process is holding it open"*, blaming a lock conflict that had not occurred. It recovered only because the migration is idempotent, its completion marker is written last, and the stack had `restart: unless-stopped`; a stack without a restart policy would have been left down after a partial migration.
  - The deadline now lives inside `NewStorage` and covers **only** the Bleve open (`server.IndexOpenTimeout`, `server.ErrSearchIndexLocked`). Everything after it takes as long as the corpus requires.
  - Guarded by `server/index_open_timeout_test.go`: a contended index still reports `ErrSearchIndexLocked` promptly, and a boot whose migration has real work to do completes it — asserting every seeded plan was migrated and the marker written.

## [0.12.2] — 2026-08-23

### Fixed

- **The dashboard and sidebar showed no documents at all — every article, memory, plan, and skill was hidden.** 0.12.0 began hiding archived documents from listings, and the check read `archived_at` as a boolean. `encoding/json`'s `omitempty` does **not** omit a zero-valued struct, so every unarchived document serialized `"archived_at": "0001-01-01T00:00:00Z"` — a string that is truthy in JavaScript. The browser concluded every document was archived and rendered none of them, while each section's count kept reporting the real total, because counts come from the unfiltered list. Search was unaffected, since it filters server-side where Go compares with `.IsZero()`.
  - `Article.ArchivedAt` and `Article.StatusChangedAt` now use **`omitzero`** (Go 1.24+), which does drop a zero `time.Time`. The API no longer emits either key for a document that has neither.
  - The frontend no longer trusts a timestamp's truthiness: `isRealTimestamp()` rejects Go's zero value, so the UI is correct even against an older server. The plan header's "status since …" carried the same latent trap and uses it too.
  - Guarded at both layers: `server/article_json_test.go` asserts the payload omits zero timestamps and keeps real ones, and `Hero.test.tsx` renders documents stamped with the zero value. Both were verified to fail against the broken code.

## [0.12.1] — 2026-08-23

### Fixed

- **Three shipped instruction texts still told agents to put lifecycle status in a tag — which the same release made a rejected write.** 0.12.0 moved status from a tag to the `status` field and began rejecting a status word in a plan's or skill's `tags`, but the guidelines seeded into a **fresh wiki** still said to "add the `completed` tag with `edit_agent_plan`", the `project_planning_workflow` MCP prompt still said to "mark the plan as completed by adding the 'completed' status tag", and the seeded tag rules still pointed at the retired `wip` vocabulary. An agent following any of them to the letter got its next call rejected. Also corrects two descriptive strings: the `GET /api/status-tags` payload described status as belonging to "a wiki article or collaborative AI plan" (wiki articles have no status), and `list_agent_plans` advertised that "lifecycle state lives in the status tags".
  - Guarded by `server/agent_instructions_test.go`, which fails if any shipped instruction text teaches the tag form, if the seeded guidelines stop naming the field, or if a prompt is added without being covered. Verified against the 0.12.0 text: it flags all three.

## [0.12.0] — 2026-08-23

### Added

- **Lifecycle status is now a first-class `status` field, with enforced vocabularies for agent plans and agent skills.** Status used to be a tag, and that was the source of a long tail of awkwardness: a status is a single value with a state machine, while tags are an unordered folksonomy, so storing one inside the other forced "exactly one" counting, precedence tables to collapse duplicates, and a denylist to catch an agent writing `wip` when it meant `implementing`. A dedicated field makes the invalid states unrepresentable rather than merely detectable.
  - **Agent plans** have a closed vocabulary of eight states — `draft`, `implementing`, `blocked`, `completed`, `superseded`, `parked`, `evergreen`, `archived` — and always have exactly one. **Agent skills** have their own — `draft`, `ready`, `archived` — and may have none. An unrecognized value is rejected with a message naming the right one, so an agent can neither invent `in-flight` nor borrow another class's word. **Wiki articles and agent memories have no status at all**: no field, no editor control, and no tag rules — tag them however is useful.
  - **A lifecycle word used as a *tag* on a plan or skill is rejected**, because a plan tagged `completed` whose field says `implementing` is two contradictory sources of truth. Project-context tags and topics are untouched on every type.
  - **An ordinary edit preserves state.** `status` is omitted-means-preserve on every write path, so editing a plan's body, renaming it, or replacing its tags can never silently reset a `completed` plan to `draft`. `SaveArticle` deliberately takes no status at all — a state transition is not a content edit.
  - **`status_changed_at`** joins the OKF front matter, stamped only when the status actually changes. The lifecycle timers run off it, never the article timestamp, so fixing a typo in a completed plan cannot restart its archive clock. The plan header shows it ("completed since 2 months ago"), making an approaching auto-archive visible rather than a surprise. Absent means *not yet eligible*, never "infinitely old".
  - **A background worker automates the tail of the plan lifecycle** (web primary only — never under `-mcp-only`, where a sidecar must not mutate a directory the primary owns): `completed`/`superseded` → `archived` after `NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS` (default 90), `archived` → permanently deleted after `NEXWIKI_PLAN_DELETE_AFTER_DAYS` (default 365), sweeping once at startup and then every `NEXWIKI_PLAN_LIFECYCLE_INTERVAL_DAYS`. `parked` and `evergreen` never move — that exemption is their purpose. Because the deletion stage has no human in the loop, it carries three guards: `NEXWIKI_PLAN_LIFECYCLE_DRY_RUN` logs intended transitions without applying them, every transition lands in the durable activity log and over SSE, and a **backlink guard refuses to auto-delete any plan other documents still link to**, reporting the refusal instead.
  - **A one-time startup migration** (marker-gated, never re-runs) takes status out of tags: plans remap legacy words (`wip`/`in-progress`/`active` → `implementing`, `done` → `completed`, `todo`/`ready` → `draft`), collapse multi-status cases by terminal-state precedence, and stamp `status_changed_at` with the migration date — **not** the article timestamp, which would put a mature wiki's months-old completed plans on an immediate archive countdown. Skills remap onto their own three. Wiki articles and memories have no status to move it into, so a retired status tag (`ready`, `draft`, `wip`, `done`, …) is simply **removed** and nothing replaces it; every other tag survives untouched, including `archived` (the archival mechanism) and `inbox` (a raw capture awaiting compilation).
  - **New MCP arguments, no new tools** (the count stays 29): `status` on `create_agent_plan`, `edit_agent_plan`, `create_agent_skill`, and `edit_agent_skill`; a `status` filter on `list_agent_plans`; and `get_status_tags` now returns `plan_status_tags` and `skill_status_tags` as separate groups (`status_tags` remains their union). REST `POST`/`PUT /api/articles` accept `status` with the same omit-means-preserve semantics.
  - **Frontend**: a Status dropdown sits beside the Tags row in the editor when editing a plan or a skill (the two types that have one), and status renders as a colored badge on dashboard cards and in the article header. The Agent Plans dashboard default becomes the inclusion list `draft || implementing || blocked`, which keeps working because filters match the status field alongside title and tags. Archived documents are hidden from the dashboard sections and the sidebar by default; typing `archived` in a filter reveals them, and direct URLs always work. Hiding lives in the UI layer, not `ListArticles`, so wikilinks to archived pages keep rendering as valid links.
  - **Carrying a status tag is the migration's only trigger.** A plan that never had one is not rewritten at boot — that cost a write, a history entry, and a reindex per document (201 writes and ~1s of startup on a 2,000-document corpus, enough to break the boot-time budget). It is defaulted to `draft` the first time anything writes it, and the lifecycle worker's first sweep — already running in the background after startup — backfills the rest.
  - Reviving a plan or skill out of `archived` now clears `archived_at`, closing a one-way door: the stamp was written once and never cleared, so a document revived out of archival stayed hidden from search and on the deletion clock forever.

- **Mermaid diagrams render natively.** A ```` ```mermaid ```` fenced code block renders as an SVG diagram in the article viewer, the editor's live preview (debounced so typing stays smooth), and print/PDF exports — previously it displayed as a plain highlighted code dump, which mattered because the plan-authoring convention puts a diagram in nearly every agent plan. Diagrams follow the active light/dark theme and re-render on toggle; wide diagrams scroll in their own container on screen and shrink to page width in print; a diagram with a syntax error falls back to its source with an inline error note, never a blank hole. The ~800KB library is lazy-loaded via dynamic `import()` only on pages that actually contain a diagram — the initial bundle is unchanged.
- **The reading column uses the display.** The article body, loading skeleton, not-found fallback, and search results were all pinned at `max-w-2xl` (672px) — correct on a laptop, a ribbon in whitespace on a 4K display. They now share a responsive ladder: 672px below 1024px viewports, 768px from 1024px, 1024px from 1536px. A cap is kept deliberately; unbounded line length reads worse than a narrow column.
- **The Agent Plans dashboard section defaults its filter to the open work.** Most plans in a long-lived wiki are finished; the useful default view is what is still moving. Under the eight-state lifecycle the default is the inclusion list `draft || implementing || blocked` — typed into the filter box itself, so it is visible and clearable like any filter the user wrote.
- **Back-navigation restores the home dashboard.** Opening an article and pressing Back previously discarded every filter, expanded section, and the scroll position. The dashboard now saves its UI state to per-tab `sessionStorage` on unmount and restores it on back/forward navigation and reloads. Deliberately navigating Home still gives a clean dashboard.

### Changed

- **⚠️ Lifecycle status moved out of the tag list.** On first boot, plans and skills have their status tags migrated into the `status` field, and wiki articles and memories simply **lose** the retired status tags (`ready`, `draft`, `wip`, `done`, …) — those types have no status, and their remaining tags, `archived` and `inbox` included, are untouched. Afterwards, plans and skills reject lifecycle words as tags; wiki articles and memories are never policed and may use any tag, including those words. Skills keep the `draft` → `ready` promotion convention, now expressed through the field.
- **⚠️ Archived documents are hidden by default everywhere, not just in search.** The dashboard sections and the sidebar now default-exclude archived documents — necessary once plans archive themselves on a timer, since a plan that vanished from search but lingered in listings had no visible explanation. Typing `archived` in any filter box reveals them, and direct URLs always work.
- **⚠️ Behavior change: filter suggestion dropdowns are navigated with `↓` / `↑`, not `Tab`.** Every filter box (dashboard sections, sidebar, activity log) and the editor's tag input previously cycled suggestions with `Tab` / `Shift+Tab` and swallowed the key — so the arrows every keyboard user reaches for in a combobox did nothing, and `Tab` could not move focus out of the input. The arrow keys now own suggestion navigation (opening a closed dropdown, wrapping through the input at each end, scrolling the highlight into view); `Tab` is an ordinary focus-movement key again and closes the dropdown on the way out without accepting the highlighted suggestion — `Enter` remains the one commit key. The controls also gained full combobox ARIA (`role="combobox"`, `aria-activedescendant`, `listbox`/`option` roles), so the keyboard interaction is announced to screen readers.

### Fixed

- **`search_wiki(tags: ["archived"])` returned zero results.** The archived filter ran before the tag filter and discarded every hit the caller was explicitly asking for. An explicit `archived` entry in the tags facet now implies `include_archived`, mirroring the browser's query-text heuristic.
- **`docs/tags.md` claimed archived articles "remain fully visible and searchable."** False since the `SearchOptions` refactor — archived documents are excluded from default search (and `server/archived_tag_test.go` asserts it). The doc now describes the real visibility rules.

## [0.11.1] — 2026-08-19

### Fixed

- **A version conflict told the client to re-read without saying what to send, which is an unbounded retry.** All five optimistic-locking messages ended in "re-fetch and try again". That names no value and sets no bound, so a client that mis-threads `loaded_version` re-reads, retries, and can mis-thread again — the same unbounded-precondition shape as the orientation cycle fixed in 0.11.0, which had an agent alternating `read_article` and `search_wiki` for 31 minutes. The server already knows the version on disk at the moment it rejects the call, so it now says exactly what to send: *"The article is at version 15 on disk; you sent loaded_version 14. Retry once with loaded_version: 15. Re-read only if you need the current content before overwriting it — sending 14 again will fail identically."* Three properties carry it: name the value, say **once**, and foreclose the identical retry.
  - `update_article_tags` was the worst of the five and had no test coverage. `UpdateArticleTags` returned a bare `fmt.Errorf` that surfaced verbatim as `Error updating tags: version conflict: loaded version 1, current version 2` — no instruction at all. It now wraps the existing `ErrVersionConflict` sentinel, and the tool maps it with `errors.Is` like the article-edit path already did.
  - The single exception is `edit_wiki_article` when the article cannot be read back after the conflict. There is genuinely no version to name, so that message still says to re-read — the one case where it is the honest instruction rather than a reflex.
- **The `nexwiki-agent-guidelines` document shipped to agents is 28% smaller** (11,733 → 8,457 characters). §2–§4 fell from **44% of the mandatory read to 20%**, with the detail moved to the existing `create-plan-skill` and a new lean `agent-memory-rules` skill, loaded only when actually doing plan or memory work. Section numbers are unchanged on purpose: `server/links.go` cites §5 by number. This is a live-wiki change, not a code one; `defaultAgentGuidelines` in `server/guidelines.go` was already lean and is untouched.

## [0.11.0] — 2026-08-19

### Added

- **`wiki_health` reports skills nothing references** — a seventh check, no new tool, so the count stays at 29. A skill is not reached the way an article is: articles are *linked*, skills are *invoked*, by a `read_article(slug: "…")` call written into another document's prose or a backticked slug reference. None of that is a link, so the link graph never saw it. Measured on a real corpus: `nexwiki-agent-core-guidelines` named `enhanced-memory-decision-making-skill` **four times** in exactly those forms and `get_backlinks` still returned **0**, while `create-plan-skill` — live and wanted — also reports 0 inbound links. An unreferenced-skill check built on the link graph alone would therefore have flagged the healthy skill and stayed silent on the dead one. The check counts links **and** in-code slug mentions, the latter tracked separately from `InboundCount` so `get_backlinks` and orphan detection keep their existing meaning.
  - **A reference from an archived document does not count.** A skill whose only mention lives in a retired document is as unreachable as one with no mention at all — and that is exactly how the dead skill was found, since it looked referenced right until the document naming it was archived.
  - `nexwiki-agent-guidelines` is exempt: three tool descriptions name its slug in Go, so no document has to.
  - Deliberately not extended to memories or plans, which are reached through their own list tools and are meant to be link-less. Orphan detection already excludes them for the same reason — scanning every type once produced 70 findings on an 83-document corpus, 27 of them agent documents behaving as designed.
  - Extraction rides on the body read `ScanLinkGraph` already performs and caches by mtime, so the scan still costs one stat per unchanged file. Collecting the mentions costs `ScanLinkGraph` **~10%** on the large-corpus benchmarks (10,000 documents: 127 ms → 139 ms), from retaining the per-document slices; `get_backlinks` and `get_wiki_statistics` share that scan and pay it without reading the field. One scan was preferred over two.

### Changed

- **CI resolves the Go toolchain from `go.mod` instead of a floating minor version.** All five `actions/setup-go` blocks across `ci.yml` and `release.yml` said `go-version: "1.26"`, which is supposed to float to the newest patch. It does not do so reliably: with `GO-2026-6218` (`net/url`) and `GO-2026-6090` (`crypto/tls`) fixed in **1.26.6**, three PRs opened within minutes of each other resolved differently — two got `go1.26.6` and passed `govulncheck`, one got `go1.26.5` and failed, and stayed on 1.26.5 across a rerun, a `--failed` rerun, and a fresh close/reopen run. Whether a PR went green was decided by which runner picked up the job. `go-version-file: "go.mod"` makes it deterministic and collapses six duplicated version strings into one.
  - `go.mod` now declares `go 1.26.6`, which **raises the minimum Go for building from source**. That is the point: the version is a build requirement, not a suggestion.
  - The Dockerfile deliberately keeps `golang:1.26-alpine`. It cannot read `go.mod` for a base image, and pinning it would reintroduce the drift this removes — Docker Hub's minor tag does track the newest patch reliably, which is exactly what the `setup-go` manifest failed to do.

### Fixed

- **The orientation prerequisites formed a cycle with no exit, and agents livelocked in it.** Three tool descriptions told agents they must *ALWAYS* load `nexwiki-agent-guidelines` before executing, and the seeded guidelines told them to search for a style guide before writing — so intent to create led to re-reading the guidelines, which led to searching, which led back to intent to create. Nothing in either half said "you have already done this." Observed on a live wiki: an agent wrote its article successfully at 20:39 UTC, never registered the success, and then spent **31 minutes and ~170 tool calls alternating between `read_article` and `search_wiki`** before it was killed. Each lap appended another copy of the guidelines — measured at 11,733 characters, roughly 3,000 tokens — to its context until the original request was evicted, at which point the only instructions left were the ones telling it to orient. Frontier models escape by tracking satisfied preconditions; smaller local models do not.
  - The `create_wiki_article`, `create_agent_memory`, and `create_agent_plan` descriptions now scope the prerequisite to **once per session**, and say explicitly not to re-read the guidelines if they are already in context.
  - The seeded guidelines gained a **§0 "Orientation runs once, then you write"**: each orientation call runs once per question, a search returning nothing is a *completed* check rather than a failed one, and when the checks are done the next action is a `create_*` call. §2 gained the matching exit clause for the case no template covers the subject — which is what triggered this, since the wiki's only format templates were for programming languages and SQL dialects and the article was about neither.
  - Existing wikis keep their own guidelines page untouched, as seeding has always been skip-if-present. Add the §0 rules by hand to get the fix.
- **`create_wiki_article` now states what `title` is for.** The same run passed `title: "create"` — the tool's own verb — and the article was stored at `/articles/create` with an otherwise complete body. The description now says to use the subject's human-readable name and never a tool name, verb, or placeholder, since the slug is derived from it.
- **A `memory-<scope>` tag on a non-memory document could never be removed.** `validateAndCleanUserTags` re-asserted every existing memory-scope tag before applying the caller's replacement set, so the tag survived any edit — and `DeleteTagGlobally` refuses memory-scope tags outright, which closed the last remaining path. The invariant is right for an `AI-Agent-Memory`, where `create_agent_memory` derives the tag from `memory_type` and dropping it would orphan the memory from its scope. On any other class the tag is stray data that no tool sets, and making it permanent was never intended. Found while trying to clean a stray `memory-rules` tag off a superseded `AI-Agent-Skill`: the tag made that document the **top hit for `search_wiki(query="style guide")`**, ahead of the actual article format templates, which is what misdirected the agent in the livelock described below. Preservation is now scoped to `AI-Agent-Memory`; forging a scope tag onto any class is still refused.
- **A malformed tool call could store an article under the tool's own verb.** `create_wiki_article` was called with `title: "create"` and an otherwise complete 2,500-word body, and NexWiki saved it at `/articles/create`. The fault is client-side serialization — a local model served through LM Studio — but accepting it silently converts a recoverable client bug into wrong data under a slug derived from the bad title. The four `create_*` tools now refuse a title that is a bare tool verb (`create`, `edit`, `get`, …) and return an error naming the likely cause. The check is deliberately narrow: an exact match against a bare verb, nothing heuristic. `Go`, `C`, and `Zig` are all legitimate titles here, and `read_write_lock` is a plausible one, so neither short titles nor snake_case titles led by a verb are touched.

## [0.10.0] — 2026-08-10

### Added

- **`edit_agent_skill` — the 29th tool.** `create_agent_skill` and `list_agent_skills` shipped without an edit counterpart, so revising a skill meant reaching for `edit_wiki_article`, which applies none of the type guarding the memory and plan tools do. It mattered most for `nexwiki-agent-guidelines`: the governance document every agent loads is itself a skill, so the one document designed to be revised had no first-class edit path.
- **`edit_agent_plan` accepts `description` and `source`.** `create_agent_plan` always did; edit did not, so a plan's one-line summary could be set once at creation and never corrected. On a real wiki that left most plans with an empty description, showing as a bare title in `get_context_overview`. Both use the same pointer semantics as `edit_agent_memory` — omit to preserve, empty string to clear.
- **`MDLINK_BROKEN` editor diagnostic** — the linter now underlines absolute `[text](/articles/slug)` links whose target does not exist, alongside the existing `WIKILINK_BROKEN`. Both link rules now skip fenced code blocks, matching the server-side scanner, so a documented `[Title](/articles/slug)` example is not reported as a link.
- **`broken_links[].form`** on `wiki_health` and `get_wiki_statistics` — `"wikilink"` or `"markdown"`. Reports print the link in the syntax it was written in, because an agent told to fix `[[rust]]` in a file that actually says `[Rust](/articles/rust)` will not find it.

### Fixed

- **The link graph ignored the link form the house style prefers.** `ScanLinkGraph` read only `[[WikiLinks]]`, but the corpus is overwhelmingly written in absolute `[text](/articles/slug)` Markdown links — and `nexwiki-agent-guidelines` §5 tells every connected agent to write them that way. Measured on an 84-document wiki: **293 Markdown links against 57 WikiLinks, and the home page's 48 internal links were 100% invisible.** Three consumers were wrong as a result, and all three are fixed by one scanner change:
  - `wiki_health` reported **0 broken links against 26 real ones** (14 of them pointing at a `/articles/rust` that had been renamed).
  - `wiki_health` called **44 of 84 documents orphans (52%)**; the true number is 2. §6.5 had already tuned this check once after it fired on 84% of the wiki, and the remaining cause was that the majority link form was never counted.
  - `get_backlinks` — which the guidelines tell agents to run *before* a rename or delete — under-reported inbound references, the dangerous direction for that use.
- **Renaming an article now heals inbound Markdown links too**, not just `[[WikiLinks]]`. Only the destination is rewritten (`](/articles/old)` → `](/articles/new)`); the link text is left exactly as the author wrote it, the same guarantee `[[old|display]]` already had. Unhealed Markdown links are presumably how `/articles/rust` came to be broken in 14 places.
- **The viewer opened the wiki's own pages in a new tab.** `Viewer.tsx` had a branch for `wikilink:` URLs and a fallback for external links, and nothing in between, so an absolute `/articles/<slug>` link rendered with `target="_blank"` and full-reloaded the app. Both internal forms now navigate in place, and both render the same red-dotted create prompt when the target does not exist.
- **The editor's quick-fix action pasted prose into the article.** `LintDiagnostic.suggestion` carried two different things — replacement text for rules like `MD034`, and human guidance for a broken WikiLink — and neither quick-fix path (the CodeMirror lint action or the right-click menu) could tell them apart. Both inserted it verbatim, so choosing **Fix** on a broken `[[Foo]]` replaced it with the sentence *"Click to create this page."* The field is now split into `fix` (replacement text, the only thing a quick-fix will insert) and `hint` (displayed, never inserted), which makes the mistake unrepresentable rather than merely documented. A test asserts every `fix` a document produces is Markdown rather than prose.
- **A stale `loaded_version` of `0` silently bypassed optimistic locking.** `ApplyArticleEdit` skipped the version check whenever the *caller's* version was 0, not just when the stored one was, so a client that omitted the field overwrote a versioned article with no conflict raised. The check now keys only on the stored version: 0 on disk means the file predates versioning and anything is accepted, but once an article has a version the caller must supply the matching one.
- **Unversioned legacy articles could not be edited through MCP at all.** `Article.Version` carried `omitempty`, so `read_article` dropped the field entirely for files written before versioning existed — and `edit_wiki_article` demanded a *positive* `loaded_version`. The documented "feed `read_article`'s version straight back" loop therefore dead-ended on exactly those articles. `version` is now always reported, including `0`, and `0` is accepted for an article that has no version.

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

[Unreleased]: https://github.com/gruberchris/nexwiki/compare/v0.22.0...HEAD
[0.22.0]: https://github.com/gruberchris/nexwiki/compare/v0.21.0...v0.22.0
[0.21.0]: https://github.com/gruberchris/nexwiki/compare/v0.20.0...v0.21.0
[0.20.0]: https://github.com/gruberchris/nexwiki/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/gruberchris/nexwiki/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/gruberchris/nexwiki/compare/v0.17.1...v0.18.0
[0.17.1]: https://github.com/gruberchris/nexwiki/compare/v0.17.0...v0.17.1
[0.17.0]: https://github.com/gruberchris/nexwiki/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/gruberchris/nexwiki/compare/v0.15.1...v0.16.0
[0.15.1]: https://github.com/gruberchris/nexwiki/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/gruberchris/nexwiki/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/gruberchris/nexwiki/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/gruberchris/nexwiki/compare/v0.12.3...v0.13.0
[0.12.3]: https://github.com/gruberchris/nexwiki/compare/v0.12.2...v0.12.3
[0.12.2]: https://github.com/gruberchris/nexwiki/compare/v0.12.1...v0.12.2
[0.12.1]: https://github.com/gruberchris/nexwiki/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/gruberchris/nexwiki/compare/v0.11.1...v0.12.0
[0.11.1]: https://github.com/gruberchris/nexwiki/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/gruberchris/nexwiki/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/gruberchris/nexwiki/compare/v0.9.0...v0.10.0
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
