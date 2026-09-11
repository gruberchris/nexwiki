#!/usr/bin/env bash
#
# uninstall.sh — Removes the installed nexwiki binary.
#
# Usage:
#   ./scripts/uninstall.sh [TARGET_DIR_OR_FILE]
#
# If no argument is supplied, searches the default install locations
# (~/.local/bin, ~/bin, /usr/local/bin) as well as PATH.
# If an argument is supplied, checks that location first.
#
# Note: this removes the binary only — your wiki content in
# ~/.config/nexwiki/nexwiki-data (Linux/macOS) is left untouched.
#

set -euo pipefail

BIN_NAMES=("nexwiki")

FOUND_ANY=0

remove_bin() {
  local target="$1"
  if [ -f "${target}" ]; then
    echo "==> Removing '${target}'..."
    rm -f "${target}"
    echo "==> Successfully removed '${target}'"
    FOUND_ANY=1
  fi
}

# If an explicit path or directory is supplied
if [ -n "${1:-}" ]; then
  USER_PATH="${1/#\~/${HOME}}"
  echo "==> Checking specified target: ${USER_PATH}"

  if [ -d "${USER_PATH}" ]; then
    for name in "${BIN_NAMES[@]}"; do
      if [ -f "${USER_PATH}/${name}" ]; then
        remove_bin "${USER_PATH}/${name}"
      fi
    done
  elif [ -f "${USER_PATH}" ]; then
    remove_bin "${USER_PATH}"
  else
    echo "Warning: '${USER_PATH}' does not exist." >&2
  fi

  if [ ${FOUND_ANY} -eq 1 ]; then
    exit 0
  fi
  echo "==> Not found at specified target; checking standard locations..."
fi

# Search default directories
CANDIDATE_DIRS=(
  "${HOME}/.local/bin"
  "${HOME}/bin"
  "/usr/local/bin"
)

for dir in "${CANDIDATE_DIRS[@]}"; do
  if [ -d "${dir}" ]; then
    for name in "${BIN_NAMES[@]}"; do
      if [ -f "${dir}/${name}" ]; then
        remove_bin "${dir}/${name}"
      fi
    done
  fi
done

# If still not found, check if command -v finds nexwiki elsewhere in PATH
if [ ${FOUND_ANY} -eq 0 ]; then
  if WHICH_BIN="$(command -v nexwiki 2>/dev/null)" && [ -n "${WHICH_BIN}" ] && [ -f "${WHICH_BIN}" ]; then
    echo "==> Found nexwiki in PATH at '${WHICH_BIN}'"
    remove_bin "${WHICH_BIN}"
  fi
fi

if [ ${FOUND_ANY} -eq 1 ]; then
  echo "==> NexWiki uninstallation complete (wiki data was left in place)."
else
  echo "==> No nexwiki binary found in default locations."
fi
