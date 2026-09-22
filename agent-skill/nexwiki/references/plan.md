# Plan: save and track multi-step work

Any task with more than two steps must be saved as a plan **before** work begins — never
just print a plan in chat.

1. Check `search_wiki(type: "plans")` for an existing plan on this topic.
2. If one exists, continue it with `append_article(slug, content)`. Otherwise call `save_article` with
   `type: "AI-Agent-Plan"`, a clear title, the steps, and `project_context` set to the project name.
3. Use `append_article(slug, content)` for progress notes after each milestone.
4. To rewrite plan steps, use `save_article` with `slug`, `content`, and `loaded_version` (full replacement,
   with optimistic locking).
5. On completion: append final notes (deviations, files created, surprises) via `append_article`, then set
   `status: "completed"` with `save_article` — passing the plan's current body back unchanged as
   `content`, since `content` always replaces the whole body.
6. Report the plan's slug.
