import { createApp } from 'vue'
import { createPinia } from 'pinia'
import type { RecentRepository } from '@shared/git/models'
import { useMockGitServiceIfRequested } from '@/lib/git'
import { relayLog } from '@/lib/backend'
import App from './App.vue'
import { syncThemePreference } from './lib/applyTheme'
import { useUiStore } from './stores/ui'
import { useRepositoryStore } from './stores/repository'
import { useRepoTabsStore } from './stores/repoTabs'
import { preloadSavedTabs } from './stores/repo/tabPreload'
import { attachPerfToTerminal } from '@/lib/shell/perfBridge'
import 'splitpanes/dist/splitpanes.css'
import 'file-icons-js/css/style.css'
import './assets/theme.css'

function startupRepositoryCandidates(
  tabs: { path: string }[],
  lastPath: string | null,
  recentRepos: RecentRepository[],
): string[] {
  const candidates: string[] = []
  if (lastPath) candidates.push(lastPath)
  for (const tab of tabs) {
    if (!candidates.includes(tab.path)) candidates.push(tab.path)
  }
  for (const item of [...recentRepos].sort((a, b) => b.lastOpened - a.lastOpened)) {
    if (!candidates.includes(item.path)) candidates.push(item.path)
  }
  return candidates
}

function attachGlobalErrorRelay(): void {
  if (typeof window === 'undefined') return
  const repo = (msg: string) => {
    relayLog('window', msg)
  }
  window.addEventListener('error', (e) => {
    repo(`error: ${e.message} @ ${e.filename}:${e.lineno}`)
  })
  window.addEventListener('unhandledrejection', (e) => {
    repo(`unhandledrejection: ${String(e.reason)}`)
  })
}

async function restoreSession(
  lastPath: string | null,
  tabs: { path: string }[],
  recentRepos: RecentRepository[],
  open: (path: string) => Promise<boolean>,
): Promise<string | null> {
  for (const path of startupRepositoryCandidates(tabs, lastPath, recentRepos)) {
    try {
      if (await open(path)) return path
    } catch (err) {
      console.error(`[gitforge] failed to restore ${path}:`, err)
    }
  }
  return null
}

async function bootstrap(): Promise<void> {
  // Diagnostics first: everything below (session restore, first snapshot,
  // tab preloading) emits perf samples, and the terminal bridge is what makes
  // them visible outside devtools.
  attachGlobalErrorRelay()
  attachPerfToTerminal()

  const app = createApp(App)
  const pinia = createPinia()
  app.use(pinia)
  // Vue swallows every component/render error into this handler; relay it to
  // the Go sink so release webviews (no devtools) stay diagnosable.
  app.config.errorHandler = (err, _instance, info) => {
    const message = `[render error: ${info}] ${err instanceof Error ? err.stack ?? err.message : String(err)}`
    console.error('[gitforge]', message)
    relayLog('vue', message)
  }

  // Mount first so a backend/startup failure can never leave a black screen:
  // the default store state renders StartupView.
  app.mount('#app')

  let useMock = false
  try {
    useMock = await useMockGitServiceIfRequested()
  } catch (err) {
    console.error('[gitforge] mock backend setup failed:', err)
  }

  const ui = useUiStore(pinia)
  try {
    await ui.hydrate()
  } catch (err) {
    console.error('[gitforge] UI prefs hydrate failed:', err)
  }
  try {
    syncThemePreference(ui.theme)
  } catch (err) {
    console.error('[gitforge] theme sync failed:', err)
  }

  const tabs = useRepoTabsStore(pinia)
  try {
    tabs.hydrate(ui.openRepoTabs, ui.lastRepositoryPath)
  } catch (err) {
    console.error('[gitforge] tabs hydrate failed:', err)
  }

  const repo = useRepositoryStore(pinia)
  try {
    await repo.init()
  } catch (err) {
    console.error('[gitforge] backend init failed (is the gateway reachable?):', err)
    // No backend, no restore: fall back to the startup picker instead of
    // leaving the restore splash on screen forever.
    repo.screen = 'startup'
    return
  }
  try {
    tabs.syncFromRecent(repo.recentRepos)
  } catch (err) {
    console.error('[gitforge] tabs sync failed:', err)
  }

  if (useMock && typeof repo.loadMock === 'function') {
    try {
      await repo.loadMock()
    } catch (err) {
      console.error('[gitforge] mock load failed:', err)
      repo.screen = 'startup'
    }
  } else {
    let openedPath: string | null = null
    try {
      openedPath = await restoreSession(
        tabs.activePath ?? ui.lastRepositoryPath,
        tabs.tabs,
        repo.recentRepos,
        (path) => repo.openRepository(path, { silent: true }),
      )
    } catch (err) {
      console.error('[gitforge] session restore failed:', err)
    }
    if (openedPath) {
      ui.setLastRepositoryPath(openedPath)
      tabs.setActive(openedPath)
      if (tabs.tabs.length > 1) {
        // Warm the other tabs after the visible repository has painted: doing
        // it inline competes with the first snapshot for git processes.
        const visiblePath = openedPath
        window.setTimeout(() => void preloadSavedTabs(repo, tabs.tabs, visiblePath), 500)
      }
    } else {
      if (ui.lastRepositoryPath) ui.setLastRepositoryPath(null)
      // Nothing restorable: this is a genuine first open, so show the picker.
      repo.screen = 'startup'
    }
  }

  // Safety net: any path that neither opened a repository nor reached the
  // startup screen must not strand the user on the restore splash.
  if (repo.screen === 'restoring' && !repo.repository) repo.screen = 'startup'

  // Wails-era shell: the native menu lives in the Go process; this bridge
  // reacts to its actions, feeds it the repository-tab list, and runs the
  // startup update check. No-ops outside the Go webview (browser dev).
  void import('@/lib/wails/menuBridge')
    .then((m) => m.setupWailsMenuBridge())
    .catch((err) => console.error('[gitforge] menu bridge setup failed:', err))
}

bootstrap().catch((err) => console.error('[gitforge] bootstrap failed:', err))
