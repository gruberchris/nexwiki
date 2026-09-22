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

1. Call `get_wiki_overview(since: "48h")` once. It returns document counts, the plans in
   flight, recent activity, and `pinned_memories` — the operator's `user` and `feedback`
   memories. Those are the operator's standing preferences and corrections: follow them on
   every task, and where one conflicts with this skill, the memory wins.
   The overview does not list every document: use `list_articles` (filter by `type`, `status`,
   or `tag`) for the full index and `search_wiki` to find a topic.

Then `read_article` only the entries you actually need; its backlinks lead to related pages.
Run each orientation call once: a search that finds nothing is an answer, not a reason to
search again with reworded queries.

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

- Set `description` and `source` on everything you create — descriptions power the wiki
  overview, sources keep knowledge auditable. For a memory they are required, together with
  `memory_kind`: `save_article` refuses a new memory missing any of the three.
- `content` always replaces the whole body. To change only metadata or status, pass the
  current body back unchanged.
- A `[[WikiLink]]` must name a document that exists in this wiki — never your own local memory
  files, instruction files, tools, or scratch paths. Cite external references as plain URLs.
- On a version conflict, retry once with the version the error names.
- Never relabel a reserved document type (`AI-Agent-Plan`, `AI-Agent-Skill`,
  `AI-Agent-Memory`) to a non-reserved one unless the user asks, and never strip a tool-managed
  `memory-<scope>` tag. Omitting `type` on an update keeps the current type. A document saved
  with the wrong type is fixed in place with `save_article` (its `slug`, `loaded_version`, and
  the right `type`) — never by deleting and recreating it; confirm with `list_articles(type: …)`.
- To remove text from a document's history (a name under embargo, personal details), rewrite it
  with `save_article(..., loaded_version, purge_history: true)`, then verify with
  `search_wiki(query, include_history: true)`. That keeps the document; `delete_article` does not —
  it is irreversible and a last resort. Plans have a status field (`draft`, `implementing`, `blocked`, `completed`, `superseded`,
  `parked`, `evergreen`, `archived`) and skills have `draft`, `ready`, `archived`.
  Slugs are lowercase and hyphenated.
- Never store credentials, tokens, or secrets in the wiki.
- If the `nexwiki` tools are unavailable, tell the user the nexwiki MCP server is not
  connected instead of guessing.
