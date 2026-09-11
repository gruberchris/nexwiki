#!/usr/bin/env bash
#
# install.sh — Installs the latest released nexwiki binary to the user's bin folder.
#
# Usage:
#   ./scripts/install.sh [TARGET_DIR]
#
# If TARGET_DIR is omitted, defaults to the standard user bin directory:
#   - Linux/macOS: ~/.local/bin (or ~/bin if already in PATH)
#
# The script always installs the latest GitHub release of
# gruberchris/nexwiki — never an unreleased main-branch build. Override with:
#   NEXWIKI_VERSION=v0.2.0 ./scripts/install.sh
#
# Windows users: run scripts/install.ps1 in PowerShell instead.
#

set -euo pipefail

REPO="gruberchris/nexwiki"
BIN_NAME="nexwiki"

# Detect OS
OS_UNAME="$(uname -s)"
case "${OS_UNAME}" in
  Linux*)
    DETECTED_OS="linux"
    ;;
  Darwin*)
    DETECTED_OS="darwin"
    ;;
  CYGWIN*|MINGW*|MSYS*|Windows*)
    echo "Error: on Windows, run scripts/install.ps1 in PowerShell instead." >&2
    exit 1
    ;;
  *)
    echo "Error: unsupported OS '${OS_UNAME}' (Linux and macOS only)." >&2
    exit 1
    ;;
esac

# Detect Architecture
ARCH_UNAME="$(uname -m)"
case "${ARCH_UNAME}" in
  x86_64|amd64)
    DETECTED_ARCH="amd64"
    ;;
  aarch64|arm64)
    DETECTED_ARCH="arm64"
    ;;
  *)
    echo "Error: unsupported architecture '${ARCH_UNAME}'." >&2
    exit 1
    ;;
esac

# The release pipeline publishes linux-amd64, linux-arm64 and darwin-arm64
# assets only — there is no Intel-macOS binary.
if [ "${DETECTED_OS}" = "darwin" ] && [ "${DETECTED_ARCH}" = "amd64" ]; then
  echo "Error: no Intel-macOS (darwin-amd64) release asset exists." >&2
  echo "Options: run the Docker setup (scripts/docker-run.sh) or build from source." >&2
  exit 1
fi

echo "==> Detected OS: ${DETECTED_OS} (${DETECTED_ARCH})"

# Determine target directory
if [ -n "${1:-}" ]; then
  # Expand leading tilde if present
  TARGET_DIR="${1/#\~/${HOME}}"
  echo "==> Using user-specified install directory: ${TARGET_DIR}"
else
  if [[ ":${PATH}:" == *":${HOME}/bin:"* ]] && [ ! -d "${HOME}/.local/bin" ]; then
    TARGET_DIR="${HOME}/bin"
  else
    TARGET_DIR="${HOME}/.local/bin"
  fi
  echo "==> Using default install directory: ${TARGET_DIR}"
fi

# Resolve the version: explicit override or latest release tag via the API.
if [ -n "${NEXWIKI_VERSION:-}" ]; then
  TAG="${NEXWIKI_VERSION}"
  echo "==> Using pinned version: ${TAG}"
else
  echo "==> Fetching latest release tag..."
  TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)"
  if [ -z "${TAG}" ]; then
    echo "Error: could not determine the latest release (GitHub API unreachable?)." >&2
    echo "Retry later, or pin one explicitly: NEXWIKI_VERSION=v0.2.0 $0" >&2
    exit 1
  fi
  echo "==> Latest release: ${TAG}"
fi

# Asset names carry the version without the leading 'v' (e.g. v0.2.0 -> 0.2.0).
VERSION="${TAG#v}"
ASSET="${BIN_NAME}-${VERSION}-${DETECTED_OS}-${DETECTED_ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${TAG}/SHA256SUMS.txt"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "==> Downloading ${ASSET}..."
curl -fsSL -o "${TMP_DIR}/${ASSET}" "${DOWNLOAD_URL}"

# Verify SHA256 when a checksum file is published (best effort, warn-only if
# the platform lacks a hashing tool).
echo "==> Verifying checksum..."
if curl -fsSL -o "${TMP_DIR}/SHA256SUMS.txt" "${CHECKSUMS_URL}"; then
  EXPECTED="$(grep -E "[[:space:]]${ASSET}\$" "${TMP_DIR}/SHA256SUMS.txt" | awk '{print $1}')"
  if [ -n "${EXPECTED}" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      ACTUAL="$(sha256sum "${TMP_DIR}/${ASSET}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
      ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ASSET}" | awk '{print $1}')"
    else
      echo "Warning: no sha256sum/shasum found; skipping verification." >&2
      ACTUAL="${EXPECTED}"
    fi
    if [ "${ACTUAL}" != "${EXPECTED}" ]; then
      echo "Error: checksum mismatch for ${ASSET}" >&2
      echo "  expected: ${EXPECTED}" >&2
      echo "  actual:   ${ACTUAL}" >&2
      exit 1
    fi
    echo "==> Checksum OK."
  else
    echo "Warning: no checksum entry for ${ASSET}; skipping verification." >&2
  fi
else
  echo "Warning: SHA256SUMS.txt unavailable; skipping verification." >&2
fi

# Ensure target directory exists
mkdir -p "${TARGET_DIR}"

DEST_BIN="${TARGET_DIR}/${BIN_NAME}"

# Install the binary
echo "==> Installing to ${DEST_BIN}..."
cp -f "${TMP_DIR}/${ASSET}" "${DEST_BIN}"

# Set executable permissions
chmod 0755 "${DEST_BIN}"

echo "==> Successfully installed ${BIN_NAME} ${TAG} to ${DEST_BIN}"

# Check if TARGET_DIR is in PATH
case ":${PATH}:" in
  *":${TARGET_DIR}:"*)
    echo "==> Verify installation:"
    echo "    $ ${BIN_NAME} --help"
    ;;
  *)
    echo ""
    echo "Notice: '${TARGET_DIR}' is not in your PATH."
    echo "To run '${BIN_NAME}' from any directory, add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
    echo "    export PATH=\"\${PATH}:${TARGET_DIR}\""
    echo ""
    ;;
esac
