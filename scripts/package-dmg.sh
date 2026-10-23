#!/bin/sh
# Build a distributable GitForge DMG for macOS.
#
# Produces GitForge-<version>.dmg at the repo root: a release build
# (renderer + Go backend) with the frontend embedded in the app bundle so
# the app works standalone, plus an /Applications symlink for drag-install.
set -eu
cd "$(dirname "$0")/.."
VERSION=$(node -p "require('./package.json').version")

npm run build
cd go-backend
# Match the Go linker's deployment target (11.0 = bundle minimum) so ld doesn't
# warn about SDK-default (27.0) cgo objects.
CGO_CFLAGS='-O2 -g -mmacosx-version-min=11.0' go build -o GitForge.app/Contents/MacOS/GitForge .
mkdir -p GitForge.app/Contents/Resources
cp appicon.icns GitForge.app/Contents/Resources/GitForge.icns
rm -rf GitForge.app/Contents/Resources/dist
cp -R ../dist GitForge.app/Contents/Resources/dist
codesign --force --deep --sign - GitForge.app

cd ..
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
ln -s /Applications "$STAGE/Applications"
cp -R go-backend/GitForge.app "$STAGE/"
OUT="GitForge-$VERSION.dmg"
hdiutil create -volname GitForge -srcfolder "$STAGE" -ov -format UDZO "$OUT"
echo "built $OUT"
