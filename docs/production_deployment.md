# NexWiki Production Deployment & Reverse Proxy Guide 🚢

This guide covers deploying NexWiki in production environments. Due to NexWiki's zero-dependency compiled binary and embedded frontend, production deployment is straightforward, highly performant, and lightweight. However, because NexWiki relies on a single-user unauthenticated trust model, configuring proper network security, authentication, and reverse proxying is essential.

---

## 🔒 1. Security Architecture & Trust Model

Before deploying NexWiki to any remote server or cloud environment, you **must** understand its security boundaries:

### The Single-User Trust Model
* **No Built-in Authentication**: NexWiki contains **no user accounts, no passwords, no session tokens, and no API keys**. 
* **Full Access Granted**: Any client or browser that can route network traffic to NexWiki's port has unrestricted permissions:
  * Read, edit, and delete any wiki article or media asset.
  * Execute all 38 Model Context Protocol (MCP) tools (including `delete_wiki_article` and bundle imports/exports).
  * Read system activity logs and search indexes.
* **Intended Boundary**: Designed for a single user running on a local workstation (`127.0.0.1`) or inside a secure private network perimeter.

> 🚨 **Critical Warning**: **NEVER expose NexWiki directly to the public internet without an authentication layer.** Terminating TLS (HTTPS) via a reverse proxy encrypts transit traffic but does **not** protect against unauthorized access.

---

## 🛡️ 2. Production Authentication Strategies

To access NexWiki securely from outside your local workstation, enforce authentication before requests reach NexWiki:

### Option A: Private Overlay Network / VPN (Recommended)
The simplest and most secure deployment strategy is keeping NexWiki bound to a private network interface accessible only via an authenticated mesh VPN:
* **Tailscale**: Run NexWiki on a server connected to your private Tailnet, or use Tailscale Serve / Funnel with access controls.
* **WireGuard / OpenVPN**: Restrict access to clients connected to your private network gateway.
* **Private Cloud VPC**: Deploy inside a private subnet without public IP allocation.

### Option B: Authenticating Reverse Proxy
If NexWiki must be reachable over public DNS (e.g., `https://wiki.yourdomain.com`), place an authenticating proxy in front of the application:
1. **Caddy with `basic_auth`**: Built-in HTTP basic authentication with bcrypt-hashed credentials.
2. **Identity-Aware Proxies (IAP)**: Cloudflare Access, Google Cloud IAP, or AWS ALB with OIDC.
3. **OAuth2 / OIDC Forward Auth**: Deploying [oauth2-proxy](https://oauth2-proxy.github.io/oauth2-proxy/), Authelia, or Authentik in front of Nginx or Traefik.

---

## 🌐 3. Browser Origin Acceptance (`NEXWIKI_ALLOWED_ORIGINS`)

Because NexWiki is unauthenticated, it enforces origin validation on all browser requests to prevent Cross-Origin Resource Sharing (CORS) attacks from malicious websites visited in other browser tabs:
* By default, NexWiki permits requests from loopback origins (`localhost`, `127.0.0.1`) and raw IP addresses.
* When accessed through a domain name (e.g., `https://wiki.yourdomain.com`), the user's browser sends:
  ```http
  Origin: https://wiki.yourdomain.com
  ```
  If this domain is not explicitly whitelisted, NexWiki rejects API requests with `403 Forbidden`.

### Configuring the Allowed Origin:
Set the `NEXWIKI_ALLOWED_ORIGINS` environment variable to match your public-facing URL:
```bash
NEXWIKI_ALLOWED_ORIGINS="https://wiki.yourdomain.com"
```
For multiple domains, separate them with commas:
```bash
NEXWIKI_ALLOWED_ORIGINS="https://wiki.yourdomain.com,https://notes.internal.net"
```

---

## 🚦 4. Reverse Proxy Setup

### Option 1: Caddy (Recommended for Simplicity)
[Caddy](https://caddyserver.com/) handles automatic TLS certificate generation via Let's Encrypt and natively proxies long-lived streaming connections without complex buffering flags.

#### 1. Generate a Password Hash
```bash
caddy hash-password
# Enter your password when prompted; copy the generated bcrypt hash
```

#### 2. Configure `Caddyfile`
```caddy
wiki.yourdomain.com {
    # 1. Enforce authentication (Caddy protects the unauthenticated NexWiki instance)
    basic_auth {
        admin $2a$14$replace.with.your.own.bcrypt.hash
    }

    # 2. Reverse proxy to local NexWiki instance
    reverse_proxy localhost:5808
}
```

#### 3. Start NexWiki with Environment Variable
```bash
NEXWIKI_ALLOWED_ORIGINS="https://wiki.yourdomain.com" \
./nexwiki -port=5808 -bind=127.0.0.1
```

---

### Option 2: Nginx Reverse Proxy Configuration

When using Nginx, special attention is required for NexWiki's streaming endpoints:
1. `/api/mcp` (Streamable HTTP Model Context Protocol transport)
2. `/api/activity/stream` (Server-Sent Events feed for live UI updates and zero-refresh dashboard sync)

> ⚠️ **CRITICAL: Streaming Buffer Directives**  
> Nginx buffers upstream responses by default. In SSE and Streamable HTTP, chunked data is pushed continuously over a persistent connection. If buffering is enabled, Nginx will hold responses in internal buffers waiting for the connection to close, causing AI agent tool calls and UI live activity streams to **silently hang indefinitely**.

#### Complete `nginx.conf` Configuration:
```nginx
server {
    listen 80;
    server_name wiki.yourdomain.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name wiki.yourdomain.com;

    # SSL Certificate Configuration
    ssl_certificate /etc/letsencrypt/live/wiki.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/wiki.yourdomain.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # Optional: Basic Authentication at Nginx layer
    auth_basic "Restricted Access";
    auth_basic_user_file /etc/nginx/.htpasswd;

    # Standard Application Routes (Static Assets & REST Endpoints)
    location / {
        proxy_pass http://127.0.0.1:5808;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # CRITICAL: Streaming Endpoints (MCP Streamable HTTP & SSE Live Activity)
    # Buffering MUST be disabled for these endpoints.
    location ~ ^/api/(mcp|activity/stream)$ {
        proxy_pass http://127.0.0.1:5808;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Disable buffering and caching for continuous chunked streams
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Connection '';
        chunked_transfer_encoding off;

        # Extend read timeout to keep long-lived streams open
        proxy_read_timeout 24h;
    }
}
```

---

## 🗄️ 5. Production Storage & Persistent Volumes

NexWiki operates on flat Markdown files (`.md`), uploaded media attachments, and a localized Bleve full-text search index (`search.bleve`).

### Storage Guidelines:
* **Path**: Inside containers, persist `/app/data`. On bare-metal or VMs, configure `-data=/var/lib/nexwiki`.
* **Filesystem Compatibility**: Use POSIX-compliant local block storage (ext4, XFS, APFS, NTFS) or dedicated persistent cloud block storage (AWS EBS, GCP Persistent Disk, DigitalOcean Block Storage).
* **Distributed Network Filesystems**: Network filesystems like NFS or SMB/CIFS that do not fully support POSIX file locking (`fcntl` / `flock`) should be avoided for the `search.bleve/` database directory, as Bleve requires reliable file locking.
* **Backup Strategy**: Backing up NexWiki is as simple as creating a snapshot or tar archive of the data directory. Alternatively, use the built-in OKF bundle export endpoint (`GET /api/okf/export`) or the `export_okf_bundle` MCP tool to create a clean, portable `.zip` backup at any time.

---

## 🐳 6. Production Docker Compose Stack

Here is a turnkey production `docker-compose.prod.yml` running NexWiki behind Caddy with automated HTTPS:

```yaml
version: "3.8"

services:
  caddy:
    image: caddy:2-alpine
    container_name: caddy-proxy
    restart: always
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      - nexwiki

  nexwiki:
    image: ghcr.io/gruberchris/nexwiki:latest
    container_name: nexwiki-app
    restart: always
    environment:
      - NEXWIKI_NAME=Corporate Second Brain
      - NEXWIKI_THEME=nordic
      - NEXWIKI_ALLOWED_ORIGINS=https://wiki.yourdomain.com
      - NEXWIKI_SECRET_SCAN=refuse
      - NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS=60
      - NEXWIKI_PLAN_DELETE_AFTER_DAYS=180
    volumes:
      - nexwiki_storage:/app/data
    # Do not expose ports to the host; Caddy communicates via the internal Docker network
    expose:
      - "5808"

volumes:
  nexwiki_storage:
    driver: local
  caddy_data:
  caddy_config:
```

### Corresponding `Caddyfile`:
```caddy
wiki.yourdomain.com {
    basic_auth {
        alice $2a$14$exampleHashedPasswordHere...
    }
    reverse_proxy nexwiki:5808
}
```

---

## 🐧 7. Production Systemd Service (Bare-Metal / Linux VM)

If running the native compiled Go binary on a Linux server without Docker:

### 1. Create Dedicated Service User and Directory
```bash
sudo useradd -r -s /bin/false nexwiki
sudo mkdir -p /var/lib/nexwiki
sudo chown -R nexwiki:nexwiki /var/lib/nexwiki
sudo cp nexwiki /usr/local/bin/nexwiki
sudo chmod +x /usr/local/bin/nexwiki
```

### 2. Create Systemd Unit (`/etc/systemd/system/nexwiki.service`)
```ini
[Unit]
Description=NexWiki Personal Second Brain & MCP Server
After=network.target

[Service]
Type=simple
User=nexwiki
Group=nexwiki
WorkingDirectory=/var/lib/nexwiki
Environment="NEXWIKI_NAME=Team Knowledge Base"
Environment="NEXWIKI_THEME=default"
Environment="NEXWIKI_ALLOWED_ORIGINS=https://wiki.yourdomain.com"
Environment="NEXWIKI_BIND=127.0.0.1"
ExecStart=/usr/local/bin/nexwiki -port=5808 -data=/var/lib/nexwiki
Restart=on-failure
RestartSec=5s

# Security sandboxing
ProtectSystem=strict
ReadWritePaths=/var/lib/nexwiki
ProtectHome=true
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

### 3. Enable and Start Service
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now nexwiki
sudo systemctl status nexwiki
```
