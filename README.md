# NexWiki 🚀

NexWiki is an elegant, lightning-fast personal and collaborative knowledge base written in **Go** with a modern embedded **React + TypeScript** frontend. It serves as a zero-dependency, self-contained wiki server that preserves your content as standard, human-readable Markdown files. 

Beyond acting as a traditional wiki, NexWiki features an **always-on Model Context Protocol (MCP) server** supporting both standard Stdio and the SSE HTTP transport. This lets AI agents (like Claude, Cursor, and custom tools) instantly query, read, and explore your knowledge base using semantic tools, turning your personal wiki into an active AI second brain.

---

## ✨ Features

- **📦 Zero-Dependency Single Binary**: Frontend compiled assets are embedded directly inside the Go web server executable using Go's `go:embed`. No external asset servers required.
- **⚡ Modern Responsive UI**: A sleek, high-fidelity Single Page Application (SPA) built using React 19, TypeScript, Vite, Lucide Icons, and styled with Tailwind CSS (v3).
- **🤖 Built-in MCP Server**: Exposes standard Model Context Protocol tools (`search_wiki`, `read_article`, `list_articles`) to AI clients via Stdio and HTTP/SSE.
- **🔍 Blazing-Fast Full-Text Search**: Powered by the robust `github.com/blevesearch/bleve/v2` engine. Supports advanced query parsing, scoring, and text snippet highlighting.
- **📂 Flat-File Markdown Storage**: Wiki pages are stored on disk as plain Markdown files with YAML-like front matter metadata. Your files remain completely portable and easily readable by external editors.
- **🖼️ Asset & Image Uploads**: Built-in support for uploading and referencing media assets (such as PNG, JPEG, GIF, SVG, and WebP) directly within articles.
- **⚙️ Dynamic Customization**: Easily personalize your wiki's name via environment variables (`WIKI_NAME`) or command-line flags.
- **🔒 Development Safety**: System logs are directed exclusively to standard error (`Stderr`) to prevent stdout corruption, guaranteeing stable MCP JSON-RPC Stdio piping.

---

## 🐳 Running on Localhost Using Docker

The fastest way to get NexWiki up and running locally is using **Docker** and **Docker Compose**.

### 1. Prerequisites
Ensure you have [Docker Desktop](https://www.docker.com/products/docker-desktop/) installed and running on your machine.

### 2. Run with Docker Compose (Recommended)
We provide a standard `docker-compose.yml` that mounts a persistent local data volume to preserve your wiki articles.

> 💡 **Tip:** When making code updates during local development, run `docker compose up -d --build` to automatically rebuild the image with your latest changes and deploy the updated container in the background (detached mode).

1. Navigate to the project root directory.
2. Run the following command:
   ```bash
   docker compose up --build
   ```
3. Once the build and startup completes, open your browser and navigate to:
   ```
   http://localhost:8080
   ```
4. You will see your newly initialized wiki with a default seeded homepage ready to edit!

### 3. Run with Docker CLI (Manual)
If you prefer running the container manually without compose:

1. **Build the Docker Image**:
   ```bash
   docker build -t nexwiki:latest .
   ```
2. **Run the Container**:
   ```bash
   docker run -d \
     -p 8080:8080 \
     -v "$(pwd)/my-wiki-data:/app/data" \
     -e WIKI_NAME="My Personal Wiki" \
     --name personal-wiki \
     --restart unless-stopped \
     nexwiki:latest
   ```

### Understanding the Volume Mount (`/app/data`)
The Docker container maps `/app/data` to your local machine (`./my-wiki-data` in compose or the path specified in CLI). This directory contains:
- `articles/` - All your Markdown wiki files (e.g., `home.md`, `setup-guide.md`).
- `assets/` - Uploaded images and media attachments grouped by article.
- `search.bleve/` - The Bleve full-text search index database.

---

## 🛠️ Local Development (Without Docker)

If you are a developer looking to modify the Go backend or React frontend locally, you can run them directly on your machine.

### Prerequisites
- **Go**: 1.26 or later
- **Node.js**: 20.x or later (includes `npm`)

### 1. Build the Frontend
To compile the static React assets so the Go server can embed them, you can choose one of the following paths:

**Option A: Manual CLI Commands**
```bash
cd frontend
npm install
npm run build
cd ..
```

**Option B: Makefile Command**
```bash
make build-frontend
```

### 2. Build & Start the Backend
Once the frontend assets exist in `frontend/dist/`, you can compile and start the Go server:

**Option A: Manual CLI Commands**
```bash
go build -o nexwiki main.go
./nexwiki -port=8080 -data=./data -name="NexWiki Development"
```

**Option B: Makefile Command**
*(This compiles both the frontend assets and backend binary in a single command)*
```bash
make
./nexwiki -port=8080 -data=./data -name="NexWiki Development"
```
Now, you can access the combined app at `http://localhost:8080`.

### 3. Frictionless Frontend Dev Mode (Hot-Reloading)
For active frontend development, you don't want to rebuild every time. Instead, run Vite's development server:
```bash
# Terminal 1: Run Vite's hot-reloading dev server
cd frontend
npm run dev

# Terminal 2: Run Go API backend server
go run main.go -port=8080 -data=./data
```
The Go backend includes a built-in CORS middleware that automatically permits requests from Vite's local dev server (`http://localhost:5173`).

---

## 🛠️ Build Automation & Multi-Platform Cross-Compilation (Makefile)

We provide a robust `Makefile` to simplify frontend compilation, local builds, Docker controls, and cross-compiling the self-contained zero-dependency binary for various architectures.

> 💡 **Tip:** Always make sure the frontend assets are compiled (`make build-frontend`) before running compilation steps, since Go's standard `embed` library will fail to build if `frontend/dist/` is empty. The Makefile cross-compilation targets automatically trigger this step for you.

### Core Developer Targets
* **Build Everything (Frontend + Backend for Host)**:
  ```bash
  make
  # or: make all
  ```
* **Clean Artifacts**: Removes the host binary, `bin/` directory, and compiled frontend assets:
  ```bash
  make clean
  ```

### Docker Compose Automation
* **Build and Spin Up Containers in background**:
  ```bash
  make docker-up
  ```
* **Shut Down Container Service**:
  ```bash
  make docker-down
  ```
* **Build Raw Docker Image**:
  ```bash
  make docker-build
  ```

### Cross-Compilation Targets
All cross-compiled binaries are saved inside the `./bin/` directory:
* **Windows (AMD64)**:
  ```bash
  make build-windows-amd64
  ```
* **Linux (AMD64)**:
  ```bash
  make build-linux-amd64
  ```
* **Linux (ARM64)**:
  ```bash
  make build-linux-arm64
  ```
* **macOS (ARM64 / Apple Silicon)**:
  ```bash
  make build-macos-arm64
  ```
* **Compile for All Platforms Simultaneously**: Builds binaries for all of the above operating systems and architectures in one go:
  ```bash
  make build-all-platforms
  ```

---

## 🤖 Connecting to AI Agents via MCP

Because NexWiki contains an embedded Model Context Protocol (MCP) server, you can attach it to your favorite AI tools to query your personal wiki.

### Connecting Claude Desktop (Stdio)
To allow Claude Desktop to search and read your wiki pages, add the following to your Claude Desktop configuration file (typically located at `~/Library/Application Support/Claude/claude_desktop_config.json` on macOS or `%APPDATA%\Claude\claude_desktop_config.json` on Windows):

**Option A: Running via Docker**
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

**Option B: Running the Go Binary directly**
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "/path/to/your/compiled/nexwiki",
      "args": ["-data", "/path/to/your/wiki-data"]
    }
  }
}
```

### Connecting over HTTP/SSE
NexWiki supports the streamable HTTP/SSE transport (2025 Spec) at `/api/mcp`. This allows modern MCP clients to connect over the network rather than stdio pipes.

---

## 🚢 Production Deployment

When deploying NexWiki for production use, containerized deployments are highly recommended due to the zero-dependency nature of the single compiled binary.

### 1. Core Deployment Requirements
- **Persistent Volume**: Since NexWiki stores articles as flat files and hosts the Bleve database on disk, **you must mount a persistent volume** to `/app/data`. If using cloud platforms (like AWS ECS, GCP Cloud Run, fly.io, or DigitalOcean), make sure to attach a persistent block store or network file share (like EFS or GCP Persistent Disk).
- **Environment Variables**:
  - `WIKI_NAME`: Configure the title of your wiki shown on the page and in the HTML headers (e.g. `WIKI_NAME="Company Knowledge Base"`).

### 2. Production Docker Compose Setup
Create a `docker-compose.prod.yml` behind a reverse proxy:

```yaml
version: '3.8'

services:
  wiki:
    image: nexwiki:latest  # Or pull from your container registry
    container_name: production-wiki
    environment:
      - WIKI_NAME=Company Wiki
    volumes:
      - wiki-prod-data:/app/data
    ports:
      - "8080:8080"
    restart: always

volumes:
  wiki-prod-data:
    driver: local
```

### 3. Setting Up SSL & Reverse Proxy (Caddy / Nginx)
It is highly recommended to terminate SSL (HTTPS) before requests reach the NexWiki server. Below is a simple config snippet if you are using [Caddy](https://caddyserver.com/) as a secure reverse proxy:

```caddy
wiki.yourdomain.com {
    reverse_proxy localhost:8080
}
```

If using **Nginx**, make sure to enable SSE headers for the `/api/mcp` endpoint if you plan to query the MCP server over HTTP:

```nginx
server {
    listen 443 ssl;
    server_name wiki.yourdomain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    # Enable Server-Sent Events (SSE) buffering bypass for HTTP MCP
    location /api/mcp {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Connection '';
        chunked_transfer_encoding off;
        proxy_read_timeout 24h;
    }
}
```
