# Developer guide

**Requirements:** Node.js 20+, Go 1.26+ (with cgo enabled — default), Git installed on your PATH

## Build from source

```bash
git clone https://github.com/gitforge/gitforge.git
cd gitforge
npm install
npm run dev        # browser dev (Vite on :1420, mock/gateway)
# or the full desktop app:
npm run dev:go     # builds renderer + Go backend, launches GitForge.app
npm run dev:go:watch  # same, but quit+rebuild+relaunch on changes
```

`npm run dev:go` = `vite build` → `go build` into `go-backend/GitForge.app` → `open`.
The Go process serves `dist/` through its own asset handler, so there is no
"dev window" separate from the renderer build; refresh after editing renderer
code (or use browser dev, below, which hot-reloads).

## Browser dev mode (UI-only)

To run the generated AwesomeShop mock data without the Go backend:

```bash
VITE_USE_MOCK=true npm run dev
```

For renderer work against the **real** Go backend in a plain browser, start a
twin gateway from the backend:

```bash
GITFORGE_LOCAL_TEST_ADDR=127.0.0.1:18333 go run .   # in go-backend/
npm run dev
```

(The probe finds the twin only if served from the same origin; in practice use
`open go-backend/GitForge.app` for real data.)

## Scripts

| Command | Description |
|---------|-------------|
| `npm run dev` | Browser dev: vite + live headless Go devgateway (real git data; `go-backend/**/*.go` changes rebuild/restart it; vite HMR for frontend) |
| `npm run dev:mock` | Vite-only browser dev over the in-memory mock (`VITE_USE_MOCK=true` behavior) |
| `npm run dev:go` | Build renderer + Go backend and launch GitForge.app |
| `npm run dev:go:watch` | Same as `dev:go`, but relaunches GitForge.app automatically on `go-backend/**/*.go` or renderer source changes |
| `npm run build` | Renderer production build (`dist/`, embedded by the Go gateway) |
| `npm run go:build` | Build the Go binary into `go-backend/GitForge.app` |
| `npm run go:test` / `go:vet` | Go backend tests / vet |
| `npm run restyle` / `restyle:check` | Space Go files via the standalone `jhstyle` project (`../jhstyle`; install once with `restyle:install`) — blank lines around blocks and above returns |
| `npm run preview` | Preview production build |
| `npm run site` | Serve the website (`docs/`, GitHub Pages source) at http://localhost:4173 with Python's SimpleHTTPServer |
| `npm run typecheck` | TypeScript check |
| `npm run test` | Unit tests (graph layout, shell bridges, shared contracts) |
| `npm run test:watch` | Run tests in watch mode |
| `npm run check` | `typecheck` + `test` + `sync-version --check` + `go:test` |
| `npm run package:dmg` | Build the app bundle and produce a standalone `GitForge-<version>.dmg` (`scripts/package-dmg.sh`) |

## Architecture

Architecture, stack, and extension points are documented in **[ARCHITECTURE.md](ARCHITECTURE.md)**.

## Creating a release

Releases are versioned with git tags (`v*`). Pushing a tag triggers GitHub Actions to build the macOS app bundles (Apple silicon + Intel) and attach them to a [GitHub Release](https://github.com/gitforge/gitforge/releases).

### Overview

```text
1. Update docs & bump version in package.json
2. Run tests
3. Commit → tag vX.Y.Z → push commit + tag
4. CI builds macOS .app zips (arm64 + amd64) and publishes the release
```

The packaged app checks for updates by comparing its version against the latest GitHub release tag. Users can also check manually via **GitForge → Check for Updates…** in the menu bar.

### 1. Prepare the release

Before bumping the version:

1. **Sync documentation** so it matches the current codebase:
   - [README.md](README.md) — feature table, install notes
   - [ARCHITECTURE.md](ARCHITECTURE.md) — stack versions, file layout
   - [docs/index.html](docs/index.html) — landing page copy and install section
   - [docs/llms.txt](docs/llms.txt) — AI-readable site summary (version synced automatically)
2. **Refresh the screenshot** if the UI changed. Run mock mode, capture the main view at 1440×900, and overwrite [docs/screenshot.png](docs/screenshot.png):

   ```bash
   VITE_USE_MOCK=true npm run dev
   ```

3. **Verify** the tree is healthy:

   ```bash
   npm run typecheck
   npm run test
   npm run go:test
   ```

### 2. Bump the version

Update `"version"` in [package.json](package.json) only, then sync derived markers:

```bash
npm run sync-version
```

Use [semver](https://semver.org/):

| Bump | Example |
|------|---------|
| Patch | `1.0.2` → `1.0.3` |
| Minor | `1.0.2` → `1.1.0` |
| Major | `1.0.2` → `2.0.0` |

Then sync version markers across docs and the app bundle:

```bash
npm run sync-version
```

The app reads `package.json` at build time (`shared/app/metadata.ts`); `sync-version` propagates the version to `package-lock.json`, `go-backend/GitForge.app/Contents/Info.plist`, `docs/index.html`, `docs/llms.txt` and `docs/llms-full.txt`.

Confirm the tag does not already exist:

```bash
git tag -l 'v*'
```

### 3. Commit and tag

Stage all release changes (docs, version bump, and any pending source changes):

```bash
git add -A
git diff --cached --stat   # review what will ship
```

Commit and create an annotated tag (replace `X.Y.Z` with the new version):

```bash
git commit -m "Release vX.Y.Z

<short summary of what changed since the last release>"
git tag -a vX.Y.Z -m "Release vX.Y.Z"
```

### 4. Push and publish

Push the commit and tag to `main`:

```bash
git push origin main
git push origin vX.Y.Z
```

The [Release workflow](.github/workflows/release.yml) runs when a `v*` tag is pushed: it runs `npm ci` plus the typecheck/test/vet gates, builds the Go binary into the `.app` bundle on both macOS runners (Apple silicon + Intel), re-signs ad-hoc, zips with `ditto`, and attaches the zips to the GitHub Release. A final job publishes `CHECKSUMS.txt` with sha256 for every asset.

Monitor the run under **Actions** on GitHub. When it succeeds, the app zips appear on the release page.

### 5. Verify the release

1. Open the new release on GitHub and confirm the platform assets are attached (`-darwin-arm64` and `-darwin-amd64` `.app.zip`).
2. Download, unzip, and drag **GitForge.app** to `/Applications`. Ad-hoc signed builds may require **Right-click → Open** on first launch (Gatekeeper).
3. Launch the app and use **GitForge → Check for Updates…** — it should report you are on the latest version.

### macOS code signing

CI refreshes the ad-hoc signature after building (`codesign --force --deep --sign -`), which is enough for local installs. To ship Developer ID signed + notarized builds, extend the release workflow's **Build GitForge.app** step:

1. Enroll in the [Apple Developer Program](https://developer.apple.com/programs/).
2. Add these GitHub repository secrets (Settings → Secrets and variables → Actions):

| Secret | Value |
|--------|--------|
| `APPLE_CERTIFICATE` | Base64-encoded `.p12` (`base64 -i cert.p12 \| pbcopy`) |
| `APPLE_CERTIFICATE_PASSWORD` | Password for the `.p12` export |
| `APPLE_SIGNING_IDENTITY` | Developer ID Application identity |
| `APPLE_ID` | Apple ID email used for notarization |
| `APPLE_PASSWORD` | App-specific password from [appleid.apple.com](https://appleid.apple.com) |
| `APPLE_TEAM_ID` | Team ID from Apple Developer → Membership |

3. Replace the ad-hoc `codesign` line with `codesign --force --options runtime --sign "$APPLE_SIGNING_IDENTITY"` plus a `xcrun notarytool submit` step, then staple the ticket.

### Build a bundle locally (optional)

To test packaging before pushing a tag:

```bash
npm run build && npm run go:build && open go-backend/GitForge.app
```

The binary inside `GitForge.app` is untracked; `Info.plist` is the tracked template and is version-stamped by `sync-version`.

### Release command

Release automation lives in `scripts/sync-version.mjs` (invoked via `npm run sync-version`). Follow the steps above to bump, verify, commit, and tag. You still need to push the commit and tag yourself to trigger CI.
