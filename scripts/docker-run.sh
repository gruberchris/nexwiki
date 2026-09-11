#!/usr/bin/env bash
#
# docker-run.sh — Pulls the latest released nexwiki image and (re)creates the container.
#
# Usage:
#   ./scripts/docker-run.sh
#
# What it does:
#   1. Resolves the OS-correct wiki data directory:
#        Linux:   $XDG_CONFIG_HOME/nexwiki/nexwiki-data (or ~/.config/...)
#        macOS:   ~/.config/nexwiki/nexwiki-data
#        Windows (Git Bash): %AppData%/nexwiki/nexwiki-data
#   2. Pulls the latest RELEASED image (ghcr.io/gruberchris/nexwiki:latest) —
#      never a local unreleased build.
#   3. Recreates the 'nexwiki' container with -p 5808:5808 and the data dir
#      mounted at /app/data.
#
# Overrides:
#   IMAGE=ghcr.io/gruberchris/nexwiki TAG=v0.2.0 CONTAINER_NAME=nexwiki HOST_PORT=5808 ./scripts/docker-run.sh
#

set -euo pipefail

IMAGE="${IMAGE:-ghcr.io/gruberchris/nexwiki}"
TAG="${TAG:-latest}"
CONTAINER_NAME="${CONTAINER_NAME:-nexwiki}"
HOST_PORT="${HOST_PORT:-5808}"

command -v docker >/dev/null 2>&1 || {
  echo "Error: docker is not installed or not on PATH." >&2
  exit 1
}

# Resolve the OS-correct data directory (mirrors the binary's defaultDataDir).
OS_UNAME="$(uname -s)"
case "${OS_UNAME}" in
  Darwin*)
    DATA_DIR="${HOME}/.config/nexwiki/nexwiki-data"
    ;;
  CYGWIN*|MINGW*|MSYS*|Windows*)
    if command -v cygpath >/dev/null 2>&1; then
      DATA_DIR="$(cygpath -u "${APPDATA}/nexwiki/nexwiki-data")"
    else
      DATA_DIR="${HOME}/.config/nexwiki/nexwiki-data"
    fi
    ;;
  Linux*|*)
    if [ -n "${XDG_CONFIG_HOME:-}" ]; then
      DATA_DIR="${XDG_CONFIG_HOME}/nexwiki/nexwiki-data"
    else
      DATA_DIR="${HOME}/.config/nexwiki/nexwiki-data"
    fi
    ;;
esac

echo "==> Detected OS: ${OS_UNAME}"
echo "==> Wiki data directory: ${DATA_DIR}"
mkdir -p "${DATA_DIR}"

echo "==> Pulling ${IMAGE}:${TAG}..."
docker pull "${IMAGE}:${TAG}"

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER_NAME}"; then
  echo "==> Removing existing container '${CONTAINER_NAME}'..."
  docker rm -f "${CONTAINER_NAME}" >/dev/null
fi

echo "==> Starting container '${CONTAINER_NAME}'..."
docker run -d \
  --name "${CONTAINER_NAME}" \
  -p "${HOST_PORT}:5808" \
  -v "${DATA_DIR}:/app/data" \
  --restart unless-stopped \
  "${IMAGE}:${TAG}" >/dev/null

echo "==> NexWiki is running at http://localhost:${HOST_PORT}"
echo "==> Wiki content is stored in ${DATA_DIR}"
