# Plan: save and track multi-step work

Any task with more than two steps must be saved as a plan **before** work begins — never
just print a plan in chat.

1. Check `list_agent_plans` for an existing plan on this topic.
2. If one exists, continue it with `append_agent_plan`. Otherwise `create_agent_plan` with
   a clear title, the steps, and `project_context` set to the project name.
3. `append_agent_plan` progress notes after each milestone.
4. To rewrite plan steps, use `edit_agent_plan` with a `content` field (full replacement,
   with optimistic locking).
5. On completion: append final notes (deviations, files created, surprises), then add the
   `completed` tag with `edit_agent_plan`.
6. Report the plan's slug.
