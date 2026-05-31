# AI Agents & Model Context Protocol (MCP) in NexWiki 🤖

NexWiki is designed as an **AI-ready second brain**. In addition to providing a beautiful personal knowledge base web application, NexWiki runs an **always-on Model Context Protocol (MCP) server**. 

This protocol acts as a standardized bridge allowing AI agents (like Claude Desktop, Cursor, Windsurf, or custom LLM systems) to query, read, and explore your personal wiki in real-time. By connecting your agent to NexWiki, you empower it to reason with access to your entire personal knowledge base.

---

## 📚 Project Documentation Guide

If you are a developer or an AI agent interacting with this repository, please observe the following documentation hierarchy:
* **Developer Setup & Quickstart**: Refer to the root [README.md](./README.md) for everything needed to get NexWiki up and running for local development, compilation, and building.
* **Technical & Content Guides**: Technical documentation, user manuals, and content guides are located in the [docs/](./docs) directory (e.g., the [docs/user_guide.md](./docs/user_guide.md)).
* **AI & MCP Agent Specifications**: This file ([AGENTS.md](./AGENTS.md)) is strictly reserved for documenting the Model Context Protocol details, exposed AI tools, and integration configurations.

---

## 🏗️ Architecture Overview

NexWiki's MCP implementation is lightweight, robust, and supports two primary transport layers:

1. **Stdio (Standard Input/Output)**: Typically used for local server-agent processes. The agent runs the NexWiki binary or spins up the Docker container directly, piping JSON-RPC 2.0 messages via standard input/output.
2. **HTTP / Server-Sent Events (SSE)**: Enables networked connection over HTTP at the `/api/mcp` endpoint. It uses an SSE channel (`GET`) to stream notifications and updates, combined with standard HTTP `POST` requests to execute synchronous JSON-RPC commands.

```mermaid
graph TD
    subgraph AI Client / Agent
        Claude[Claude Desktop]
        Cursor[Cursor IDE]
        Custom[Custom Agent SDK]
    end

    subgraph NexWiki Server
        MCPEngine[MCP Server Engine]
        Bleve[Bleve Search Index]
        FlatFiles[Flat-File Markdown]
    end

    Claude <-->|Stdio / JSON-RPC| MCPEngine
    Cursor <-->|HTTP/SSE /api/mcp| MCPEngine
    Custom <-->|Stdio or SSE| MCPEngine

    MCPEngine <-->|Full-Text Search| Bleve
    MCPEngine <-->|Read/Write Files| FlatFiles
```

### 🔒 Log Safety Guarantee
In order to prevent stdio pipe corruption (which breaks JSON-RPC communication in tools like Claude Desktop), **NexWiki redirects all internal system and web application logs exclusively to standard error (`Stderr`)**. Only valid JSON-RPC envelopes are ever output to `Stdout`.

---

## 🛠️ Exposed MCP Tools

The NexWiki MCP server registers and exposes three powerful, semantic tools for AI agents:

### 1. `search_wiki`
Performs a high-speed, full-text search across all wiki articles using the built-in **Bleve Search** engine. It supports advanced queries (wildcards, exact phrase quotes, and logical operators).

* **Arguments**:
  * `query` (string, **required**): The search keywords or query string.
* **Output Format**:
  A structured, human-readable summary containing scored matches, slug identifiers, and content snippets. To optimize LLM context usage, all HTML `<mark>` search highlight tags are automatically converted to clean markdown bold formatting (`**`).

* **Example Call (JSON-RPC)**:
  ```json
  {
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "search_wiki",
      "arguments": {
        "query": "setup guide"
      }
    },
    "id": 1
  }
  ```

---

### 2. `read_article`
Retrieves the raw Markdown content and Yaml-style front-matter configurations of a specific article using its URL-safe slug.

* **Arguments**:
  * `slug` (string, **required**): The clean, URL-safe slug of the target article (e.g. `home` or `setup-guide`).
* **Output Format**:
  A plain text document listing the article Title, Slug, Created timestamp, Updated timestamp, and the complete raw Markdown content.

* **Example Call (JSON-RPC)**:
  ```json
  {
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "read_article",
      "arguments": {
        "slug": "setup-guide"
      }
    },
    "id": 2
  }
  ```

---

### 3. `list_articles`
Lists all articles currently available in your knowledge base. This acts as a directory index for the agent to understand what documentation exists.

* **Arguments**: None (empty object `{}`).
* **Output Format**:
  A bulleted plain text index containing the titles, URL-safe slugs, and last-edited timestamps for all active articles.

* **Example Call (JSON-RPC)**:
  ```json
  {
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "list_articles",
      "arguments": {}
    },
    "id": 3
  }
  ```

---

## 🔌 Connecting Popular AI Clients

### 1. Claude Desktop (Stdio Connection)
You can configure your local Claude Desktop app to talk directly to your NexWiki instance. Locate your Claude Desktop configuration file:
* **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add the `nexwiki` server block inside the `mcpServers` object:

#### Option A: Connection via Running Docker Container (Recommended)
If you run NexWiki via Docker with the container name `personal-wiki`:
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "docker",
      "args": ["exec", "-i", "personal-wiki", "/app/nexwiki"]
    }
  }
}
```

#### Option B: Connection via Local Go Binary
If you compiled the binary on your local machine:
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

Restart Claude Desktop, and you will see the **hammer icon 🔨** in the chat window, confirming that the `search_wiki`, `read_article`, and `list_articles` tools are ready to use!

---

### 2. Cursor IDE (HTTP/SSE Connection)
Cursor supports connecting to MCP servers over the network using the HTTP/SSE transport.

1. Open **Cursor Settings** (Gear icon in top right).
2. Go to **Features** -> **MCP**.
3. Click **+ Add New MCP Server**.
4. Configure the server with the following settings:
   * **Name**: `nexwiki`
   * **Type**: `SSE`
   * **URL**: `http://localhost:8080/api/mcp` (or your production domain e.g. `https://wiki.yourdomain.com/api/mcp`)
5. Click **Save**.

Cursor will establish a stream connection and immediately list the three tools in the sidebar. You can now use Cursor Composer or chat (`Cmd+K` / `Ctrl+K`) and reference your wiki directly during code generation!

---

## 💡 Developer Guidelines: Custom Clients

If you are building your own AI agent workflows (using Python, Node.js/TypeScript, or Go), you can interface with NexWiki using standard MCP SDKs.

### Example in Python (using `mcp` SDK)
```python
import asyncio
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

# Define the server parameters
server_params = StdioServerParameters(
    command="docker",
    args=["exec", "-i", "personal-wiki", "/app/nexwiki"]
)

async def query_wiki():
    async with stdio_client(server_params) as (read_stream, write_stream):
        async with ClientSession(read_stream, write_stream) as session:
            # Initialize connection
            await session.initialize()
            
            # List available tools
            tools = await session.list_tools()
            print("Exposed Tools:", [t.name for t in tools.tools])
            
            # Execute a full-text search
            result = await session.call_tool("search_wiki", arguments={"query": "Docker"})
            print("\nSearch Results:\n", result.content[0].text)

asyncio.run(query_wiki())
```

### Direct HTTP Request (Minimal JSON-RPC POST)
If you don't want to use an MCP SDK and prefer standard HTTP requests, you can interact with the server's SSE HTTP endpoint. For execution, issue standard HTTP `POST` requests to `/api/mcp`:

```bash
curl -X POST http://localhost:8080/api/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "list_articles",
      "arguments": {}
    },
    "id": 1
  }'
```

---

## 🧠 Design Tips for AI Agents Interacting with NexWiki

If you are prompting or building an agent to work with NexWiki, teach it these best practices:
1. **Explore First**: Start by running `list_articles` to see an index of what is available, or use `search_wiki` to query specific keywords.
2. **Resolve Slugs Intelligently**: When linking or reading, always use the URL-safe slug (e.g. `setup-guide`) rather than the raw article title.
3. **Handle WikiLinks**: NexWiki files contain internal `[[Double Bracket]]` links. When displaying these to users, agents should resolve them to clean relative links `/articles/target-slug` or explain them as references.
4. **Context Management**: Raw Markdown files can occasionally grow large. Prefer searching first to locate key headings/sections before reading the entire article if context window limits are a concern.
