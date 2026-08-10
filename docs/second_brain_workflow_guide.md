# NexWiki Second Brain Workflow Guide 🧠

This guide walks through, step by step, how to use NexWiki as an AI **second brain** with your AI agent CLI — from one-time setup through the daily working loop. It uses **Claude Code** as the example client, but everything applies equally to Cursor, GitHub Copilot CLI, Claude Desktop, or any MCP-compatible agent.

The workflow combines three ideas from the second-brain literature (captured in the wiki articles `karpathy-second-brain-method` and `meta-ai-second-brain`):

1. **Progressive disclosure** (Meta): the agent loads a lean index of the whole wiki first, then reads only what it needs.
2. **Compile at ingest, don't retrieve at query time** (Karpathy): knowledge is synthesized into persistent, cross-linked pages the moment it enters the system, so it compounds across sessions.
3. **The MCP bridge + skills registry** as the infrastructure that lets any agent participate.

---

## 🚀 One-Time Setup

### 1. Deploy a build with the 28-tool MCP surface

The second-brain tools (`get_context_overview`, `get_backlinks`, `edit_agent_memory`, `delete_agent_memory`, `get_recent_activity`, `export_okf_bundle`, `import_okf_bundle`, plus the `description`/`source` front-matter fields) require a current build. If you run the Docker container, rebuild and redeploy:

```bash
docker compose up -d --build
```

**Verify**: ask your agent to list NexWiki's tools — you should see **28**, including `get_context_overview` and `get_recent_activity`.

### 2. Connect your CLI over Streamable HTTP

Streamable HTTP is preferred over stdio — all clients share the running server process, which avoids Bleve search-index file-lock contention:

```bash
claude mcp add --transport http nexwiki http://localhost:8080/api/mcp
```

(See the [MCP Server Guide](./mcp_server.md) for Cursor, Claude Desktop, and Copilot CLI equivalents.)

### 3. Install the agent skill (once, for all projects)

Instead of pasting a rule into every project, install the provided **Agent Skill** — the
folder [`agent-skill/nexwiki/`](../agent-skill/nexwiki) — into your agent's `skills/`
directory. Use **home scope** to reuse it across every project:

| Agent CLI | Home scope | Project scope |
|---|---|---|
| **Claude Code** | `~/.claude/skills/nexwiki/` | `.claude/skills/nexwiki/` |
| **Copilot CLI** | `~/.copilot/skills/nexwiki/` | `.github/skills/nexwiki/` |
| **opencode** | `~/.config/opencode/skills/nexwiki/` | `.opencode/skills/nexwiki/` |
| **Codex** | `~/.codex/skills/nexwiki/` | `.codex/skills/nexwiki/` |
| **Antigravity** (`agy`) | `~/.gemini/antigravity-cli/skills/nexwiki/` | `.agents/skills/nexwiki/` |

```bash
mkdir -p ~/.claude/skills && cp -r agent-skill/nexwiki ~/.claude/skills/
```

The skill (a `SKILL.md` in its own folder, per the [Agent Skills](https://agentskills.io)
standard) activates the whole workflow — load `nexwiki-agent-guidelines`,
`get_context_overview`, `get_recent_activity(since: "48h")` at session start — which in turn
drives style checks, plan auto-saving, and memory hygiene. Agents load it automatically when
relevant, or you can invoke it with `/nexwiki`. NexWiki also sends a short "you are a second
brain" hint to any MCP client on connect, so agents get a nudge even before the skill loads.
Full per-tool paths and tips: [`agent-skill/README.md`](../agent-skill/README.md).

### 4. The governance skill is seeded for you

The wiki article with slug `nexwiki-agent-guidelines` (OKF type `AI-Agent-Skill`) is the system's "schema file" — the equivalent of Karpathy's `CLAUDE.md`. NexWiki **seeds a default version automatically the first time the server starts**, so the MCP hooks resolve out of the box. Refine it in the wiki UI at any time — changes reach every agent immediately. To author it from scratch, see [Crafting Your nexwiki-agent-guidelines Skill](./agent_integration_guide.md#-crafting-your-nexwiki-agent-guidelines-skill).

---

## 🔄 The Session Loop

What a well-configured agent does every session, in order:

### Step 1 — Orient (progressive disclosure)

The agent calls **`get_context_overview`** and receives the entire wiki as one compact index — title, slug, one-line description, tags, and updated date per entry, grouped into Wiki Articles / Agent Memories / Agent Plans / Agent Skills sections. This is the "lean root context": a few hundred tokens instead of bulk-reading articles.

It then calls **`get_recent_activity(since: "48h")`** to see what you, other agents, or other sessions changed since it last looked — the answer to *"what happened while I was gone?"*. Events come from the durable `data/activity.jsonl` log, so they survive server restarts.

### Step 2 — Drill in selectively

Based on the overview, the agent calls **`read_article`** only on relevant entries. Each read now includes:

* the **Description** and **Source** metadata (when set), and
* a **`Linked from:`** footer listing inbound internal links, in either form.

For deeper graph traversal it calls **`get_backlinks(slug)`** — *"what references this decision?"* — and hops the knowledge graph associatively, the way a human brain follows threads. Writing flows in the other direction too: agents add internal links when creating content — `[[WikiLinks]]` or absolute `[text](/articles/slug)` Markdown links, both of which the graph counts — and the **Linked from** panel in the web UI shows you the same inbound links.

### Step 3 — Work, with plans

When you give the agent a multi-step task, the governance rules force it to:

1. **`create_agent_plan`** (with `project_context`) *before* starting work — never just print a plan in chat.
2. **`append_agent_plan`** progress notes after each milestone.
3. On completion: append final notes (deviations, files created, surprises), then **`edit_agent_plan`** to add the `completed` status tag.

> **Correcting plan steps:** Use `edit_agent_plan` with a `content` field to rewrite plan content in-place (full replacement with optimistic locking). Use `append_agent_plan` for additive progress notes only. The same tool sets `description` and `source`, so a plan's one-line summary can be corrected after creation rather than only at it.

You watch all of this live in the Activity Drawer, and the plan is a normal wiki page you can read, edit, and build on later.

### Step 4 — Capture knowledge as it's earned

When the agent solves something non-obvious or you tell it a durable fact:

1. **Search first**: `list_agent_memories` → `search_wiki` — never create blind duplicates.
2. **Append or create**: prefer `append_agent_memory` on an existing memory; otherwise `create_agent_memory` with a scoped `memory_type` (project name, topic name, or omitted for general), plus a `description` and `source`.
3. **Hygiene loop** (the key behavioral upgrade): when a memory turns out to be *wrong or stale*, the agent corrects it in place with **`edit_agent_memory`** or retires it with **`delete_agent_memory`** — instead of letting contradictions pile up. As a guardrail, `delete_wiki_article` *refuses* memory-tagged pages, so a careless bulk cleanup can't destroy curated memories.

---

## 📥 The Capture-and-Compile Loop (Karpathy Workflow)

For ingesting **external** knowledge — articles, transcripts, meeting notes, research:

### 1. Capture without organizing

Paste anything into a quick wiki article tagged **`inbox`** (or just hand the agent a URL). Don't structure it, don't title it carefully — the point is zero-friction capture.

### 2. Compile one source at a time

Say *"ingest my inbox"* or *"ingest this article: \<URL\>"*. The agent loads the **`ingest-source`** skill from your wiki's Skills Registry, which walks it through:

1. Load the governance skill and style guides.
2. Orient with `get_context_overview` to avoid duplicates.
3. Read the source **fully** before writing.
4. Synthesize a proper wiki article — a compilation in the wiki's voice, not a transcript — with `description` and `source` (citation) set.
5. Cross-link to related pages — `[[WikiLinks]]` or absolute `/articles/<slug>` Markdown links, whichever the wiki's house style prefers — and add backlinks from 1–3 closely related existing pages.
6. **Flag contradictions** with existing content for your review — never silently overwrite.
7. Remove the `inbox` tag from the raw dump (or delete it).
8. Report what was created, linked, and flagged.

### 3. One source per pass — by design

The skill explicitly stops after each source so you can steer. Batch ingestion degrades quality and removes your ability to guide; an empty inbox means the wiki is fully compiled.

Over time this compounds: descriptions make the overview richer, sources keep everything auditable, backlinks knit the graph together, and the activity log gives every future session continuity.

---

## 🧪 A Concrete First Session to Try

```text
You:  Ingest this article into the wiki: <paste URL>
You:  What do we already know about <topic>? What links to it?
You:  Plan and implement <small task> — save the plan to NexWiki first.

Next day:
You:  What changed in the wiki in the last 24 hours?
```

That sequence exercises every piece of the system: the ingest skill, overview + backlinks for retrieval, the plan lifecycle, and the durable activity log.

---

## 🔧 Tuning Tips

* **Activity log noise**: `read_article` events land in the activity log too — useful for *"what was I looking at?"*, but if it feels noisy, filter with `get_recent_activity(action: "edit")` or `source: "api"` (human web UI actions only).
* **`since` formats**: `get_recent_activity` accepts Go durations (`30m`, `24h`, `168h` for a week) or RFC3339 timestamps.
* **Descriptions pay rent**: the one-line `description` field is what makes `get_context_overview` powerful. Encourage agents (via the guidelines skill) to set it on everything they create; add them yourself in the editor's description input when writing manually.
* **Sources keep knowledge auditable**: a `source` can be a URL, a document reference, or simply `"conversation with Chris, 2026-06-11"`. Six months later, provenance is the difference between trusting and re-verifying a note.
* **Stdio fallback**: if you must use stdio transport (no running web server), remember each stdio client spawns its own process — don't combine it with a running server on the same data directory, or Bleve lock contention can occur.

---

## 📚 Related Guides

* [MCP Server Guide](./mcp_server.md) — all 29 tools in detail, client connection configs
* [AI Agent Integration & SOP Guide](./agent_integration_guide.md) — governance layers and the guidelines skill
* [Tags & AI Agent Memories Guide](./tags.md) — protected tags and memory isolation
* [AI Agent Skills & Custom Registry Guide](./aiagent_skills.md) — the skills registry powering `ingest-source`
