#!/usr/bin/env bash
#
# AirBuild CLI installer — macOS and Linux
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.sh | bash
#
# Pin a version, or choose the install directory:
#   curl -fsSL .../install.sh | AIRBUILD_VERSION=v1.2.0 bash
#   curl -fsSL .../install.sh | AIRBUILD_INSTALL_DIR=$HOME/bin bash
#
# The binary is verified against the release's checksums.txt (SHA-256) before
# it is installed; a missing or mismatching checksum aborts the install.
#
set -euo pipefail

REPO="airbuild/airbuild-cli"

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

# --- Resolve the release (latest, or AIRBUILD_VERSION=vX.Y.Z) ---
if [ -n "${AIRBUILD_VERSION:-}" ]; then
  TAG="${AIRBUILD_VERSION#cli-}"
  case "$TAG" in v*) ;; *) TAG="v${TAG}" ;; esac
  BASE_URL="https://github.com/${REPO}/releases/download/cli-${TAG}"
  info "Installing pinned version ${TAG}"
else
  # The direct redirect works without GitHub API rate limits.
  BASE_URL="https://github.com/${REPO}/releases/latest/download"
fi

# --- Pick a SHA-256 tool (sha256sum on Linux, shasum on macOS) ---
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  err "Neither sha256sum nor shasum found — cannot verify the download."
  exit 1
fi

# --- Download binary + checksums ---
TMPFILE="$(mktemp -t airbuild.XXXXXX)"
SUMFILE="$(mktemp -t airbuild-sums.XXXXXX)"
trap 'rm -f "$TMPFILE" "$SUMFILE"' EXIT

info "Downloading ${BINARY}..."
if ! curl -fsSL --connect-timeout 15 --max-time 600 "${BASE_URL}/${BINARY}" -o "$TMPFILE"; then
  err "Download failed. Please check your internet connection (or the version)."
  err "If no release exists yet, build from source:"
  err "  go install github.com/airbuild/airbuild-cli@latest"
  exit 1
fi
if ! curl -fsSL --connect-timeout 15 --max-time 60 "${BASE_URL}/checksums.txt" -o "$SUMFILE"; then
  err "Could not download checksums.txt — refusing to install an unverified binary."
  exit 1
fi

# --- Verify integrity ---
EXPECTED="$(awk -v f="$BINARY" '{n=$2; sub(/^\*/,"",n)} n==f {print $1}' "$SUMFILE" | head -n1)"
ACTUAL="$(sha256 "$TMPFILE")"
if [ -z "$EXPECTED" ]; then
  err "No checksum listed for ${BINARY} — refusing to install."
  exit 1
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
  err "Checksum mismatch for ${BINARY} (expected ${EXPECTED}, got ${ACTUAL}) — refusing to install."
  exit 1
fi
ok "Checksum verified (sha256 ${ACTUAL})"

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
