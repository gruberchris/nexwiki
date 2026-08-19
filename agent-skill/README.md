# The NexWiki Agent Skill

This folder contains a single **Agent Skill** — [`nexwiki/`](./nexwiki) — that teaches any
AI coding agent to use your NexWiki as a second brain: storing plans and memories, looking
up prior knowledge, and ingesting new sources, without you prompting it every time.

It follows the vendor-neutral [Agent Skills](https://agentskills.io) standard (a
`SKILL.md` file in its own folder), so the **same skill works across Claude Code, GitHub
Copilot CLI, opencode, OpenAI Codex, and Google Antigravity** — you just drop the folder
into that tool's `skills/` directory.

```
nexwiki/
  SKILL.md              # lean router: identity, orient, house rules, and a behavior table
  references/           # per-behavior guides the agent loads only when needed
    remember.md         #   save a durable fact as an agent memory
    plan.md             #   save and track multi-step work
    search.md           #   recall what the wiki already knows
    ingest.md           #   compile a URL / notes into one linked article
```

The `SKILL.md` is intentionally small; the agent reads a `references/` guide on demand
(progressive disclosure), so the skill costs almost nothing until a behavior is triggered.
Copy the **whole `nexwiki/` folder** — the `references/` load only when used.

## Step 1 — Connect the MCP server

The skill tells the agent *how* to use NexWiki; the MCP server gives it the tools. Point
your agent at your running instance's Streamable HTTP endpoint (`http://localhost:8080/api/mcp`).
For example, in Claude Code:

```bash
claude mcp add --transport http nexwiki http://localhost:8080/api/mcp
```

See [docs/mcp_server.md](../docs/mcp_server.md) for the per-client MCP config (Copilot,
opencode, Codex, Antigravity, Cursor). One gotcha: **Antigravity** requires the `serverUrl`
field for remote servers (`{"mcpServers":{"nexwiki":{"serverUrl":"http://localhost:8080/api/mcp"}}}`),
not the legacy `url`/`httpUrl` keys.

## Step 2 — Install the skill

Copy the whole [`nexwiki/`](./nexwiki) folder into your agent's `skills/` directory. Use
**project scope** to share it with a repo (commit it), or **home scope** to reuse it across
every project on your machine.

| Agent CLI | Project scope | Home scope (reuse everywhere) |
|---|---|---|
| **Claude Code** | `.claude/skills/nexwiki/` | `~/.claude/skills/nexwiki/` |
| **GitHub Copilot CLI** | `.github/skills/nexwiki/` | `~/.copilot/skills/nexwiki/` |
| **opencode** | `.opencode/skills/nexwiki/` | `~/.config/opencode/skills/nexwiki/` |
| **OpenAI Codex** | `.codex/skills/nexwiki/` | `~/.codex/skills/nexwiki/` |
| **Google Antigravity** (`agy`) | `.agents/skills/nexwiki/` | `~/.gemini/antigravity-cli/skills/nexwiki/` |

For example, to reuse it across all your Claude Code projects:

```bash
mkdir -p ~/.claude/skills
cp -r agent-skill/nexwiki ~/.claude/skills/
```

> **Cover several tools at once.** The skills convention is shared, so a couple of home-dir
> locations blanket almost everything:
> - `~/.claude/skills/nexwiki/` is read by **Claude Code, Copilot CLI, and opencode**.
> - `~/.agents/skills/nexwiki/` is read by **Copilot CLI, opencode, Codex, and Antigravity**.
>
> Drop the folder in both and nearly every agent picks it up.

## Step 3 — Use it

Agents load the skill automatically when a request matches its description (remembering a
fact, planning work, looking something up, ingesting a source). You can also invoke it
explicitly — `/nexwiki` in Claude Code and Copilot CLI, or the `/skills` selector in Codex.
Some CLIs only scan skills at startup, so restart the session after installing.

Try it in any project:

```
What do we already know about <topic>?
Remember that <durable fact>.
Plan and implement <small task> — save the plan to NexWiki first.
```

Then, tomorrow: *"What changed in the wiki in the last 24 hours?"*

### Optional: argument autocomplete in Claude Code

Typing `/nexwiki` shows no hint about what can follow it. Claude Code can display one, via an
`argument-hint` field in the skill's frontmatter:

```yaml
---
name: nexwiki
description: Use NexWiki as a persistent second brain via the nexwiki MCP server. ...
argument-hint: "[remember|plan|search|ingest] [topic or text]"
---
```

The hint then appears inline in the `/` menu as you type. It is presentation only — the skill
already accepts free text either way, because Claude Code appends anything you type after the
command as `ARGUMENTS: <value>` when the body contains no `$ARGUMENTS` placeholder.

**This field is deliberately not shipped in `nexwiki/SKILL.md`, and adding it upstream would be a
regression.** `argument-hint` is a Claude Code extension, not part of the [Agent
Skills](https://agentskills.io) spec, whose frontmatter allows only `allowed-tools`,
`compatibility`, `description`, `license`, `metadata`, and `name`. The shipped skill uses just
`name` and `description`, so it stays spec-conformant and portable across all five CLIs above.
Adding `argument-hint` makes packaging or uploading the skill fail outright:

```
Unexpected key(s) in SKILL.md frontmatter: argument-hint.
Allowed properties are: allowed-tools, compatibility, description, license, metadata, name
```

So add it to *your installed copy* if you want it, not to the repo — and remember it is lost the
next time you re-copy the folder. Whether the other CLIs ignore an unknown key or reject it has
not been verified here.

For the same reason, `/nexwiki` works everywhere without configuration: the command name comes
from the **directory** name (`nexwiki/`), not from the frontmatter `name` field.

## Customizing agent behavior

**Don't fork `SKILL.md` to change the rules.** The skill points every agent at a live,
editable wiki page — **`nexwiki-agent-guidelines`** — which NexWiki seeds automatically on
first start. Edit it in your browser to add style rules, conventions, or project specifics;
changes reach every connected agent immediately, with no re-copying and no restart.

Your NexWiki also announces itself over MCP: on connect, the server sends a short
instructions hint telling the agent it is a second brain — so agents get a nudge even
before the skill loads.
