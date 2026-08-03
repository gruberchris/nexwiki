# Look up: recall what the wiki already knows

Use before answering questions about the user's projects, decisions, conventions, or prior
work — and whenever the user asks "what do we know about X?".

1. `search_wiki` for the topic. If results are thin, scan `get_context_overview`.
2. `read_article` on the most relevant hits; use `get_backlinks` to find related decisions
   and pages, hopping the knowledge graph as needed.
3. Answer grounded in what the wiki says, and cite the article slugs you used. If the wiki
   contradicts your assumption, the wiki wins — or flag the conflict for the user.
4. Note any gaps or contradictions worth capturing later (see `references/remember.md`).
