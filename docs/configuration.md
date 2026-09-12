# NexWiki Configuration Guide ⚙️

NexWiki is designed to be zero-configuration out of the box while providing granular configuration options through command-line flags and environment variables. This guide outlines every configuration option, precedence rules, storage path resolution, security implications, and practical usage examples.

---

## 🧭 Overview & Configuration Model

NexWiki supports two primary mechanisms for configuration:
1. **Command-Line (CLI) Flags**: Passed directly when executing the `nexwiki` binary.
2. **Environment Variables**: Prefixed exclusively with **`NEXWIKI_`** in compliance with NexWiki's governance standards.

### Precedence Hierarchy
When both an environment variable and a CLI flag are supplied for the same setting, **the environment variable takes precedence**:

$$\text{Environment Variable (\texttt{NEXWIKI\_*})} > \text{CLI Flag} > \text{Default Value}$$

For example, running:
```bash
NEXWIKI_NAME="Primary Knowledge Base" ./nexwiki -name="Scratchpad"
```
will name the wiki `"Primary Knowledge Base"`.

---

## 📋 Complete Configuration Reference

| Setting | CLI Flag | Environment Variable | Default Value | Description |
|---|---|---|---|---|
| **HTTP Port** | `-port` | — | `5808` | TCP port on which the web server and HTTP endpoints listen. |
| **Data Directory** | `-data` | — | OS-aware default (see below) | Absolute or relative filesystem path where markdown articles, uploaded media assets, Bleve search index, and activity logs are stored. Inside Docker containers, this is overridden to `/app/data` via `ENTRYPOINT`. |
| **Wiki Name / Title** | `-name` | `NEXWIKI_NAME` | `NexWiki` | Custom display title shown in the web navigation bar, page headings, and HTML `<title>` tags. |
| **Default Theme** | `-theme` | `NEXWIKI_THEME` | `default` | Initial active color palette theme. Can be an embedded theme (`default`, `midnight`, `forest`, `lavender`, `nordic`, `sepia`, etc.) or a custom theme created in the UI. |
| **Seasonal Themes** | `-theme-scheduling` | `NEXWIKI_THEME_SCHEDULING` | `false` | When enabled (`true` or `1`), automatically switches themes according to annual date windows (Independence Day, Halloween, Christmas, New Year). |
| **Stdio MCP-Only Mode** | `-mcp-only` | `NEXWIKI_MCP_ONLY` | `false` | Run as a pure stdio Model Context Protocol (MCP) server, completely skipping the HTTP network bind. Used when spawning a stdio subprocess (e.g., Claude Desktop) alongside an already-running web server. If an existing web server is detected on the configured port, the MCP process proxies requests directly to it to avoid index file locks. |
| **Launch in Browser** | `-launch-in-browser` | `NEXWIKI_LAUNCH_BROWSER` | `false` | Automatically opens the wiki URL in the system's default web browser once the server successfully starts (ignored when `-mcp-only` is active). |
| **Network Bind Host** | `-bind` | `NEXWIKI_BIND` | `127.0.0.1` (native) / `0.0.0.0` (Docker) | Network IP interface to bind. Defaults to `127.0.0.1` for local desktop security. Inside containers, defaults to `0.0.0.0` to permit container port publishing. Set to `0.0.0.0` to listen on all interfaces. |
| **Agent Attribution Fallback** | `-agent-name` | `NEXWIKI_AGENT_NAME` | `(unset)` | Fallback attribution identity recorded in the activity log for MCP clients that do not supply client metadata in `clientInfo`. Clients providing `clientInfo` are always recorded under their own reported name. *(Note: This is distinct from `-name`, which is the wiki title).* |
| **WikiSkill Evolution Role** | `-wikiskill-role` | `NEXWIKI_WIKISKILL_ROLE` | `(unset)` | Process-wide least-privilege role for the MCP layer: `inference` (read skills + append raw memories only), `maintainer` (raw + wiki read/write), or `proposer` (wiki read + candidate propose). No phase may publish/promote skills or write audit entries; refusals fail with a `lacks scope` error audited as a `deny` event. Unset means unrestricted. |
| **Job CLI Profiles File** | — | `NEXWIKI_JOB_PROFILES_FILE` | `(unset)` | Path to a JSON file extending the built-in headless job-runner CLI profiles (`{"profiles": {"name": {"binary": "opencode", "args": [...], "env_allowlist": [...], "step_timeout_secs": 600}}}`). Profiles are allowlisted command templates from server config only — never wiki docs. An arbitrary template (shell binary, path, interpolation placeholder, unknown binary) refuses the whole process at startup. Built-ins: `opencode` and `claude-code`; `copilot` is a planned extension hook, not yet executable. |
| **Job Max Concurrent** | — | `NEXWIKI_JOB_MAX_CONCURRENT` | `1` | How many headless evolution jobs may run at once (same-host spawn, no PTY, stdout/stderr captured to files). Maximum `2`; higher values refuse startup. |
| **Job Step Timeout** | — | `NEXWIKI_JOB_STEP_TIMEOUT_SECS` | `600` | Per-step timeout in seconds for one harness spawn. Must not exceed the 90-minute run cap. |
| **Allowed Browser Origins** | — | `NEXWIKI_ALLOWED_ORIGINS` | Loopback only | Comma-separated list of browser origins permitted to communicate with the API (e.g., `https://wiki.example.com`). Required when serving NexWiki from a custom DNS domain name. |
| **Archive Auto-Delete** | — | `NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS` | `0` (disabled) | Number of days after an article is archived before it is permanently deleted during server startup sweeps. |
| **Plan Lifecycle Interval** | — | `NEXWIKI_PLAN_LIFECYCLE_INTERVAL_DAYS` | `1` | Interval (in days) between automated sweeps by the background plan lifecycle worker. A sweep also runs once at startup. Minimum value is `1`. |
| **Plan Auto-Archive** | — | `NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS` | `90` | Days a collaborative AI plan remains in `completed` or `superseded` status before being automatically transitioned to `archived`. Set to `0` to disable. |
| **Plan Auto-Delete** | — | `NEXWIKI_PLAN_DELETE_AFTER_DAYS` | `365` | Days an AI plan remains in `archived` status before being permanently deleted. Set to `0` to disable. **Safety Guard:** Plans referenced by inbound links from other documents are never deleted. |
| **Plan Lifecycle Dry-Run** | — | `NEXWIKI_PLAN_LIFECYCLE_DRY_RUN` | `false` | When `true` or `1`, the plan lifecycle worker evaluates and logs planned transitions to stderr and the activity log without applying changes to disk. |
| **Secret Scanning** | — | `NEXWIKI_SECRET_SCAN` | `refuse` | Disposition when an AI agent write contains credential-shaped text: `refuse` (reject the write with a descriptive error), `warn` (persist the write but add an alert note), or `off` (disable checks). Any unrecognized value falls back safely to `refuse`. |
| **Activity Archive Cap** | — | `NEXWIKI_ACTIVITY_MAX_ARCHIVES` | unlimited | Maximum number of rotated `activity-<UTC>.jsonl` log archives retained in the data directory. When log files exceed 10 MB, they are rotated; setting this cap purges the oldest rotated files beyond the limit. |

---

## 📁 OS-Aware Default Data Directory

If the `-data` flag is not specified, NexWiki selects an operating-system-appropriate directory for data storage:

### 1. Linux & macOS
1. When `XDG_CONFIG_HOME` is defined as an absolute path:
   ```
   $XDG_CONFIG_HOME/nexwiki/nexwiki-data
   ```
2. Otherwise, user home directory fallback:
   ```
   ~/.config/nexwiki/nexwiki-data
   ```
3. Fallback if user home cannot be resolved:
   ```
   ./data
   ```
> **Note on macOS**: NexWiki deliberately defaults to `~/.config/nexwiki/nexwiki-data` on macOS (rather than `~/Library/Application Support`) to maintain parity across POSIX environments and terminal workflows.

### 2. Windows
1. User configuration directory (`os.UserConfigDir()`):
   ```
   %AppData%\nexwiki\nexwiki-data
   (Typically: C:\Users\<Username>\AppData\Roaming\nexwiki\nexwiki-data)
   ```
2. Fallback to user profile:
   ```
   %USERPROFILE%\.config\nexwiki\nexwiki-data
   ```
3. Fallback if user directories cannot be resolved:
   ```
   .\data
   ```

### 3. Containerized Deployments (Docker)
The official Dockerfile sets an explicit entrypoint:
```dockerfile
ENTRYPOINT ["/app/nexwiki", "-port=5808", "-data=/app/data"]
```
Therefore, in Docker, the data directory is consistently `/app/data`, which must be mounted to a persistent volume.

---

## 🔒 Trust Model & Security Governance

Before configuring and exposing NexWiki, understand the system trust model:

### 1. Unauthenticated Architecture
NexWiki has **no built-in user accounts, passwords, session tokens, or API keys**. Any client with network access to the listening port has complete read, write, and delete access across all articles, assets, and MCP tools (including `delete_wiki_article` and bundle imports).

* **Designed Scope**: Single-user operation on a trusted local workstation or private virtual network (Tailscale, WireGuard, private VPC).
* **Public Internet Exposure**: **Never** expose NexWiki directly to the public internet without an authenticating reverse proxy (e.g., Caddy with `basic_auth`, OAuth2 Proxy, Cloudflare Access) or VPN.

### 2. Network Interface Binding
* **Native Desktop Execution**: Defaults to loopback (`127.0.0.1`), ensuring unauthenticated access is confined to the local machine.
* **Containerized Execution**: Automatically detects container environments (e.g., presence of `/.dockerenv` or container cgroups) and defaults to `0.0.0.0` so Docker port mapping (`-p 5808:5808`) operates properly.
* **Manual Override**: Use `-bind=0.0.0.0` or `NEXWIKI_BIND=0.0.0.0` to allow connections across a local LAN.

### 3. Browser Origin Validation (`NEXWIKI_ALLOWED_ORIGINS`)
Because NexWiki is unauthenticated, web applications running in other browser tabs could theoretically attempt Cross-Origin Resource Sharing (CORS) attacks against `http://localhost:5808`.

NexWiki blocks this by inspecting the `Origin` header of incoming HTTP requests:
* **Allowed by Default**:
  - Non-browser requests (no `Origin` header, such as `curl`, MCP clients, Python scripts).
  - Loopback origins (`http://localhost:*`, `http://127.0.0.1:*`, Vite dev server on `:5173`).
  - Same-origin requests where the host is an IP address (e.g., connecting from a mobile phone via `http://192.168.1.50:5808`).
* **Custom Domain Deployments**:
  When proxying NexWiki behind a domain name (e.g., `https://wiki.internal.net`), browser requests will carry `Origin: https://wiki.internal.net`. NexWiki will reject these requests with `403 Forbidden` unless the domain is registered:
  ```bash
  export NEXWIKI_ALLOWED_ORIGINS="https://wiki.internal.net,https://notes.internal.net"
  ```

### 4. Secret Scanning (`NEXWIKI_SECRET_SCAN`)
When AI agents perform writes via MCP (`create_wiki_article`, `edit_wiki_article`, `create_agent_memory`, `create_agent_plan`, `create_agent_skill`, `import_okf_bundle`), NexWiki analyzes the content, description, and source fields for credential patterns (private keys, AWS/GitHub/OpenAI/Anthropic tokens, Slack webhooks, JWTs):
* `refuse` (default): Immediately aborts the tool call, returning an actionable error message with the byte offset and pattern type. **The actual secret value is never echoed back in the error**, preventing it from entering agent logs or transcripts.
* `warn`: Accepts the write but appends a warning to the tool response.
* `off`: Disables pattern scanning.

---

## 💡 Practical Configuration Examples

### Example 1: Local Development / Custom Data Directory
Run NexWiki on port `9000`, storing data in a dedicated workspace folder:
```bash
./nexwiki -port=9000 -data=/home/chris/notes/wiki-data -name="Research Brain"
```

### Example 2: Headless Stdio Subprocess (Claude Desktop)
When configuring Claude Desktop to spawn a local NexWiki stdio subprocess while a web server is already running on `:5808`:
```json
{
  "mcpServers": {
    "nexwiki": {
      "command": "/usr/local/bin/nexwiki",
      "args": ["-mcp-only", "-port=5808"]
    }
  }
}
```
The subprocess detects the primary web instance on port `5808`, operates in proxy mode, and routes all tool invocations through the running primary without colliding on database locks.

### Example 3: Private LAN Server with Seasonal Themes
Launch NexWiki accessible across your home lab LAN on `0.0.0.0`, with seasonal theme swaps enabled:
```bash
NEXWIKI_BIND="0.0.0.0" \
NEXWIKI_THEME_SCHEDULING="true" \
NEXWIKI_NAME="Family Wiki" \
./nexwiki -port=5808 -data=/var/lib/nexwiki
```

### Example 4: Domain Reverse Proxy Environment File
When running containerized behind Caddy or Nginx with a custom domain and plan retention policies:
```env
# /etc/nexwiki/nexwiki.env
NEXWIKI_NAME=Engineering Knowledge Base
NEXWIKI_THEME=nordic
NEXWIKI_ALLOWED_ORIGINS=https://wiki.mycorp.internal
NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS=60
NEXWIKI_PLAN_DELETE_AFTER_DAYS=180
NEXWIKI_ACTIVITY_MAX_ARCHIVES=10
NEXWIKI_SECRET_SCAN=refuse
```
Pass this environment file to Docker:
```bash
docker run -d \
  --env-file /etc/nexwiki/nexwiki.env \
  -p 5808:5808 \
  -v /var/data/nexwiki:/app/data \
  --name nexwiki \
  ghcr.io/gruberchris/nexwiki:latest
```

### Example 5: Windows PowerShell Launch
Launch NexWiki on Windows with custom name, browser launch enabled, and specific data path:
```powershell
$env:NEXWIKI_NAME = "Personal Notebook"
$env:NEXWIKI_LAUNCH_BROWSER = "true"
.\nexwiki.exe -port=5808 -data="D:\KnowledgeBase\nexwiki-data"
```
