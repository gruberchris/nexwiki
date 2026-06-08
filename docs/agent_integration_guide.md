# NexWiki AI Agent Integration & SOP Guide 🤖✍️

Integrating AI coding agents (like Claude Desktop, Cursor, and GitHub Copilot) with NexWiki's Model Context Protocol (MCP) server transforms your wiki into an active, collaborative **second brain**. 

However, LLM agents are naturally "greedy planners"—they often default to writing content or creating plans purely in their chat context, or guessing formats based on general knowledge, rather than looking up your local wiki rules and saving their work.

---

## 🛑 The Production Challenge: Multi-Project Workspaces

When you deploy NexWiki in a production environment, you and other developers will be working on **other software projects** (e.g., building a Go service in `/Projects/my-go-api` or a React app in `/Projects/web-app`). 

Because you are using NexWiki as a global tool connected via MCP, **you cannot keep dropping custom `.cursorrules` or `copilot-instructions.md` files into every software repository you work on.** That is unrepeatable, inconsistent, and highly error-prone.

---

## 🏆 The Solution: Centralized Skills-Based Governance

NexWiki solves this by utilizing its native **AI Agent Skills Registry** combined with **Schema-Driven Prerequisite Hooking** to implement zero-configuration, centralized governance.

```mermaid
graph TD
    subgraph External Software Project Workspace ["External Project Workspace (e.g. /Projects/my-app)"]
        Agent[AI Agent Planner / LLM]
    end

    subgraph NexWiki Production Server
        mcp[MCP Server Engine]
        guidelines["Centralized Guidelines Skill (nexwiki-agent-guidelines)"]
        tools["create_wiki_article / create_agent_plan"]
    end

    Agent -->|1. List Tools| mcp
    mcp -->|2. Returns Tool Schemas with Guidelines Hook| Agent
    Agent -->|3. Must load guidelines first| guidelines
    guidelines -->|4. Returns Core SOPs| Agent
    Agent -->|5. Apply Style Memories & Save Plan| tools
```

---

## 🛠️ How Skills-Based Governance Works Dynamically

### 1. Centralized "Wrench" Skill (`nexwiki-agent-guidelines`)
Instead of duplicating rules in local files across countless folders, all instructions are stored centrally inside a single, live, editable page in NexWiki named **NexWiki Agent Core Guidelines** (slug: `nexwiki-agent-guidelines`). 
* Because it is tagged with `aiagent-skill`, it is automatically registered on NexWiki's Custom AI Skills Registry.
* You can edit these agent rules directly from your browser in the NexWiki UI. **Any changes you save are instantly propagated to all connected AI agents globally.**

> **The slug must be exactly `nexwiki-agent-guidelines`.** The MCP tool schema hooks are hard-coded to reference this slug. If the article does not exist, the agent will get a `not found` error when it tries to load the guidelines and may proceed without any rules applied.

---

### 2. Schema-Driven Prerequisite Hooking
To ensure the agent actually reads these guidelines without any manual user prompting, we embed explicit prerequisites directly into the **MCP tool schema descriptions** (`server/mcp.go`). 

For example, the description for `create_wiki_article` is registered as:
> *`Create a brand new wiki article. (IMPORTANT: AI agents must ALWAYS load the global operational guidelines skill using 'read_article(slug: "nexwiki-agent-guidelines")' to understand formatting and style guide check requirements before executing this tool.)`*

When your agent parses these tools in *any* external workspace, the LLM planner reads this prerequisite and is **forced to execute `read_article(slug: "nexwiki-agent-guidelines")` in its first turn** before drafting or saving any content!

---

### 3. One-Time Global Client Setup (The Ultimate Best Practice)
To guarantee your agents are immediately aligned at the start of a session (before they even select a tool), you can add a single, one-time instruction to your global client configurations:

#### Option A: Claude Desktop (Global Configuration)
Open your global Claude Desktop configuration:
* **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add a custom instruction rule prompting the agent to fetch the guidelines:
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "docker",
      "args": ["exec", "-i", "personal-wiki", "/app/nexwiki"],
      "env": {
        "NEXWIKI_SYSTEM_PROMPT_MODIFIER": "You have the NexWiki MCP server registered. At the beginning of the session, always read the global operational guidelines using 'read_article(slug: \"nexwiki-agent-guidelines\")' to align on style guide lookups and task planning."
      }
    }
  }
}
```

#### Option B: Cursor (Global Custom Instructions)
1. Open Cursor **Settings** -> **Features** -> **Custom Instructions**.
2. Paste the following global instruction:
   > *"You have the `nexwiki` MCP server registered. Before writing any documentation or saving development plans, always load and follow the global agent operational guidelines skill using `read_article(slug: 'nexwiki-agent-guidelines')`."*

---

## 📖 Practical End-to-End Walkthroughs

### Walkthrough 1: Centralized Style Enforcement in an External Project
Imagine you are working in a Python service (`/Projects/python-api`) and tell Cursor/Claude: *"Add a wiki page about our new PostgreSQL database schema."*

**The Agent's Step-by-Step Execution:**
1. **Agent retrieves tools**: The agent parses the MCP tools list and sees `create_wiki_article`.
2. **Schema Hook Triggers**: The description of `create_wiki_article` warns that it *must* first load the operational guidelines.
3. **Agent loads guidelines**: The agent calls `read_article(slug="nexwiki-agent-guidelines")` and learns it must check memories for style guides.
4. **Agent checks style memories**: The agent calls `list_agent_memories(memory_type="rules")` or searches for `style guide`. It discovers `sql-dialect-article-format-template`.
5. **Agent reads template**: It calls `read_article(slug="sql-dialect-article-format-template")`, discovering the required schema table headers and syntax blocks.
6. **Agent creates page**: The agent drafts a beautiful, perfectly formatted Postgres article conforming to the wiki's rules, and saves it using `create_wiki_article`.

---

### Walkthrough 2: Auto-Saving Collaborative Plans Globally
Imagine you are working in a legacy project (`/Projects/legacy-system`) and tell your agent: *"We need to plan the migration of this legacy database to MySQL."*

**The Agent's Step-by-Step Execution:**
1. **Schema Hook Triggers**: The agent outlines a migration plan in its planner. It notes that the MCP `create_agent_plan` tool description requires it to first load `nexwiki-agent-guidelines`.
2. **Agent loads guidelines**: The agent calls `read_article(slug="nexwiki-agent-guidelines")` and confirms it must save any plans persistently in the wiki.
3. **Agent saves plan**: The agent automatically executes `create_agent_plan`, creating the page `mysql-database-migration-plan` with the `project_context` set to `legacy-system`.
4. **Agent reports slug**: The agent provides you with the slug and link, keeping both the local workspace and your knowledge base perfectly in sync.

---

### Walkthrough 3: Plan Completion Workflow
Imagine your agent has finished implementing a plan it previously created (e.g., `mysql-database-migration-plan`).

**The Agent's Step-by-Step Execution:**
1. **Agent completes implementation**: The agent finishes all the coding tasks outlined in the plan.
2. **Agent appends final notes**: The agent calls `append_agent_plan(slug="mysql-database-migration-plan")` to document the implementation: any plan deviations, files created, tools used, unexpected challenges, or other observations.
3. **Agent marks plan as completed**: The agent calls `edit_agent_plan(slug="mysql-database-migration-plan", tags=["completed"], loaded_version=<current_version>)` to add the `completed` status tag.
4. **Protected tag preserved**: The `aiagent-plan` tag is automatically preserved by the system and cannot be removed.
5. **Agent reports completion**: The agent confirms the plan is now marked as completed with final notes appended.

---

## 📋 Crafting Your `nexwiki-agent-guidelines` Skill

This skill is the single most important document in your NexWiki instance when using AI agents. If it does not exist, the schema hooks in `create_wiki_article`, `create_agent_memory`, and `create_agent_plan` will fail silently — the agent will get a not-found error and proceed without any governance at all.

**Create it immediately after setting up NexWiki**, before connecting any AI agent. Use the **AI Skill** button in the sidebar and set the title to `NexWiki Agent Core Guidelines` (the slug auto-generates as `nexwiki-agent-guidelines`).

### What to Include

Write this skill as a numbered list of imperative directives — not prose. Agents parse and follow bullet lists far more reliably than paragraphs.

**1. Memory search before writing**
```markdown
Before creating any wiki article, always call `list_agent_memories` or `search_wiki` 
for formatting memories, style guides, or templates relevant to the article type.
If a style guide memory exists, read it and follow it exactly.
```

**2. Plan-saving behavior**
```markdown
Any implementation task with more than two steps must be saved as a Collaborative AI Plan 
using `create_agent_plan` before work begins. Set `project_context` to the project name.
Append progress using `append_agent_plan` after each major milestone.
Mark plans completed with `edit_agent_plan` (add "completed" tag) once done.
```

**3. Tag and slug rules**
```markdown
Never remove `aiagent-plan`, `aiagent-skill`, or `aiagent-memory-*` tags from any document.
Article slugs must be lowercase, hyphenated, and descriptive (e.g., "go-api-database-schema").
Use `get_status_tags` to see valid lifecycle tags (draft, wip, completed, etc.).
```

**4. Memory creation guidelines**
```markdown
Memories must be succinct — bullet points over paragraphs. One clear insight per memory.
Use project-scoped memory_type (e.g., "nexwiki") for project-specific knowledge.
Use topic-scoped memory_type (e.g., "docker") for cross-project reusable knowledge.
Omit memory_type only for general, broadly applicable knowledge.
```

**5. Your personal style preferences** *(examples)*
```markdown
Article headers: use sentence case, not title case.
Code blocks: always specify the language identifier.
Tables: prefer pipe tables with alignment markers.
Avoid emoji in article titles or headers unless the existing page already uses them.
```

### What to Exclude

Keep the guidelines skill lean. Bloated context slows agents down and risks being truncated.

| Do NOT include | Use instead |
|---|---|
| Project-specific configs (API keys, folder paths, repo names) | A project-scoped AI memory (`memory_type: "myproject"`) |
| Per-language coding standards | Individual topic skills (e.g., `go-coding-standards`) |
| Content that changes per task | Agent memory logs (`create_agent_memory`) |
| Verbose explanations of NexWiki internals | Cross-reference `read_article(slug: "nexwiki-agent-guidelines")` is enough |
| Credentials, tokens, or secrets | Never store these in NexWiki |

### Keeping It Up to Date

Because the skill is a live wiki article, you can edit and refine it directly in your browser at any time. Changes take effect immediately — the next agent session will read the updated version. There is no cache to flush and no server restart required.
