# Changelog

All notable changes to NexWiki are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and NexWiki adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). NexWiki is pre-1.0: breaking changes may land in minor releases, and are always called out below.

## [Unreleased]

### Added
- **MCP tool annotations.** All 27 tools now declare `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`, and a human-readable `title`, so clients can auto-approve safe reads rather than prompting for every call. `openWorldHint` is `false` on every tool — NexWiki never reaches outside the local wiki.
- **MCP protocol revision `2026-07-28` (dual-era).** NexWiki now serves the current per-request-metadata revision *and* older `initialize`-based clients on the same endpoint, choosing per request. Includes `server/discover`, `resultType` result envelopes, `MCP-Protocol-Version` / `Mcp-Method` / `Mcp-Name` header validation, and the `-32020` / `-32022` error codes.
- **`search_wiki` facets:** `type`, `tags`, `limit`, and `include_archived` arguments.
- **`-bind` flag / `NEXWIKI_BIND`** to restrict the listening interface (e.g. `127.0.0.1`). Defaults to all interfaces so Docker deployments keep working.
- `X-Accel-Buffering: no` on SSE responses, so reverse proxies stop buffering the stream.

### Changed
- **`search_wiki` now searches every document type by default.** Previously memories, plans, and skills were hidden unless the *query text* contained the words "memory", "plan", or "skill" — so a memory about Elasticsearch was invisible to a search for "elasticsearch". Human/browser search is unchanged and still hides agent documents.
- Article metadata and the WikiLink graph are cached, validated by file modification time so edits made outside NexWiki are still picked up. `ListArticles` is ~7.8× faster and `GetBacklinks` ~17.6× faster on a 200-article wiki.
- MCP tools moved to a registry that pairs each schema with its handler, so the two cannot drift apart. `server/mcp.go` dropped from 2,323 to 590 lines.

### Fixed
- **A `-mcp-only` sidecar sharing a data directory hung forever at startup**, silently — this was the documented Claude Desktop stdio configuration. It now fails fast with an actionable message.
- **Stored XSS** in the search-snippet fallback path, which emitted unescaped article content into a `dangerouslySetInnerHTML` sink.
- **Wildcard CORS on every route.** With no authentication, any website you visited could read and delete your entire wiki over `localhost`. Origins are now validated against an allow-list (`NEXWIKI_ALLOWED_ORIGINS`).
- **Lost revisions under concurrent writes.** `SaveArticle` assigned version numbers without a lock; a measured test lost 8 of 13 revisions with 12 concurrent writers.
- **No graceful shutdown.** SIGTERM (every `docker stop`) killed the process with the Bleve index open — the likely cause of search-index corruption on restart.
- Unbounded request bodies and OKF bundle decompression (zip-bomb exposure); both are now capped, returning `413` where appropriate.
- SVG uploads are served with `Content-Disposition: attachment` and a restrictive CSP so they cannot execute as same-origin scripts.
- Path containment now uses `filepath.Rel` instead of a string-prefix check.
- MCP `edit_wiki_article`'s optimistic-locking check is now atomic with its write.
- Broken-WikiLink affordance is a real `<button>`, reachable by keyboard and announced to screen readers.
- Search results no longer render a hardcoded `http://localhost:8080` URL.

### Security
- Added [`SECURITY.md`](./SECURITY.md) with the trust model and a private vulnerability reporting path.
- **NexWiki has no authentication.** This is now stated explicitly in the README and SECURITY.md rather than left implicit.

## [0.5.2] — 2026-08-08

### Fixed
- Enhanced CORS handling, added a mutex for concurrent writes, and tightened security ([#17](https://github.com/gruberchris/nexwiki/pull/17)).

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

[Unreleased]: https://github.com/gruberchris/nexwiki/compare/v0.5.2...HEAD
[0.5.2]: https://github.com/gruberchris/nexwiki/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/gruberchris/nexwiki/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/gruberchris/nexwiki/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/gruberchris/nexwiki/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/gruberchris/nexwiki/compare/v0.2.5...v0.3.0
[0.2.5]: https://github.com/gruberchris/nexwiki/compare/v0.2.3...v0.2.5
[0.2.3]: https://github.com/gruberchris/nexwiki/compare/v0.2.1...v0.2.3
[0.2.1]: https://github.com/gruberchris/nexwiki/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/gruberchris/nexwiki/releases/tag/v0.2.0
