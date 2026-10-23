#!/bin/sh
# GitForge installer / updater for macOS.
#
# Downloads the latest release from GitHub, verifies it against CHECKSUMS.txt,
# and replaces /Applications/GitForge.app.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/JH12Ops/GitForge/main/scripts/install.sh | sh
#   sh install.sh [--uninstall] [--no-open] [--quiet]
#
# Why this works: curl (unlike browsers) does not attach the com.apple.quarantine
# flag, so macOS Gatekeeper never blocks the app, even though builds are
# ad-hoc signed.
set -eu

REPO="JH12Ops/GitForge"
APP_NAME="GitForge"
APP_PATH="/Applications/${APP_NAME}.app"
BASE="https://github.com/${REPO}"

QUIET=0
OPEN_APP=1
UNINSTALL=0
for arg in "$@"; do
  case "$arg" in
    --quiet) QUIET=1 ;;
    --no-open) OPEN_APP=0 ;;
    --uninstall) UNINSTALL=1 ;;
    -h|--help)
      echo "Usage: sh install.sh [--uninstall] [--no-open] [--quiet]"
      exit 0
      ;;
    *) echo "Unknown option: $arg" >&2; exit 2 ;;
  esac
done

say() { [ "$QUIET" -eq 1 ] || printf '%s\n' "$*"; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { say "Error: '$1' is required but not installed."; exit 1; }
}
require_cmd curl
require_cmd shasum
require_cmd ditto

# --- macOS check -------------------------------------------------------------
if [ "$(uname -s)" != "Darwin" ]; then
  say "This script is for macOS. See https://github.com/${REPO}/releases/latest for other platforms."
  exit 1
fi

# --- Architecture check ------------------------------------------------------
case "$(uname -m)" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64) ARCH="amd64" ;;
  *) say "Unsupported macOS architecture: $(uname -m)"; exit 1 ;;
esac

# --- Uninstall ---------------------------------------------------------------
if [ "$UNINSTALL" -eq 1 ]; then
  [ -d "$APP_PATH" ] || { say "${APP_NAME} is not installed in /Applications."; exit 0; }
  pgrep -f "${APP_NAME}" >/dev/null 2>&1 && {
    say "Quitting ${APP_NAME}..."
    osascript -e 'quit app "GitForge"' >/dev/null 2>&1 || true
    sleep 2
  }
  rm -rf "$APP_PATH"
  say "${APP_NAME} removed."
  exit 0
fi

# --- Resolve latest tag (no API/JSON parsing needed) --------------------------
say "Resolving latest release..."
LATEST_URL="$BASE/releases/latest"
TAG=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$LATEST_URL" | sed -E 's|^.*/tag/||')
[ -n "$TAG" ] || { say "Error: could not resolve the latest release tag."; exit 1; }
say "Latest release: $TAG"

VERSION="${TAG#v}"
ASSET="GitForge-${TAG}-darwin-${ARCH}.app.zip"
ASSET_URL="$BASE/releases/download/$TAG/$ASSET"

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/gitforge-install.XXXXXX")
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

# --- Download -----------------------------------------------------------------
say "Downloading $ASSET..."
curl -fsSL --retry 3 -o "$TMP_DIR/$ASSET" "$ASSET_URL" || {
  say "Error: download failed from $ASSET_URL"
  exit 1
}

# --- Verify checksum (if the release has CHECKSUMS.txt) ------------------------
if curl -fsSL -o "$TMP_DIR/CHECKSUMS.txt" "$BASE/releases/download/$TAG/CHECKSUMS.txt" 2>/dev/null; then
  say "Verifying checksum..."
  ( cd "$TMP_DIR" && grep -E "^[0-9a-fA-F]{64}[[:space:]]+${ASSET}\$" CHECKSUMS.txt | shasum -a 256 -c --status ) || {
    say "Error: checksum mismatch — the download may be corrupted. Aborting."
    exit 1
  }
  say "Checksum OK."
else
  say "Warning: no CHECKSUMS.txt in this release. Continuing without verification."
fi

# --- Prepare target directory --------------------------------------------------
if [ -d "$APP_PATH" ]; then
  if [ ! -w "/Applications" ]; then
    say "Error: $APP_PATH already exists and /Applications is not writable."
    say "Re-run with: sudo sh install.sh"
    exit 1
  fi
fi

# --- Quit a running instance --------------------------------------------------
if pgrep -f "GitForge.app/Contents/MacOS" >/dev/null 2>&1; then
  say "Quitting running ${APP_NAME}..."
  osascript -e 'quit app "GitForge"' >/dev/null 2>&1 || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    pgrep -f "GitForge.app/Contents/MacOS" >/dev/null 2>&1 || break
    sleep 1
  done
fi

# --- Extract and swap ----------------------------------------------------------
say "Extracting..."
mkdir -p "$TMP_DIR/extract"
ditto -x -k "$TMP_DIR/$ASSET" "$TMP_DIR/extract"
NEW_APP=$(find "$TMP_DIR/extract" -maxdepth 1 -name "*.app" -mindepth 1 | head -n 1)
[ -d "$NEW_APP" ] || { say "Error: no .app found in the archive."; exit 1; }

say "Installing to $APP_PATH..."
if [ -d "$APP_PATH" ]; then
  rm -rf "$APP_PATH"
fi
ditto "$NEW_APP" "$APP_PATH"

# Re-seal the bundle with a valid ad-hoc signature: release zips carry a
# linker-signed main binary whose bundle-level verification can report
# "code has no resources but signature indicates they must be present".
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_PATH" >/dev/null 2>&1 || true
fi

# --- Launch ----------------------------------------------------------------------
if [ "$OPEN_APP" -eq 1 ]; then
  say "Launching ${APP_NAME}..."
  open "$APP_PATH"
fi
