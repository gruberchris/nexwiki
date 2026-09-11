# NexWiki Docker Deployment Guide 🐳

NexWiki publishes official, ready-to-run multi-platform Docker container images to the GitHub Container Registry (GHCR) on every release. Using Docker provides an isolated, zero-dependency environment for running your personal wiki and Model Context Protocol (MCP) server across Linux, macOS, and Windows.

---

## 📦 Container Registry & Architecture

* **Image Repository**: [`ghcr.io/gruberchris/nexwiki`](https://github.com/gruberchris/nexwiki/pkgs/container/nexwiki)
* **Supported Architectures**:
  * `linux/amd64` (Standard x86_64 servers and PCs)
  * `linux/arm64` (ARM64 servers, Raspberry Pi 4/5, and native Apple Silicon M-series via Docker Desktop)
* **Image Tagging**:
  * `latest`: The latest stable released version.
  * Semantic Version tags (e.g., `0.12.0`, `1.0.0`): Pinned historical releases.

> ℹ️ **Image Footprint**: NexWiki's Docker image is built using a multi-stage Alpine Linux base containing only CA certificates, timezone data, and the single compiled Go static binary. Total image download size is under 30 MB.

---

## 🚀 Quickstart: Running with `docker run`

### 1. Minimal Deployment
To run NexWiki with standard defaults, map port `5808` and mount a local folder for data persistence:

```bash
docker run -d \
  --name nexwiki \
  -p 5808:5808 \
  -v "$(pwd)/wiki-data:/app/data" \
  --restart unless-stopped \
  ghcr.io/gruberchris/nexwiki:latest
```

Open your browser to `http://localhost:5808` to view the initial seeded homepage.

---

### 2. Advanced `docker run` Invocation
You can pass custom wiki settings, specify a different port, enable seasonal theme scheduling, and adjust retention policies using environment variables and entrypoint arguments:

```bash
docker run -d \
  --name personal-brain \
  -p 9090:9090 \
  -v "/var/lib/nexwiki:/app/data" \
  -e NEXWIKI_NAME="Engineering Second Brain" \
  -e NEXWIKI_THEME="midnight" \
  -e NEXWIKI_THEME_SCHEDULING="true" \
  -e NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS="60" \
  -e NEXWIKI_SECRET_SCAN="refuse" \
  --restart unless-stopped \
  ghcr.io/gruberchris/nexwiki:latest \
  -port=9090
```

> ⚠️ **Port Mapping Note**: When overriding the in-container listening port with trailing CLI flags (e.g. `-port=9090`), ensure you adjust the Docker host-to-container port mapping accordingly (`-p 9090:9090`).

---

## 🐙 Running with Docker Compose (Recommended)

Docker Compose simplifies multi-environment configuration, volume management, and lifecycle control.

Create a `docker-compose.yml` file:

```yaml
version: "3.8"

services:
  nexwiki:
    image: ghcr.io/gruberchris/nexwiki:latest
    container_name: nexwiki
    restart: unless-stopped
    ports:
      - "5808:5808"
    environment:
      - NEXWIKI_NAME=My Personal Brain
      - NEXWIKI_THEME=default
      - NEXWIKI_THEME_SCHEDULING=true
      # - NEXWIKI_ALLOWED_ORIGINS=https://wiki.example.com
      - NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS=90
      - NEXWIKI_PLAN_DELETE_AFTER_DAYS=365
      - NEXWIKI_SECRET_SCAN=refuse
    volumes:
      # Named volume (recommended for Docker-managed storage)
      - nexwiki_data:/app/data
      # Or use a local host bind-mount:
      # - ./data:/app/data

volumes:
  nexwiki_data:
    driver: local
```

### Starting and Managing the Stack
```bash
# Start container in the background
docker compose up -d

# View real-time container logs
docker compose logs -f

# Pull new updates and recreate container
docker compose pull && docker compose up -d

# Stop container cleanly
docker compose down
```

---

## 💾 Volume Persistence Architecture (`/app/data`)

NexWiki persists all state on disk inside the `/app/data` directory. When running in Docker, **you must mount a persistent volume to `/app/data`**; otherwise, your wiki articles, search indexes, and uploads will be erased if the container is destroyed or upgraded.

### Directory Structure Inside `/app/data`:
```
/app/data/
├── articles/           # All wiki pages, memories, plans, and skills (.md files)
├── assets/             # Uploaded media (images, diagrams) grouped by article slug
├── search.bleve/       # Bleve full-text indexing engine database
├── activity.jsonl      # Real-time append-only activity audit trail
└── custom_themes.json  # UI-customized themes palette (created on theme save)
```

### Storage Details:
* **`articles/`**: Contains raw markdown files with OKF v0.2 YAML front matter. These files are human-readable, version-controlled, and can be backed up or edited with external text editors.
* **`assets/`**: Uploaded attachments are segregated into folders matching article slugs (e.g., `assets/project-phoenix/diagram.png`).
* **`search.bleve/`**: The local search database holds an exclusive file lock while running. NexWiki handles graceful shutdown (`SIGTERM` / `SIGINT`), flushing and closing the Bleve database safely within Docker's 10-second shutdown window.
* **`activity.jsonl`**: Rotates automatically into timestamped `activity-<UTC>.jsonl` archives whenever the active log exceeds 10 MB. Archived logs are retained according to `NEXWIKI_ACTIVITY_MAX_ARCHIVES`.

---

## ⚙️ Container Configuration

### 1. Environment Variables
All configuration options supported by NexWiki can be passed into the container via `-e` flags or an `.env` file:

| Variable | Default | Purpose |
|---|---|---|
| `NEXWIKI_NAME` | `NexWiki` | Custom display title shown in web UI and HTML headers |
| `NEXWIKI_THEME` | `default` | Initial active color palette (`default`, `midnight`, `forest`, `nordic`, etc.) |
| `NEXWIKI_THEME_SCHEDULING` | `false` | Enable automatic annual seasonal theme swapping |
| `NEXWIKI_BIND` | `0.0.0.0` | Default interface inside containers (do not change unless binding specific bridge IPs) |
| `NEXWIKI_ALLOWED_ORIGINS` | Loopback only | Comma-separated DNS origins allowed to access the API from browsers |
| `NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS` | `0` (disabled) | Startup sweep retention for archived articles |
| `NEXWIKI_PLAN_LIFECYCLE_INTERVAL_DAYS` | `1` | Background sweep frequency for AI plan transitions |
| `NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS` | `90` | Days until completed plans are archived |
| `NEXWIKI_PLAN_DELETE_AFTER_DAYS` | `365` | Days until archived plans are purged |
| `NEXWIKI_PLAN_LIFECYCLE_DRY_RUN` | `false` | Enable dry-run logging without executing plan status changes |
| `NEXWIKI_SECRET_SCAN` | `refuse` | Credential scanner disposition (`refuse`, `warn`, or `off`) |
| `NEXWIKI_ACTIVITY_MAX_ARCHIVES` | unlimited | Retention count for rotated activity archives |

### 2. Entrypoint and Trailing Arguments
The official `Dockerfile` defines:
```dockerfile
ENTRYPOINT ["/app/nexwiki", "-port=5808", "-data=/app/data"]
```
Any command arguments passed after the image name in `docker run` are appended to the entrypoint invocation. For example:
```bash
docker run -d -p 5808:5808 -v $(pwd)/data:/app/data ghcr.io/gruberchris/nexwiki:latest -theme="midnight"
```
Because trailing CLI flags take effect during argument parsing, you can customize or override flags directly.

---

## 🤖 Stdio MCP Subprocess via Docker Exec

If you want to run an MCP client (such as Claude Desktop) on your host machine and connect it to a NexWiki instance running inside Docker via standard input/output (stdio):

```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "docker",
      "args": [
        "exec",
        "-i",
        "nexwiki",
        "/app/nexwiki",
        "-mcp-only",
        "-data",
        "/app/data"
      ]
    }
  }
}
```

### Why `-mcp-only` Is Essential:
`docker exec` spawns a secondary process inside the container. If that secondary process attempts to start a web server, it will fail when attempting to bind port `5808`. Passing `-mcp-only` instructs the binary to run strictly as a JSON-RPC stdio server. It detects the running primary web process and proxies MCP calls to it, preventing Bleve database lock collisions.

---

## 🛠️ Automated Runner Scripts

For users who prefer running NexWiki in Docker without writing compose files manually, NexWiki provides automated cross-platform helper scripts:
* **Linux / macOS**: `scripts/docker-run.sh`
* **Windows (PowerShell)**: `scripts/docker-run.ps1`

These scripts automatically:
1. Detect your host operating system and resolve the standard user data directory (`~/.config/nexwiki/nexwiki-data` or `%AppData%\nexwiki\nexwiki-data`).
2. Pull the latest release image (`ghcr.io/gruberchris/nexwiki:latest`).
3. Gracefully stop and remove any existing `nexwiki` container.
4. Launch a new container with volume mapping and port configuration.

For full instructions on using runner scripts, see the [Install Scripts Guide](./install_scripts_guide.md).
