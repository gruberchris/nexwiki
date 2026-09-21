package server

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

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

The server exposes nine MCP tools: ` + "`search_wiki`" + `, ` + "`read_article`" + `, ` + "`save_article`" + `, ` + "`append_article`" + `,
` + "`list_articles`" + `, ` + "`delete_article`" + `, ` + "`get_wiki_overview`" + `, ` + "`get_backlinks`" + `, and ` + "`wiki_health`" + `. Every
document — wiki article, memory, plan, or skill — is written with ` + "`save_article`" + `, its
` + "`type`" + ` argument choosing the class.

## 0. Orientation runs once, then you write
These rules bound every other rule on this page. Orientation is a prerequisite, not a loop.
- Load this page **once per session**. If it is already in your context, do not read it again.
- Run each orientation call (` + "`get_wiki_overview`" + `, ` + "`list_articles`" + `, ` + "`search_wiki`" + `)
  once per question. Repeating a call you have already made is never the right next action.
- A search that returns nothing relevant is a **completed** check, not a failed one. Proceed —
  do not re-run it with reworded queries hoping for a different result.
- When the checks are done, write. If you have finished orienting and have not yet called
  ` + "`save_article`" + `, calling it is your next action.
- Unsure whether you already created something? Call ` + "`get_wiki_overview(since: \"1h\")`" + ` once and
  look at its recent activity. Do not restart the task from the beginning.

## 1. Orient at session start (progressive disclosure)
- Call ` + "`get_wiki_overview(since: \"48h\")`" + ` before reading anything. It returns document counts,
  the ` + "`user`" + ` and ` + "`feedback`" + ` memories (read them — they apply to every task), the plans in flight,
  and what changed recently. It does not list every document.
- For the full index, call ` + "`list_articles`" + ` filtered by ` + "`type`" + `, ` + "`status`" + `, or ` + "`tag`" + `; to find a
  topic, call ` + "`search_wiki`" + `.
- Then ` + "`read_article`" + ` only on the entries you actually need — do not bulk-read to orient.

## 2. Search before you write — once
- Before creating a wiki article, make one ` + "`list_articles(type: \"memories\")`" + ` or ` + "`search_wiki`" + ` call
  for relevant style guides, templates, or formatting memories, and follow any you find.
- If nothing matches the subject, that is the expected answer for a topic no template covers:
  use a sensible structure of your own and write the article. Do not search again.

## 3. Save multi-step work as plans
- Any task with more than two steps must be saved with ` + "`save_article(type: \"AI-Agent-Plan\")`" + ` (set
  ` + "`project_context`" + `) before work begins — never just print a plan in chat.
- Append progress with ` + "`append_article`" + ` after each milestone.
- Rewrite plan steps with ` + "`save_article`" + ` (its ` + "`slug`" + `, the ` + "`loaded_version`" + ` you read, and the
  full ` + "`content`" + `); set ` + "`status: \"completed\"`" + ` with ` + "`save_article`" + ` when done.
- Lifecycle state is the ` + "`status`" + ` **field**, never a tag. A plan carries exactly one of
  draft, implementing, blocked, completed, superseded, parked, evergreen, archived.
- A document created with the wrong type is corrected in place: ` + "`save_article`" + ` with its
  ` + "`slug`" + `, ` + "`loaded_version`" + `, and the right ` + "`type`" + `. Never delete and recreate it — that
  destroys its history. Confirm with ` + "`list_articles(type: …)`" + `, not a read.

## 4. Memory hygiene
- Keep memories succinct — one clear insight each, bullets over paragraphs.
- Give every memory a ` + "`description`" + ` and a ` + "`source`" + `. The description is what
  ` + "`list_articles`" + ` and ` + "`get_wiki_overview`" + ` show, so a memory without one is invisible when an
  agent orients itself; the source is what makes the fact re-verifiable, and origin cannot be recovered
  after the fact. Write real values — a placeholder defeats the point.
- A memory has **two independent axes**, and both are set at creation:
  - **Kind** — ` + "`memory_kind`" + `, a closed vocabulary of four: ` + "`project`" + `
    (goals and constraints not derivable from the repo), ` + "`reference`" + ` (a pointer to an
    external resource), ` + "`user`" + ` (who the operator is), ` + "`feedback`" + ` (a correction
    the operator gave — give the why and how to apply it in the body).
  - **Scope** — ` + "`memory_type`" + `, optional, free-form: a project name for project-specific
    knowledge, a topic name for cross-project knowledge, or omit for general knowledge.
  Every combination is legal. Kind is *what sort of fact*; scope is *how far it reaches*.
- Filter by kind with ` + "`search_wiki`" + `'s ` + "`memory_kind`" + ` argument. Ask for ` + "`user`" + ` and
  ` + "`feedback`" + ` to load what is known about the operator and how they want work done.
- A memory written before the kind axis existed carries none. It stays valid and editable;
  ` + "`wiki_health`" + ` lists them as ` + "`unkinded_memories`" + `. Classify one with
  ` + "`save_article`" + ` when you touch it — do not guess in bulk.
- Correct stale or wrong memories in place with ` + "`save_article`" + `; retire fully
  superseded ones with ` + "`delete_article`" + ` — do not create near-duplicates. ` + "`wiki_health`" + `
  reports memories that closely resemble each other.
- **Never write a credential into any document.** A write carrying an API key, token, private
  key or password is refused, and the refusal names the pattern class and offset without
  repeating the value. Describe the secret instead ("the deploy token for X, in 1Password") or use
  a placeholder like ` + "`<your-token>`" + `. A NexWiki document goes to disk, to version
  history, to the search index, to the web UI and to every connected client.
- Text that must not be kept — a leaked name, a customer's details — is redacted, not deleted:
  rewrite the document with ` + "`save_article`" + ` and ` + "`purge_history: true`" + ` to drop every earlier
  revision, then confirm with ` + "`search_wiki(query: <term>, include_history: true)`" + `.

## 5. Respect reserved types and tags
- Never relabel a reserved document type (` + "`AI-Agent-Plan`" + `, ` + "`AI-Agent-Skill`" + `, ` + "`AI-Agent-Memory`" + `)
  unless the user explicitly asks for it, and never strip a tool-managed ` + "`memory-<scope>`" + ` tag.
  Omitting ` + "`type`" + ` on an update always keeps the current one.
- Slugs are lowercase, hyphenated, and descriptive.
- Only plans and skills have a ` + "`status`" + `. Plans: the eight values above. Skills: draft, ready,
  archived. ` + "`get_wiki_overview`" + `'s ` + "`status_tags`" + ` lists each closed vocabulary. Never invent a
  value, and never put one in ` + "`tags`" + ` — both are rejected. Wiki articles and memories have no
  status and no tag rules.

## 6. Style preferences
- Add the wiki owner's personal writing conventions here (header casing, code-block
  language identifiers, table style, emoji policy, ...).
`

// SeedAgentGuidelinesIfMissing creates the nexwiki-agent-guidelines governance skill when
// it does not already exist, so the MCP tool-description hooks resolve out of the box.
// It is idempotent: if the article is already present (as a skill or any type) it does
// nothing. Errors are logged but never fatal — seeding is a convenience, not a requirement.
//
// Only a file that is genuinely absent is seeded. One that exists but cannot be read or parsed is
// still the user's, perhaps with a typo in its front matter, and seeding over it would replace their
// rules with the default; it gets a warning instead. Lstat, so a symlink whose target is missing
// counts as present rather than being replaced.
func (srv *Server) SeedAgentGuidelinesIfMissing() {
	name := AgentGuidelinesSlug + ".md"
	rel := filepath.Join("articles", name)
	if _, err := os.Lstat(filepath.Join(srv.Storage.ArticleDir, name)); err == nil {
		if _, err := srv.Storage.GetArticle(AgentGuidelinesSlug); err != nil {
			log.Printf("Warning: not seeding the %s skill: %s exists but could not be loaded, so it was left as it is: %v",
				AgentGuidelinesSlug, rel, err)
		}
		return // already exists — leave the user's version untouched
	} else if !errors.Is(err, fs.ErrNotExist) {
		log.Printf("Warning: not seeding the %s skill: could not check whether %s exists: %v", AgentGuidelinesSlug, rel, err)
		return
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
