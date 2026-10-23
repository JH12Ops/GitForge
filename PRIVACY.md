# Privacy

GitForge is a local-first desktop app. No account, no telemetry, no analytics.

## Data stays on your machine

| Data | Where | Notes |
|------|-------|-------|
| Repository content | Your disk | GitForge shells out to your local `git` CLI; repos are never uploaded anywhere |
| Preferences (`gitforge:preferences`) | localStorage | Theme, layout, tabs, sidebar/graph prefs |
| Recent repos (`recent-repos.json`) | App data dir | Path + last-opened timestamp |
| Branch favorites (`branch-favorites:{path}`) | App data dir (`favorites.json`) | Per-repo pinned branches |
| Avatar cache (`gitforge:avatar-cache-v3`) | localStorage | Cached author avatar URLs |

No login, no cloud sync, no crash reporting.

## Network requests (only these)

1. **Author avatars** — `https://github.com/{user}.png` for `*@users.noreply.github.com` emails only, with per-email failure backoff (`avatarCache.ts`). Gravatar-style lookup for other emails derives from the commit email hash; no email list leaves the device beyond the image request itself.
2. **Update check** — compares the app version against the latest GitHub release tag (`shell/updater.ts`). Manual via **Check for Updates…**, plus an automatic check on startup in packaged builds. No identifiers are sent; GitHub sees a plain HTTPS fetch like any browser visit to the releases page.

## Permissions

The renderer has no shell access; file/process access happens only in Go commands with argv arrays (no interpolation of renderer input), and git paths are validated (`ValidateRepo` in Go, `assertPathInsideRepo` in TS).
