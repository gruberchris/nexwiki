package server

import "log"

// AgentGuidelinesSlug is the fixed slug of the centralized governance skill that the MCP
// tool-description hooks in mcp.go instruct agents to load before any create operation.
// It must never change — the hooks reference it verbatim.
const AgentGuidelinesSlug = "nexwiki-agent-guidelines"

// agentGuidelinesTitle must slugify to AgentGuidelinesSlug ("nexwiki-agent-guidelines"),
// since create_agent_skill/SaveArticle derive the slug from the title. Do not add words
// like "Core" here — that would produce a different slug the MCP hooks don't reference.
const agentGuidelinesTitle = "NexWiki Agent Guidelines"

// defaultAgentGuidelines is the seed body written when no nexwiki-agent-guidelines skill
// exists yet. It is a lean, editable starting point — users refine it in the wiki UI.
const defaultAgentGuidelines = `# NexWiki Agent Guidelines

Operating rules for AI agents working with this NexWiki second brain. Edit this page in
the wiki UI to change how every connected agent behaves — updates take effect immediately.

## 0. Orientation runs once, then you write
These rules bound every other rule on this page. Orientation is a prerequisite, not a loop.
- Load this page **once per session**. If it is already in your context, do not read it again.
- Run each orientation call (` + "`get_context_overview`, `list_agent_memories`, `search_wiki`" + `)
  once per question. Repeating a call you have already made is never the right next action.
- A search that returns nothing relevant is a **completed** check, not a failed one. Proceed —
  do not re-run it with reworded queries hoping for a different result.
- When the checks are done, write. If you have finished orienting and have not yet called a
  ` + "`create_*`" + ` tool, calling it is your next action.
- Unsure whether you already created something? Call ` + "`get_recent_activity`" + ` once and look.
  Do not restart the task from the beginning.

## 1. Orient at session start (progressive disclosure)
- Call ` + "`get_context_overview`" + ` to load a compact index of the whole wiki before reading anything.
- Then ` + "`read_article`" + ` only on the entries you actually need — do not bulk-read to orient.
- When resuming, call ` + "`get_recent_activity`" + ` (e.g., since: "48h") to see what changed.

## 2. Search before you write — once
- Before creating a wiki article, make one ` + "`list_agent_memories`" + ` or ` + "`search_wiki`" + ` call
  for relevant style guides, templates, or formatting memories, and follow any you find.
- If nothing matches the subject, that is the expected answer for a topic no template covers:
  use a sensible structure of your own and write the article. Do not search again.

## 3. Save multi-step work as plans
- Any task with more than two steps must be saved with ` + "`create_agent_plan`" + ` (set
  ` + "`project_context`" + `) before work begins — never just print a plan in chat.
- Append progress with ` + "`append_agent_plan`" + ` after each milestone.
- Rewrite plan steps with ` + "`edit_agent_plan`" + ` (full ` + "`content`" + ` replacement); add the
  ` + "`completed`" + ` tag with ` + "`edit_agent_plan`" + ` when done.

## 4. Memory hygiene
- Keep memories succinct — one clear insight each, bullets over paragraphs.
- Set a one-line ` + "`description`" + ` and a ` + "`source`" + ` on everything you create.
- Scope with ` + "`memory_type`" + `: a project name for project-specific knowledge, a topic name
  for cross-project knowledge, or omit for general knowledge.
- Correct stale or wrong memories in place with ` + "`edit_agent_memory`" + `; retire fully
  superseded ones with ` + "`delete_agent_memory`" + ` — do not create near-duplicates.

## 5. Respect reserved types and tags
- Never relabel a reserved document type (` + "`AI-Agent-Plan`, `AI-Agent-Skill`, `AI-Agent-Memory`" + `)
  to a non-reserved one, and never strip a tool-managed ` + "`memory-<scope>`" + ` tag.
- Slugs are lowercase, hyphenated, and descriptive. Use ` + "`get_status_tags`" + ` for valid
  lifecycle tags (draft, wip, completed, ...).

## 6. Style preferences
- Add the wiki owner's personal writing conventions here (header casing, code-block
  language identifiers, table style, emoji policy, ...).
`

// SeedAgentGuidelinesIfMissing creates the nexwiki-agent-guidelines governance skill when
// it does not already exist, so the MCP tool-description hooks resolve out of the box.
// It is idempotent: if the article is already present (as a skill or any type) it does
// nothing. Errors are logged but never fatal — seeding is a convenience, not a requirement.
func (srv *Server) SeedAgentGuidelinesIfMissing() {
	if _, err := srv.Storage.GetArticle(AgentGuidelinesSlug); err == nil {
		return // already exists — leave the user's version untouched
	}

	_, err := srv.Storage.SaveArticle(
		"",
		agentGuidelinesTitle,
		defaultAgentGuidelines,
		"Centralized operating rules loaded by AI agents before creating or editing NexWiki content.",
		"NexWiki default seed",
		"",
		"Seeded default agent guidelines",
		nil,
		ContentTypeSkill,
	)
	if err != nil {
		log.Printf("Warning: failed to seed %s skill: %v", AgentGuidelinesSlug, err)
		return
	}
	log.Printf("Seeded default governance skill: %s", AgentGuidelinesSlug)
}
