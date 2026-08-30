# Remember: save a durable fact as an agent memory

Use when the user states a durable fact, decision, or preference, or when you learn
something non-obvious worth keeping across sessions.

1. **Search first** — `list_agent_memories` and/or `search_wiki` — so you never create a
   blind duplicate.
2. **Append or create**:
   - If a closely related memory exists, `append_agent_memory` to it, or correct it in
     place with `edit_agent_memory`.
   - Otherwise `create_agent_memory` with a concise one-insight body, a **required**
     one-line `description`, a **required** `source` (how you learned it), and **both**
     memory axes:
     - `memory_kind` (**required**) — what sort of fact this is: `project` (goals and
       constraints not derivable from the repo), `reference` (a pointer to an external
       resource), `user` (who the operator is), `feedback` (a correction the operator gave).
     - `memory_type` (optional) — how far it reaches: a project name for project-specific
       knowledge, a topic name for cross-project knowledge, or omitted for general knowledge.

     The axes are independent; every combination is legal. A `feedback` memory should say
     *why* the correction was given and *how to apply it*, not just what was said.

     `description` and `source` are refused if absent or blank. Write real values rather
     than a placeholder: the description is what `get_context_overview` shows, and the
     source is what lets the fact be re-verified later.
3. **Keep it clean** — retire fully superseded memories with `delete_agent_memory`. Never
   let near-duplicates pile up.
4. Report the memory's slug and exactly what you wrote.
