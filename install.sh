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
# it is installed; checksums.txt itself is verified against an RSA-SHA256
# signature (checksums.txt.sig) made by AirBuild's release key, whose public
# half is pinned below. A missing signature or checksum aborts the install.
#
set -euo pipefail

REPO="airbuild/airbuild-cli"

# AirBuild release-signing public key (RSA-3072). The private key lives only in
# the GitHub release workflow's secrets. Pinned here so a tampered release —
# including a swapped checksums.txt — fails verification.
read -r -d '' RELEASE_PUBKEY <<'PUBKEY' || true
-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEAqm+iSCXUFOy3XZpQkDIP
Fur3jpGQdMJtEO3C1JJQq6933UslsKgQLS6dmU7xeQfGflN97bHn94FlP9wVBSeb
or2Es2DybU9CMGnUE3OJjffRnRDLxN5oc9ip1qNyVUbe/I0wX6lhXLSU3Ez56RG8
P/1zQzpqMu90/9gi8Ejnnyt5w1kZtqaiixGGznIEyWbt1Lx/H1u6qejf8ByCs0eJ
cVe9nWlkhPQ7KkkPVe/KVBr62hMqr+Eb3rHV/azYVOkb69dh09H+uOoebhop0jWY
bWDRK6C1EFfzz2gIGFwst9L7CyUUIfLkekrL6jUZih57biLm6AS4X+Mr5AQZxLX9
f7oPHLXYKP10jlEtKDZ5eyNwRFeVzYlcd68XjMwpiUDEx9MTu51zE8Vh6VLL1jC3
Yhnct+aF+BtwwzzQJCUbIxg3ExwnWAMbmaIebKp7f+mUD7u30lSZgulSxvgJeP1e
cZFjOMuBNv6AqUGFCtHaptCUS0GSp9uqjbAOm4cMWOynAgMBAAE=
-----END PUBLIC KEY-----
PUBKEY

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
SIGFILE="$(mktemp -t airbuild-sig.XXXXXX)"
PUBFILE="$(mktemp -t airbuild-pub.XXXXXX)"
trap 'rm -f "$TMPFILE" "$SUMFILE" "$SIGFILE" "$PUBFILE"' EXIT

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
if ! curl -fsSL --connect-timeout 15 --max-time 60 "${BASE_URL}/checksums.txt.sig" -o "$SIGFILE"; then
  err "Could not download checksums.txt.sig — refusing to install an unsigned release."
  exit 1
fi

# --- Verify authenticity: checksums.txt was signed by AirBuild's release key ---
if ! command -v openssl >/dev/null 2>&1; then
  err "openssl not found — cannot verify the release signature."
  exit 1
fi
printf '%s' "$RELEASE_PUBKEY" > "$PUBFILE"
if ! openssl dgst -sha256 -verify "$PUBFILE" -signature "$SIGFILE" "$SUMFILE" >/dev/null 2>&1; then
  err "Release signature verification failed — this release was not signed by AirBuild."
  err "Refusing to install."
  exit 1
fi
ok "Release signature verified"

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
