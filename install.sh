#!/usr/bin/env bash
#
# AirBuild CLI installer — macOS and Linux
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/airbuild/cli/main/install.sh | bash
#
set -euo pipefail

REPO="airbuild/cli"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"

# Colors
if [ -t 1 ]; then
  GREEN='\033[0;32m'
  RED='\033[0;31m'
  BLUE='\033[0;34m'
  NC='\033[0m'
else
  GREEN='' RED='' BLUE='' NC=''
fi

info()  { printf "${BLUE}ℹ${NC} %s\n" "$1"; }
ok()    { printf "${GREEN}✓${NC} %s\n" "$1"; }
err()   { printf "${RED}✗${NC} %s\n" "$1" >&2; }

# --- Detect platform ---
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *) err "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)   ARCH="amd64" ;;
  arm64|aarch64)  ARCH="arm64" ;;
  *) err "Unsupported architecture: $ARCH"; exit 1 ;;
esac

BINARY="airbuild-${OS}-${ARCH}"

info "Detected platform: ${OS}/${ARCH}"

# --- Determine install directory ---
INSTALL_DIR="${AIRBUILD_INSTALL_DIR:-${HOME}/.local/bin}"
if [ ! -d "$INSTALL_DIR" ] && ! mkdir -p "$INSTALL_DIR" 2>/dev/null; then
  # Fallback to /usr/local/bin if we can't create ~/.local/bin
  if [ -w /usr/local/bin ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    err "Cannot create ${HOME}/.local/bin or write to /usr/local/bin"
    err "Set AIRBUILD_INSTALL_DIR to a writable directory"
    exit 1
  fi
fi

info "Install directory: ${INSTALL_DIR}"

# --- Find the latest release download URL ---
# Try the direct GitHub redirect first (works without API rate limits)
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"

# --- Download ---
TMPFILE="$(mktemp -t airbuild.XXXXXX)"
trap 'rm -f "$TMPFILE"' EXIT

info "Downloading ${BINARY}..."
if ! curl -fsSL "$DOWNLOAD_URL" -o "$TMPFILE"; then
  err "Download failed. Please check your internet connection."
  err "If no release exists yet, build from source:"
  err "  go install github.com/airbuild/cli@latest"
  exit 1
fi

# --- Verify it's a binary (not an HTML error page) ---
if file "$TMPFILE" | grep -q "HTML\|ASCII text"; then
  err "Downloaded file is not a binary — the release may not exist yet."
  err "Build from source instead: go install github.com/airbuild/cli@latest"
  exit 1
fi

chmod +x "$TMPFILE"

# --- Install ---
TARGET="${INSTALL_DIR}/airbuild"
mv "$TMPFILE" "$TARGET"
trap - EXIT

ok "Installed airbuild to ${TARGET}"

# --- PATH check ---
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ok "Ready! Run: airbuild --help"
    ;;
  *)
    err "${INSTALL_DIR} is not in your PATH."
    err "Add this line to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
    printf '    export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    err "Then restart your terminal or run: source ~/.bashrc"
    ;;
esac

# --- Verify ---
if "${TARGET}" --help >/dev/null 2>&1; then
  ok "Verification: airbuild runs successfully"
else
  err "Warning: airbuild --help returned an error. The binary may not be compatible."
fi
