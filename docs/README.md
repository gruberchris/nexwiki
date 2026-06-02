# NexWiki Documentation Hub 📚

Welcome to the NexWiki documentation directory. This folder contains detailed guides, technical descriptions, and manuals to help you master NexWiki's features and understand its architecture.

---

## 📖 Available Guides

Select a guide below to explore specific features:

### 1. [NexWiki User & Content Creation Guide](./user_guide.md)
A comprehensive manual designed to help you create, format, link, share, upload, and export wiki content:
* **Managing Articles**: Creating, editing in different view layouts, and deleting wiki articles.
* **WikiLinks & Linking**: Creating double-bracket internal WikiLinks (`[[WikiLink]]`), custom display tags, and secure external links.
* **Media & Uploads**: Backed by a drag-and-drop file uploader and embedded image assets.
* **Exporting & Sharing**: Sharing page URLs, copying Markdown body text, and exporting articles directly to PDF, Microsoft Word (`.docx`), and Markdown (`.md`) files using the native File System Access API.

### 2. [NexWiki Article Versioning & Revision Guide](./version_control.md)
An advanced technical and user guide covering the flat-file gzipped backup and conflict prevention systems:
* **Flat-File Gzip backups**: How NexWiki backs up history using lightweight compressed `.md.gz` snapshots on disk.
* **Interactive Difference Panels**: Navigating and reviewing historical revisions in side-by-side **Split Diff** or unified **Inline Diff** views.
* **Instant Reversion**: Restoring past versions of articles safely.
* **Optimistic Locking Guards**: Understanding version numbers and how NexWiki prevents multi-session write collisions.

### 3. [NexWiki Model Context Protocol (MCP) Server Guide](./mcp_server.md)
A comprehensive technical manual describing the always-on Go MCP engine:
* **Transport Layers**: Connecting AI clients over standard input/output (Stdio) or Streamable HTTP network streams.
* **Semantic Tools**: In-depth explanations of all twelve exposed tools including read, search, optimistic locked writes, reverts, and dead WikiLink scanners.
* **Client Configurations**: Step-by-step setup guides for Claude Desktop and Cursor IDE.

### 4. [NexWiki Tags & AI Agent Memories Guide](./tags.md)
An advanced user and developer manual designed to help you organize content and understand protected AI memories:
* **User Tag Management**: Creating, applying, and globally deleting standard user tags.
* **Tag Editor & Badges**: Managing tags in the split-editor and viewing responsive color-coded tag badges.
* **AI Agent Memory System**: Protected `aiagent-` prefixed tags and the dedicated collapsible sidebar directory.
* **Search & Index Isolation**: How NexWiki auto-excludes agent-created memories from standard browsing/searches by default, keeping your workspace clean.

### 5. [NexWiki AI Agent Skills & Custom Registry Guide](./aiagent_skills.md)
A comprehensive technical manual describing the custom AI skills registry and management engine:
* **UI Management & Tags**: Toggling pages as skills and understanding the `aiagent-skill` tag.
* **Registry REST APIs**: Details on the `/api/skills`, `/api/skills/{slug}`, and raw `SKILL.md` endpoints.
* **Search Isolation**: How skills are isolated in search by default, and how to trigger explicit search bypass.
* **Integrations**: Connecting JetBrains editors and other custom AI agent systems to NexWiki as a custom skills registry.

### 6. [NexWiki Customizable Themes Engine Guide](./theme_guide.md)
A comprehensive technical and user guide covering the customizable, dual-variant themes engine:
* **Startup Configurations**: Setting default active themes via CLI flags or environment variables.
* **Dual-Variant Designing**: Building themes with specific Light and Dark mode variants, along with dynamic custom color pickers in the UI.
* **Theme Persistence**: Understanding how custom themes are stored in `custom_themes.json` inside the wiki's data directory.

### 7. [NexWiki AI Agent Integration & SOP Guide](./agent_integration_guide.md)
An advanced governance and integration manual designed to help you configure external AI agents (Cursor, Claude Desktop, Copilot):
* **Three Layers of Governance**: Custom tool schemas, workspace rules, and MCP Prompts.
* **Standard Operating Procedures (SOPs)**: Enforcing style checks, format rule lookups, and auto-saving project plans.
* **Configuration Blueprints**: Setup guidelines and step-by-step examples.

### 8. [NexWiki Advanced Editor, Linter, Scheduling & SSE Activity Guide](./editor_activity_linter_guide.md)
A comprehensive technical manual describing the upgraded CodeMirror 6 composing editor, real-time custom linter, syntax cheat guide, seasonal theme scheduling, SSE live activity stream, and ZIP export tools:
* **Built-in seasonal themes**: Date-based seasonal themes, environment flags, and custom dates scheduling.
* **Composing editor**: CodeMirror 6 integration, custom toolbar transactions, and Option B runtime adaptive color wraps.
* **Syntax validation checks**: Inline wavy underline highlights, hover quick fixes, right-click custom context menus, and the linter dashboard modal.
* **Real-time syncing**: Thread-safe EventBus circular queue logs, SSE network streams, cumulative unread badges, and zero-refresh dashboard count syncing.
* **Bulk exporter**: Creating category-specific packaged ZIP archives of the database.

---

## 🏗️ Architecture Overview

NexWiki integrates documentation directly with active AI pipelines. If you are an AI developer or are connecting an AI agent (like Claude Desktop or Cursor) to NexWiki, refer to the root [AGENTS.md](../AGENTS.md) for specifications on the exposed Model Context Protocol (MCP) server endpoints.
