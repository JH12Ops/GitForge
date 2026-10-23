# Developer guide

**Requirements:** Node.js 20+, Go 1.26+ (with cgo enabled — default), Git installed on your PATH

## Build from source

```bash
git clone https://github.com/JH12Ops/GitForge.git
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
| `npm run install:app` | Build the current tree and install it to `/Applications/GitForge.app` (`scripts/install-app.sh`) |

## Architecture

Architecture, stack, and extension points are documented in **[ARCHITECTURE.md](ARCHITECTURE.md)**.

## Creating a release

Releases are automated. Every merge to `main` that changes app code bumps the version, tags `vX.Y.Z`, and publishes a [GitHub Release](https://github.com/JH12Ops/GitForge/releases) with installer bundles for all platforms plus a `CHECKSUMS.txt` manifest. There are no manual bump/tag/push steps.

### How it works

The [Release workflow](.github/workflows/release.yml) triggers on a push to `main` that touches app code (`src/`, `go-backend/`, `shared/`, `public/`, `index.html`, `vite.config.ts`, `package.json`, `package-lock.json`). Docs/website-only merges do not release.

1. [`scripts/release-bump.mjs`](scripts/release-bump.mjs) picks the bump level from the commit messages since the last release tag:
   - default: **patch**
   - `feat: …` / `feat(scope): …` → **minor**
   - breaking changes (`feat!: …`, a `BREAKING CHANGE:` footer, or `+semver: major`) → **major**
   - `+semver: minor` / `+semver: patch` anywhere in a commit message overrides
2. It writes the next version into `package.json`, `npm run sync-version` propagates the markers (lockfile, docs pages, `Info.plist`), the workflow commits the result as `chore(release): vX.Y.Z [skip ci]`, and pushes an annotated `vX.Y.Z` tag.
3. Build jobs check out the tag and build every platform — macOS (drag-install DMG + portable `GitForge-v*-darwin-arm64.app.zip` with the renderer embedded), Windows (zip with the exe next to `dist/`), Linux (tarball with the binary next to `dist/`) — and a publish job attaches the assets plus `CHECKSUMS.txt` to the Release with generated notes.

This makes the pipeline loop-safe: a run against an already-tagged HEAD is a no-op, and the release bot's own pushes cannot re-trigger workflows. Squash-merged PRs use the PR title as the commit message, so use Conventional Commits titles (`fix: …`, `feat: …`) to control the bump. Preview decisions locally with a dry run:

```bash
node scripts/release-bump.mjs
```

To retry a release after a failed run, use **Run workflow** from the Actions tab (idempotent), or delete the stale tag on GitHub and dispatch again.

### Verify the release

1. Open the new release on GitHub and confirm the platform assets are attached (`-darwin-arm64`, `-windows-amd64`, `-linux-amd64`) plus `CHECKSUMS.txt`.
2. On macOS, install in one line via the installer script (**Right-click → Open** on first launch for Gatekeeper if you run the app directly from the zip instead):

   ```bash
   sh -c "$(curl -fsSL https://raw.githubusercontent.com/JH12Ops/GitForge/main/scripts/install.sh)"
   ```

3. Launch GitForge and use **GitForge → Check for Updates…** — the app compares its version against the latest release tag, so it should report you are on the latest version.

### macOS code signing

CI refreshes the ad-hoc signature after building (`codesign --force --deep --sign -`), which is enough for local installs. To ship Developer ID signed + notarized builds, extend the Release workflow's **Build DMG and .app bundle (macOS)** step:

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

To test packaging before a release ships:

```bash
npm run build && npm run go:build && open go-backend/GitForge.app
```

To build and install directly into `/Applications` instead, run `npm run install:app`
— it builds renderer + backend, embeds `dist/` into the bundle, swaps
`/Applications/GitForge.app`, and relaunches. No version bump or git operations.

The binary inside `GitForge.app` is untracked; `Info.plist` is the tracked template and is version-stamped by `sync-version`.

### Release command

Release automation lives in `.github/workflows/release.yml` + `scripts/release-bump.mjs` (see **Creating a release**). Version markers are still synced by `npm run sync-version`; follow the steps above for local packaging.
