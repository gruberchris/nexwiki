# Remember: save a durable fact as an agent memory

Use when the user states a durable fact, decision, or preference, or when you learn
something non-obvious worth keeping across sessions.

1. **Search first** — `list_agent_memories` and/or `search_wiki` — so you never create a
   blind duplicate.
2. **Append or create**:
   - If a closely related memory exists, `append_agent_memory` to it, or correct it in
     place with `edit_agent_memory`.
   - Otherwise `create_agent_memory` with a concise one-insight body, a one-line
     `description`, a `source` (how you learned it), and a `memory_type` scope — a project
     name for project-specific knowledge, a topic name for cross-project knowledge, or
     omitted for general knowledge.
3. **Keep it clean** — retire fully superseded memories with `delete_agent_memory`. Never
   let near-duplicates pile up.
4. Report the memory's slug and exactly what you wrote.
