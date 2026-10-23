# Architecture

GitForge is a Wails 3 desktop app with a Go backend and a Vue 3 renderer. The UI depends on a `GitService` interface; the renderer never talks to Git directly — it posts typed JSON commands to an HTTP gateway served by the Go process inside the Wails asset handler.

## Stack

| Layer | Technology |
|-------|------------|
| Shell | Wails 3 (Go backend + native window, `v3.0.0-beta.33`) |
| UI | Vue 3.5, Pinia 3, Tailwind v4, reka-ui |
| Build | Vite 7 + TypeScript 6; the Go binary embeds `dist/` via the gateway |
| Git | Native `git` CLI spawned by Go (`exec.Command`, argv arrays only) |
| Terminal | System terminal (external, via `git_open_terminal_external`) |
| File watch | `fsnotify` → 250ms debounced `git:changed` SSE events → scoped snapshot refresh |

## Process model

```
┌─────────────────────────────────────────────────────────────┐
│  Renderer (Vue)                                             │
│  features/ → stores/repository + repoDiff → wailsGitService │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTP JSON gateway (POST /gitforge/api/<cmd>)
                            │ + SSE (git:changed, git:progress, menu:action)
┌───────────────────────────▼─────────────────────────────────┐
│  Go backend (go-backend/)                                   │
│  main.go: app + window + native menu + dialog hooks         │
│  backend/: gateway.go (routes + SSE hub) →                  │
│    git.go / refs.go / diff.go / actions.go / journal.go /   │
│    watcher.go / conflict.go / image.go / rebase.go          │
│              → git CLI + parsers                            │
└─────────────────────────────────────────────────────────────┘
```

## Shared contracts

### App shell (`shared/app/`)

| Module | Purpose |
|--------|---------|
| `metadata.ts` | App name, version, homepage, repository URL (from `package.json`) |
| `credits.ts` | App links, third-party attribution list, `formatAboutCredits()` |

### Git layer (`shared/git/`)

| Module | Purpose |
|--------|---------|
| `models.ts` | Wire types (ISO date strings, serializable snapshots) |
| `actions.ts` | `GitActionKind` + `GitAction` — single source of truth |
| `errors.ts` | `GitError`, `classifyGitError()`, serialized error payloads |
| `refLabel.ts` | Branch/tag/ref display labels shared by renderer and parsers |
| `avatar.ts` | Gravatar URL helpers for commit authors |
| `rebase.ts` | Interactive rebase entry types and sequence helpers |
| `undoPolicy.ts` | Reflog-based undo/redo eligibility rules |
| `destructive.ts` | Shared destructive-action set for UI + ActionRouter |
| `snapshotScopes.ts` | Snapshot refresh scopes per `GitActionKind` |
| `credentialHints.ts` | Recovery hints for authentication/network errors |
| `githubUrl.ts` | Parse remotes and build GitHub browse URLs |

### Diff helpers (`shared/diff/`)

| Module | Purpose |
|--------|---------|
| `buildPatch.ts` | Builds unified patches for per-hunk stage/unstage |
| `guessLanguage.ts` | File-path → highlight.js language id for syntax highlighting |

`shared/perf.ts` provides lightweight timing helpers for dev profiling.

## Renderer git layer (`src/lib/wails/` + `src/lib/shell/` + `src/lib/git/`)

| File | Role |
|------|------|
| `GitService.ts` (`src/lib/git/`) | Interface the store depends on |
| `wailsGitService.ts` (`src/lib/wails/`) | Production implementation over the HTTP gateway (`fetch` + `EventSource`) |
| `backendGate.ts` (`src/lib/shell/`) | Holds repo-scoped commands until an in-flight `git_activate_repo` settles (cached tabs paint before the backend re-points) |
| `appBridge.ts` (`src/lib/shell/`) | External URLs + native window background (via gateway) |
| `repoCommands.ts` (`src/lib/shell/`) | Clone/init/scan flows + native folder picker (via gateway) |
| `updater.ts` (`src/lib/shell/`) | GitHub Releases check, semver compare, snooze (confirm-based under wails; native self-update returns with the release pipeline) |
| `perfBridge.ts` (`src/lib/shell/`) | Perf samples → gateway log sink (webview console is invisible in release) |
| `menuBridge.ts` (`src/lib/wails/`) | Dispatches SSE `menu:action` events into stores; feeds the native Tabs submenu via `git_set_tabs`; startup update check |
| `favorites.ts` (`src/lib/`) | Pinned-branch persistence (gateway server-side; localStorage in mock dev) |
| `keybindings.ts` (`src/lib/`) | Central shortcut registry (`COMMAND_IDS`), defaults, conflict detection, persistence |
| `imageFiles.ts` / `imageZoom.ts` (`src/lib/`) | Image-path detection + zoom/pan transform helpers for `ImageDiffView` |
| `graphFilter.ts` (`src/lib/graph/`) | Local commit/stash matcher that dims non-matching graph rows during search |
| `MockGitService.ts` | Generated data; used when `VITE_USE_MOCK=true` (browser/dev) |
| `wireAdapter.ts` | Revives wire snapshots into domain types (`Date`, etc.) |
| `themes.ts` / `applyTheme.ts` | Theme catalog, system preference resolution, CSS variable application, native window background sync |
| `avatarCache.ts` | Cached author avatar images for commit rows |

Backend selection: the Go asset server stamps `__GITFORGE_BACKEND__='wails'`
into the served `index.html` (`src/lib/backend.ts`); mock dev swaps the
in-memory service when `VITE_USE_MOCK=true`.

## Go backend (`go-backend/`)

| File | Role |
|------|------|
| `main.go` | Wails app + window (`application.New`), native dialog hooks (`PickAndOpenFunc`, `PickDirectoryFunc`, `AcceptWindowBackground`), self-probe, twin test listener (`GITFORGE_LOCAL_TEST_ADDR`) |
| `menubar.go` | Native menu bar (App/File/View/Window/Help) mirroring the Tauri-era `appMenuLabels`; zoom, fullscreen, relaunch, tabs submenu rebuild |
| `backend/gateway.go` | All `/gitforge/api/*` routes + SSE hub (`/gitforge/events`); stamps the backend marker into `index.html` |
| `backend/git.go` | Snapshot builder, repo validation, recents + favorites persistence |
| `backend/log.go` | Commit log fetch + parse, author registry, avatar URLs |
| `backend/refs.go` | Branches/tags/stashes/worktrees/remotes/status parsers |
| `backend/diff.go` | Diff/commit-files/search, conflict markers, rebase commits |
| `backend/image.go` | Binary image previews (size cap, data-URL sides for old/new) |
| `backend/actions.go` | Action router (`git_execute_action`), cancellable runner with progress, command log, remote/stash/staging/branch/tag ops |
| `backend/journal.go` | Per-repo undo/redo journal (HEAD + index checkpoints) |
| `backend/watcher.go` | `fsnotify`-based repo watcher → debounced scoped `git:changed` SSE events |
| `backend/conflict.go` | Conflict sidecar + `git_get_conflict` |

Tests live as `backend/*_test.go`; run `npm run go:test`.

## Renderer stores (`src/stores/`)

| Store | Role |
|-------|------|
| `repository` | Repo lifecycle, commits, branches, git actions, commit box |
| `repoDiff` | Selected file, diff/conflict loading, commit/compare file lists |
| `selection` | Selected/hovered commit, shift-compare range |
| `branchDrag` | Branch drag-and-drop state and integrate dialog payload |
| `ui` | Layout prefs, graph columns, sidebar sections, theme |
| `interactiveRebase` | Interactive rebase dialog state |
| `prompt` / `toast` | Modal and toast UI |
| `repoTabs` | Open repository tabs, active tab, persistence (reopen closed, cycle, jump-to-index) |
| `repoTabCache` | Per-tab snapshot cache for fast tab switching |
| `gitOutput` | Per-repo executed-command log (capped) + drawer open state |

The `repository` store composes actions from `src/stores/repo/`:

| Module | Role |
|--------|------|
| `lifecycle.ts` | Open/close repo, refresh, pagination |
| `gitActions.ts` | Fetch, merge, rebase, stash, undo, etc. |
| `commitBox.ts` | Commit message, amend, sign-off |
| `getters.ts` | Derived repo state (HEAD, ahead/behind, etc.) |
| `graphLayout.ts` | Runs `computeGraphLayout` on commit list |
| `refreshScheduler.ts` | Debounced snapshot refresh after `git:changed` |
| `diffReloadScheduler.ts` | Debounced diff reload when selection changes |
| `tabPreload.ts` | Warms the tab cache (`git_preload_repo`, no re-point) at startup, on hover, and on tab add |
| `actionHelpers.ts` | Shared action dispatch + error handling |

## UI layout

```
App.vue
├── StartupView          (no repo open: open/recent + CloneDialog + InitDialog + folder discovery)
└── main view
    ├── RepoTabs         multi-repo tab bar + repo switcher (reopen closed, cycle, jump-to-index)
    ├── RepoLoadingSkeleton  cold tab switch: three-pane skeleton (delayed 120ms) until the snapshot lands
    ├── Toolbar          remote/history actions, open external terminal
    ├── OperationBanner  merge/rebase/cherry-pick/revert in progress
    ├── SidebarRail      expand affordance when left/right pane collapsed
    ├── Splitpanes
    │   ├── BranchSidebar       (drag-drop integrate dialog, section context menus, windowed rows)
    │   ├── CommitGraph         (virtualized graph + diff/conflict overlay; ImageDiffView with zoom/pan/lightbox)
    │   └── WorkingTreePanel    (FileTypeIcon per path)
    ├── GitOutputDrawer  per-tab executed git commands (stdout/stderr, ok/error)
    ├── StatusBar        ahead/behind, progress, cancel, auto-fetch state
    ├── PromptHost       confirm / prompt dialogs
    ├── SearchOverlay    commit + file search
    ├── InteractiveRebaseDialog
    ├── BlameOverlay     blame view + file history side panel
    └── AppSettingsDialog  appearance (theme, UI mode), layout, changes, commits, graph, sidebar, sync (auto-fetch), shortcuts (remappable, conflict detection), credits
```

## Data flow

1. User opens repo → `git_open_repo` → validated `RepoInfo` (cached per path for the run) → snapshot builder → full snapshot over the gateway.
2. `wireAdapter` revives dates; store runs `computeGraphLayout` on commits.
3. Switching to a cached tab paints `repoTabCache` synchronously and calls `git_activate_repo` in the background; `backendGate.ts` holds repo-scoped commands until it settles, while inactive tabs are warmed with `git_preload_repo` (never touches `current`/watcher/recents).
4. Mutations dispatch `GitAction` → `git_execute_action` → git CLI → scoped `refreshSnapshot`; the envelope carries the command log for the output drawer.
5. External edits trigger the fsnotify watcher → coalesced scoped `git:changed` SSE → same refresh path.
6. Undo/redo uses the Go-side journal (checkpoint before each journalable action) with reset/checkout restore per `undoPolicy`.

## Persistence

| Key | Storage |
|-----|---------|
| `gitforge:preferences` | Panel sizes, collapsed state, theme, graph/sidebar prefs, open repo tabs (localStorage) |
| `recent-repos` | Recent repository list (Go side: app-data `recent-repos.json`) |
| `branch-favorites` | Per-repo pinned branches (Go side: app-data `favorites.json`) |
| `gitforge:update-snooze` | Skipped update version for the startup check (localStorage) |

## Testing

Frontend unit tests live next to the code as `*.test.ts` under `src/` and `shared/` (graph layout, diff helpers, shell bridges, shared contracts, stores). Go tests live in `go-backend/backend/*_test.go` — parsers (log/refs/diff), journal policy, runner, error classification, wire shapes.

Run: `npm run test` + `npm run go:test`

## Extension points

- **New git operation:** add kind to `shared/git/actions.ts`, route it in `go-backend/backend/actions.go`, wire UI.
- **New gateway command:** register the route in `backend/gateway.go` + typed wrapper on `WailsGitService` (exempt repo-gating ones in `REPO_GATE_EXEMPT`).
- **New snapshot field:** extend wire model + Go parser + `wireAdapter` + store state.
- **Replace CLI backend:** implement `GitService` without touching Vue features.

## Security notes

- The webview has no shell access; git runs only in Go via argv arrays (no shell interpolation for anything derived from renderer input).
- Paths passed to git are validated (`assertPathInsideRepo` in TS, `ValidateRepo` in Go).
- The gateway only accepts same-origin requests served through the Wails asset handler; external URLs are scheme-filtered (`http`/`https`) before the OS opener runs.
