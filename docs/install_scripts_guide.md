# NexWiki Install Scripts Guide 🚀

NexWiki ships installer scripts under [`scripts/`](../scripts/) that always fetch the **latest GitHub release** — never an unreleased main-branch build. Release binaries are built by the `release.yml` workflow on every `v*` tag (`nexwiki-<version>-{linux-amd64,linux-arm64,darwin-arm64,windows-amd64.exe}` plus `SHA256SUMS.txt`), and the Docker image is published to `ghcr.io/gruberchris/nexwiki` (`:latest` plus version tags).

> **Intel Macs are not supported**: the release pipeline publishes no `darwin-amd64` binary. Use `docker-run.sh` or build from source instead.

---

## Binary install — macOS / Linux

```bash
./scripts/install.sh                 # installs to ~/.local/bin (or ~/bin)
./scripts/install.sh ~/my-tools      # explicit target directory
NEXWIKI_VERSION=v0.2.0 ./scripts/install.sh   # pin a specific release
```

What it does:
1. Detects OS/arch (`linux/{amd64,arm64}`, `darwin/arm64`).
2. Resolves the latest release tag via the GitHub API (or uses `NEXWIKI_VERSION`).
3. Downloads the asset plus `SHA256SUMS.txt` and verifies the checksum.
4. Installs to `~/.local/bin/nexwiki` (`chmod 0755`) and warns if that dir is not on `PATH`.

## Binary install — Windows (PowerShell)

```powershell
.\scripts\install.ps1
.\scripts\install.ps1 -InstallDir "$HOME\bin" -Version v0.2.0
```

Installs `nexwiki.exe` to `%LOCALAPPDATA%\Programs\nexwiki` by default, verifies SHA256, and adds the directory to your **user** `PATH` (restart the terminal to pick it up). Your wiki content lives in `%AppData%\nexwiki\nexwiki-data` and is created on first launch.

## Uninstall (binary left, data kept)

```bash
./scripts/uninstall.sh               # sweeps ~/.local/bin, ~/bin, /usr/local/bin, PATH
./scripts/uninstall.sh ~/my-tools    # explicit location first
```

```powershell
.\scripts\uninstall.ps1
.\scripts\uninstall.ps1 -InstallDir "$HOME\bin"
```

Uninstallers remove **the binary only** — your wiki content (`~/.config/nexwiki/nexwiki-data` on macOS/Linux, `%AppData%\nexwiki\nexwiki-data` on Windows) is left untouched. The PowerShell uninstaller also drops the empty directory from your user `PATH`.

## Docker run — latest image, correct data dir

```bash
./scripts/docker-run.sh
IMAGE=ghcr.io/gruberchris/nexwiki TAG=v0.2.0 CONTAINER_NAME=nexwiki HOST_PORT=5808 ./scripts/docker-run.sh
```

```powershell
.\scripts\docker-run.ps1
.\scripts\docker-run.ps1 -Tag v0.2.0 -HostPort 5808
```

What it does:
1. Resolves the OS-correct data directory (`$XDG_CONFIG_HOME` else `~/.config/nexwiki/nexwiki-data` on Linux, `~/.config/...` on macOS, `%AppData%/nexwiki/nexwiki-data` under Git Bash).
2. `docker pull`s the latest **released** image (`ghcr.io/gruberchris/nexwiki:latest`).
3. Recreates the `nexwiki` container with `-p 5808:5808`, the data dir mounted at `/app/data`, and `--restart unless-stopped`.

## Opening the browser on startup

Pass `-launch-in-browser` (or `NEXWIKI_LAUNCH_BROWSER=true`) to any normal binary launch:

```bash
./nexwiki -launch-in-browser
```

NexWiki waits until `/api/config` answers, then opens `http://localhost:5808` in your system default browser (`open` / `rundll32` / `xdg-open`). Failures are warnings only — a headless box still serves. The flag is ignored under `-mcp-only` (no web server is bound there).
