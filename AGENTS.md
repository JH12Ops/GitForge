# GitForge — Wails 3 (Go) + Vue 3 + TypeScript

Desktop Git client. Version source of truth: `package.json`.

## Commands

Frontend (Node 20+):

- `npm run dev` — browser dev: vite + live headless Go dev gateway (real git data; gateway restarts on `go-backend/**/*.go` changes, vite HMR on frontend changes)
- `npm run dev:mock` — vite-only browser dev (in-memory mock for `VITE_USE_MOCK=true`)
- `npm run dev:go` — build renderer + Go backend, then launch GitForge.app
- `npm run build` — production renderer into `dist/`
- `npm run typecheck` — `vue-tsc --noEmit`
- `npm run test` — `vitest run`
- `npm run sync-version` — syncs version to `package-lock.json`, `docs/`, `go-backend/GitForge.app/Contents/Info.plist`
- `npm run sync-version -- --check` — check only, writes nothing

Backend (Go, in `go-backend/`):

- `npm run go:build` — build the binary into the tracked `.app` bundle
- `npm run go:test` / `go:vet`
- `npm run restyle` — apply the Go blank-line spacing style to `go-backend/` (blocks get blank lines around them, `return` gets one when preceded). Runs the standalone `jhstyle` tool from the sibling `../jhstyle` checkout — install once with `npm run restyle:install`; `restyle:check` verifies without writing
- `cd go-backend && gofmt -l .` / `gofmt -w .`

Run the app: `npm run build && npm run go:build && open go-backend/GitForge.app`.

Check before commit/release: `npm run typecheck`, `npm run test`, `npm run go:test`, `npm run sync-version -- --check`.

## Architecture

- UI in `src/` (Vue 3, Pinia, Tailwind v4). Never calls Git directly.
- Shared contracts in `shared/git/` (`actions.ts`, `models.ts`) and `shared/app/metadata.ts` (reads `package.json` at build time).
- Go backend in `go-backend/` (`main.go` registers the app + window; `backend/` holds `git.go` (process runner), `actions.go` (action router), `journal.go` (undo/redo), `gateway.go` (HTTP routes + SSE hub), `watcher.go` (fsnotify), `conflict.go`, `image.go`, `rebase.go`, `menubridge.go`).
- Transport: HTTP JSON gateway served inside the Wails asset handler (`/gitforge/api/<command>`, POST) + SSE `/gitforge/events`. JSON shapes = `shared/git/models.ts` 1:1; backend stateless per repo path (favorites + journal persisted server-side).
- Renderer calls via `src/lib/wails/wailsGitService.ts` (GitService impl) with `src/lib/shell/` holding backend-neutral shell glue (`backendGate.ts`, `appBridge.ts`, `repoCommands.ts`, `updater.ts`, `perfBridge.ts`).
- Native menu bar + dialogs + window background live in `go-backend/menubar.go` and `main.go`; renderer ↔ menu actions flow over SSE `menu:action` and `git_set_tabs`.
- Wails config: `go-backend/main.go` (`application.New` + `Assets.Handler = backend.NewGateway(distDir)`); gateway stamps `window.__GITFORGE_BACKEND__='wails'` into the served `index.html`.

## Conventions

- New gateway commands: route in `backend/gateway.go` + typed method on `WailsGitService` (`src/lib/wails/wailsGitService.ts`). Never `exec.Command` with interpolated strings from the renderer; all argv assembled in Go.
- Go errors: return `*GitError` (`backend.GitError`) mapped onto the codes in `shared/git/errors.ts`.
- Version: edit only `package.json`, then run `npm run sync-version`. Never edit `Info.plist`/`docs/*/version` manually.
- Do not edit generated output: `dist/`, `node_modules/`, `GitForge.app/Contents/MacOS/` (built binary), `_CodeSignature/`.
- Go tests in `go-backend/backend/*_test.go`; parser/git tests in `vitest` in the frontend.

## Docs

- `README.md` — features + install; `DEVELOPER.md` — build/dev/release; `ARCHITECTURE.md` — stack + processes; `docs/llms.txt` + `docs/llms-full.txt` — AI index.
