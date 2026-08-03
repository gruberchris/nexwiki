---
name: nexwiki
description: Use NexWiki as a persistent second brain via the nexwiki MCP server. Use whenever the user wants to remember or save a fact, decision, or preference; capture, continue, or complete a plan; look up what is already known about a topic, project, or past decision; ingest an article, URL, transcript, or notes into the knowledge base; or orient at the start of a work session.
---

# NexWiki: your second brain

You have a NexWiki instance connected as an MCP server named `nexwiki` (its tools may be
prefixed `mcp__nexwiki__`). It is the user's persistent, cross-project knowledge base.
Store plans, memories, and knowledge there — not only in this chat — and consult it before
answering from scratch or re-deriving something.

## Orient first

At the start of a session, or when picking up prior work:

1. Call `get_context_overview` for a compact index of the whole wiki (titles, slugs,
   one-line summaries, tags). Read this before opening individual articles.
2. Call `get_recent_activity` (since: `"48h"`) to see what changed since last time.

Then `read_article` only the entries you actually need; `get_backlinks` follows related pages.

## Route to the right behavior

Work out what the user wants, then open and follow the matching guide in this skill's
`references/` folder. Load only the guide(s) you need; if a request spans several, handle
them in turn.

| If the user wants to… | Read and follow |
|---|---|
| Save / remember a fact, decision, or preference | `references/remember.md` |
| Plan, track, or complete multi-step work | `references/plan.md` |
| Look up or recall what's already known about something | `references/search.md` |
| Ingest a URL, article, transcript, or notes | `references/ingest.md` |

## Always

- Once per session, before creating or editing content, load the user's editable rulebook:
  `read_article(slug: "nexwiki-agent-guidelines")` and follow it — it overrides this skill.
  If it doesn't exist, proceed with the defaults in these guides.
- Set `description` and `source` on everything you create — descriptions power the context
  overview, sources keep knowledge auditable.
- Never relabel a reserved document type (`AI-Agent-Plan`, `AI-Agent-Skill`,
  `AI-Agent-Memory`) to a non-reserved one, and never strip a tool-managed `memory-<scope>`
  tag. Use `get_status_tags` for valid lifecycle tags. Slugs are lowercase and hyphenated.
- Never store credentials, tokens, or secrets in the wiki.
- If the `nexwiki` tools are unavailable, tell the user the nexwiki MCP server is not
  connected instead of guessing.
