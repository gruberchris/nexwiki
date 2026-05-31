# NexWiki Model Context Protocol (MCP) Server Guide 🤖

NexWiki is designed as an **AI-ready second brain**. In addition to providing a beautiful personal knowledge base web application, NexWiki runs an **always-on Model Context Protocol (MCP) server** directly inside its compiled executable. 

This protocol acts as a standardized bridge allowing AI agents (like Claude Desktop, Cursor, or custom LLM systems) to query, read, and explore your personal wiki in real-time. By connecting your agent to NexWiki, you empower it to reason with access to your entire personal knowledge base.

---

## 🏗️ Architectural Overview

The NexWiki MCP server supports two primary transport layers:

1. **Stdio (Standard Input/Output) [JSON-RPC 2.0]**: Typically used for local server-agent processes. The agent runs the NexWiki binary or spins up the Docker container directly, piping JSON-RPC 2.0 messages via standard input/output.
2. **HTTP / Server-Sent Events (SSE)**: Enables a networked connection over HTTP. It uses an SSE channel (`GET`) at `/api/mcp` to stream notifications and updates, combined with standard HTTP `POST` requests to execute synchronous JSON-RPC commands.

### 🔒 Log Safety Guarantee
In order to prevent stdio pipe corruption (which breaks JSON-RPC communication in tools like Claude Desktop), **NexWiki redirects all internal system and web application logs exclusively to standard error (`Stderr`)**. Only valid JSON-RPC envelopes are ever output to `Stdout`.

---

## 🛠️ Exposed MCP Tools

The NexWiki MCP server registers and exposes nine powerful, semantic tools for AI agents:

### 1. `search_wiki`
Performs a high-speed, full-text search across all wiki articles using the built-in **Bleve Search** engine.

* **Arguments**:
  * `query` (string, **required**): The search keywords or query string. Supports wildcards, quotes for exact matches, and boolean terms.
* **Behavior**:
  Executes the search query against the local Bleve index. It converts scored matches into a human-readable text block. To optimize LLM context usage, all HTML `<mark>` search highlight tags are automatically converted to clean markdown bold formatting (`**`).

---

### 2. `read_article`
Retrieves the raw Markdown content and Yaml-style front-matter configurations of a specific article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL-safe slug of the target article (e.g. `home` or `setup-guide`).
* **Behavior**:
  Reads the Markdown file on disk, parses the front-matter metadata, and returns a plain text document listing the article Title, Slug, Created timestamp, Updated timestamp, and the complete raw Markdown body.

---

### 3. `list_articles`
Lists all articles currently available in your knowledge base. This acts as a directory index for the agent to understand what documentation exists.

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the database and returns a bulleted plain text index containing the titles, URL-safe slugs, and last-edited timestamps for all active articles.

---

### 4. `create_wiki_article`
Creates a brand new wiki article with a given title and raw Markdown content body.

* **Arguments**:
  * `title` (string, **required**): The human-readable title of the new article (e.g. "React Hooks Guide").
  * `content` (string, **required**): The raw Markdown content of the article body.
  * `edit_summary` (string, **optional**): A summary describing the reason for creating the page.
* **Behavior**:
  Automatically handles title slugification, checks for slug collisions, serializes the metadata block, commits the first version backup snapshot, saves the flat Markdown file on disk, and indexes the new article in Bleve for search.

---

### 5. `edit_wiki_article`
Modifies the title, Markdown content, or edit summary of an existing wiki article.

* **Arguments**:
  * `slug` (string, **required**): The unique URL slug of the article to edit.
  * `title` (string, **required**): The updated title of the article.
  * `content` (string, **required**): The updated raw Markdown content of the article body.
  * `loaded_version` (integer, **required**): The current version number loaded by the AI agent.
  * `edit_summary` (string, **optional**): A summary detailing the modifications.
* **Behavior**:
  Employs **optimistic locking** to prevent write collision conflicts. If the `loaded_version` does not match the active version on disk, it aborts the write with a conflict message (notifying the agent to re-fetch and try again). On success, it creates a new gzipped history backup snapshot (`.md.gz`), writes the updated flat Markdown file, and refreshes the search index.

---

### 6. `delete_wiki_article`
Permanently deletes an existing wiki article and its associated resources.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the article to delete.
* **Behavior**:
  Permanently deletes the Markdown file, all its gzip revision backups, and its uploaded media files/assets from the server. It also de-indexes the page from Bleve.

---

### 7. `get_article_history`
Retrieves the full revision history log of a wiki page, showing version numbers, timestamps, and edit summaries.

* **Arguments**:
  * `slug` (string, **required**): The URL-safe slug of the target article.
* **Behavior**:
  Scans the gzip history directory and returns a structured, bulleted plain text revision list of all historical edits made to the page.

---

### 8. `revert_article_version`
Reverts the active state of an article back to a specific historical version number.

* **Arguments**:
  * `slug` (string, **required**): The URL slug of the target article.
  * `version` (integer, **required**): The historical version number to restore.
* **Behavior**:
  Extracts the compressed `.md.gz` snapshot of that version, restores it to the active flat file on disk, increments the active version number, and updates the search index.

---

### 9. `get_wiki_statistics`
Scans the entire knowledge base to compile total page stats and **autonomously scan for dead or broken internal WikiLinks** (e.g., `[[Missing Page]]`).

* **Arguments**: None (empty object `{}`).
* **Behavior**:
  Scans the raw content of all wiki articles for double-bracket WikiLink references. It normalizes targets into slugs and matches them against active articles. It returns a summary text listing total pages, total WikiLinks, total broken links, and details on exactly which pages contain dead references so the AI agent can autonomously fix them!

---

## 🔌 Connecting Clients

To connect your AI agents (Claude Desktop, Cursor, Copilot CLI, Claude Code, or Google `agy` CLI) to NexWiki, you can choose between two transport models:

1. **Streamable HTTP (Recommended 🚀)**:
   Connects the client directly to your active running web server on port `8080` (at `http://localhost:8080/api/mcp`).
   * **Advantages**: Zero process overhead, and **completely avoids database file lock contentions** (since the active running Go server process maintains exclusive locks, and all clients share it over HTTP).
2. **Stdio (Process-Based Alternative 📦)**:
   The client spawns its own isolated background process of the `nexwiki` Go executable on demand.
   * **Disadvantage**: Since each Stdio client spawns a separate binary process, they might compete to acquire exclusive database/search index file locks if the active web server is already running, which can trigger file-locking errors. Use this only if you aren't running the web server interface.

---

### 1. Cursor IDE (Streamable HTTP Connection - Preferred)
NexWiki implements the modern **Streamable HTTP** transport (2025 Spec) at `/api/mcp`.

To connect Cursor:
1. Open **Cursor Settings** (gear icon in the top-right corner).
2. Go to **Features** -> **MCP**.
3. Click **+ Add New MCP Server**.
4. Configure the server:
   * **Name**: `nexwiki`
   * **Type**: `Streamable HTTP` *(Note: select `SSE` as a fallback if your Cursor version does not list the new 2025 Streamable HTTP type yet)*
   * **URL**: `http://localhost:8080/api/mcp`
5. Click **Save**.

---

### 2. Claude Desktop (Preferred: Streamable HTTP)
Locate your Claude Desktop configuration file (`claude_desktop_config.json`):
* **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add the `nexwiki` server configuration block:

#### Option A: Streamable HTTP (Recommended)
```json
{
  "mcpServers": {
    "nexwiki": {
      "url": "http://localhost:8080/api/mcp"
    }
  }
}
```

#### Option B: Stdio Process Fallback
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "/path/to/your/compiled/nexwiki",
      "args": [
        "-data", "/path/to/your/wiki-data",
        "-name", "My Personal Brain"
      ]
    }
  }
}
```

---

### 3. Claude Code CLI (Preferred: Streamable HTTP)
Anthropic's terminal agent **Claude Code** (`claude` CLI) can dynamically connect to the active NexWiki server over HTTP/SSE.

#### Option A: Streamable HTTP (Recommended)
Run this command in your shell to register the running server:
```bash
claude mcp add nexwiki http://localhost:8080/api/mcp
```

#### Option B: Stdio Process Fallback
```bash
claude mcp add nexwiki -- /path/to/your/compiled/nexwiki -data /path/to/your/wiki-data -name "My Personal Brain"
```

---

### 4. GitHub Copilot CLI (Preferred: Streamable HTTP)
GitHub Copilot's CLI environment supports connecting to custom HTTP/SSE servers. Add this block to your Copilot config file (`~/.config/github-copilot/config.json`):

#### Option A: Streamable HTTP (Recommended)
```json
{
  "mcpServers": {
    "nexwiki": {
      "url": "http://localhost:8080/api/mcp"
    }
  }
}
```

#### Option B: Stdio Process Fallback
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "/path/to/your/compiled/nexwiki",
      "args": [
        "-data", "/path/to/your/wiki-data",
        "-name", "My Personal Brain"
      ]
    }
  }
}
```

---

### 5. Google's `agy` CLI / Antigravity CLI (Preferred: Streamable HTTP)
Google's advanced agentic developer tool **`agy` CLI** supports direct connection to active HTTP/SSE MCP endpoints.

To register NexWiki inside your Antigravity pipeline:

#### Option A: Streamable HTTP (Recommended)
* **Via CLI**:
  ```bash
  agy mcp add nexwiki http://localhost:8080/api/mcp
  ```
* **Via Config File** (`/Users/chris/.gemini/antigravity-cli/config.json`):
  ```json
  {
    "mcpServers": {
      "nexwiki": {
        "url": "http://localhost:8080/api/mcp"
      }
    }
  }
  ```

#### Option B: Stdio Process Fallback
* **Via CLI**:
  ```bash
  agy mcp add nexwiki -- /path/to/your/compiled/nexwiki -data /path/to/your/wiki-data -name "My Personal Brain"
  ```


