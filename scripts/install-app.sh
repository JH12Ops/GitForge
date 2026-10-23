#!/bin/sh
# Build the current working tree and install it as /Applications/GitForge.app.
#
# Usage:
#   npm run install:app
#   sh scripts/install-app.sh
#
# The bundle is made self-contained by embedding the renderer build at
# Contents/Resources/dist, so the app works standalone from /Applications
# (backend.ResolveDistDir falls back to Resources/dist inside a bundle).
# No git operations: this only ships what is in the tree right now.
set -eu

cd "$(dirname "$0")/.."
VERSION=$(node -p "require('./package.json').version")
APP_NAME="GitForge"
APP_PATH="/Applications/${APP_NAME}.app"

say() { printf '%s\n' "$*"; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { say "Error: '$1' is required but not installed."; exit 1; }
}
require_cmd node
require_cmd npm
require_cmd ditto
require_cmd codesign

say "Building ${APP_NAME} v${VERSION} (renderer + backend)..."
npm run build
npm run go:build

# Embed the renderer so the installed bundle does not depend on the repo's dist/.
rm -rf go-backend/${APP_NAME}.app/Contents/Resources/dist
cp -R dist go-backend/${APP_NAME}.app/Contents/Resources/dist
codesign --force --deep --sign - go-backend/${APP_NAME}.app

# --- Permissions ---------------------------------------------------------------
if [ ! -w "/Applications" ]; then
  say "Error: /Applications is not writable for this user."
  say "Re-run with: sudo sh scripts/install-app.sh"
  exit 1
fi

# --- Quit only the installed instance (dev bundles elsewhere keep running) ------
if pgrep -f "/Applications/${APP_NAME}.app/Contents/MacOS" >/dev/null 2>&1; then
  say "Quitting the installed ${APP_NAME}..."
  osascript -e 'quit app "GitForge"' >/dev/null 2>&1 || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    pgrep -f "/Applications/${APP_NAME}.app/Contents/MacOS" >/dev/null 2>&1 || break
    sleep 1
  done
fi

# --- Install --------------------------------------------------------------------
say "Installing to ${APP_PATH}..."
rm -rf "$APP_PATH"
ditto go-backend/${APP_NAME}.app "$APP_PATH"

# Re-seal at the destination (same as scripts/install.sh).
codesign --force --deep --sign - "$APP_PATH" >/dev/null 2>&1 || true

# --- Launch ----------------------------------------------------------------------
# Freshly swapped bundles can confuse LaunchServices: `open` fails with
# `_LSOpenURLsWithCompletionHandler() failed with error -600` (procNotFound)
# until the new bundle is registered and the old process record drains.
# The install itself is done, so launch softly: re-register, settle, retry.
say "Installed ${APP_NAME} v${VERSION}."

LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
[ -x "$LSREGISTER" ] && "$LSREGISTER" -f "$APP_PATH" >/dev/null 2>&1 || true

say "Launching..."
LAUNCHED=0
for _ in 1 2 3 4 5; do
  if open "$APP_PATH" >/dev/null 2>&1; then
    LAUNCHED=1
    break
  fi
  sleep 2
done

if [ "$LAUNCHED" -eq 0 ]; then
  say "Could not launch automatically (LaunchServices hiccup)."
  say "Start it manually: open ${APP_PATH}"
fi
