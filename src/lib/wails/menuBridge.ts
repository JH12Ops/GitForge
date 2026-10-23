import { wailsGitService, type MenuActionEvent } from './wailsGitService'
import { useUiStore } from '@/stores/ui'
import { useRepositoryStore } from '@/stores/repository'
import { useRepoTabsStore } from '@/stores/repoTabs'
import { useToastStore } from '@/stores/toast'
import {
  closeActiveRepositoryTab,
  reopenClosedRepositoryTab,
  switchToRepositoryTab,
} from '@/composables/useRepoActions'
import { cloneRepository, initRepository } from '@/lib/shell/repoCommands'
import { openExternalUrl } from '@/lib/shell/appBridge'
import { checkForUpdates, fetchLatestRelease, type UpdateCheckSource, type UpdateSnooze } from '@/lib/shell/updater'
import { APP_VERSION } from '@shared/app/metadata'

/* Wails shell bridge: the native menu lives in the Go process, so renderer
behaviour for menu items arrives as SSE `menu:action` events, and the native
Window → Tabs submenu is fed from the renderer's tab store. Also owns the
startup update check (3s delay, snooze-aware). */

const SNOOZE_KEY = 'gitforge:update-snooze'
const LEGACY_SNOOZE_KEY = 'git-light:update-snooze'

function readSnoozeWails(): UpdateSnooze | null {
  try {
    const raw = localStorage.getItem(SNOOZE_KEY) ?? localStorage.getItem(LEGACY_SNOOZE_KEY)
    return raw ? (JSON.parse(raw) as UpdateSnooze) : null
  } catch {
    return null
  }
}

function writeSnoozeWails(snooze: UpdateSnooze): void {
  try {
    localStorage.setItem(SNOOZE_KEY, JSON.stringify(snooze))
  } catch {
    /* storage unavailable */
  }
}

/**
 * Update check sans native updater: everything here is fetch-based; the
 * "download?" decision becomes a plain confirm and self-update is disabled
 * (the in-place updater ships with the Go release pipeline, v0.2).
 * Manual checks report failure and "already up to date" as toasts; the
 * startup check stays silent unless an update is offered.
 */
async function runUpdateCheck(source: UpdateCheckSource): Promise<void> {
  const toast = useToastStore()
  try {
    const outcome = await checkForUpdates({
      source,
      currentVersion: APP_VERSION,
      fetchRelease: fetchLatestRelease,
      readSnooze: readSnoozeWails,
      writeSnooze: writeSnoozeWails,
      askDownload: async (message, detail) =>
        window.confirm(`${message}\n\n${detail}`) ? 'download' : 'later',
      openDownload: (url) => openExternalUrl(url),
      arch: 'arm64',
      platform: 'macos',
      selfUpdateHint: 'Download opens the release page in your browser.',
    })
    if (source === 'menu') {
      if (outcome === 'up-to-date') {
        toast.push(`You're on the latest version (v${APP_VERSION}).`, 'info')
      } else if (outcome === 'failed') {
        toast.push('Update check failed. Check your connection and try again.', 'error')
      }
    }
  } catch (err) {
    toast.push(`Update check failed: ${String(err)}`, 'error')
  }
}

function handleMenuAction(event: MenuActionEvent): void {
  const repo = useRepositoryStore()
  const ui = useUiStore()
  switch (event.action) {
    case 'new-tab':
    case 'open-repository':
      void repo.pickAndOpenRepository()
      break
    case 'close-tab':
      void closeActiveRepositoryTab()
      break
    case 'reopen-closed-tab':
      void reopenClosedRepositoryTab()
      break
    case 'clone-repository':
      void cloneRepository()
      break
    case 'init-repository':
      void initRepository()
      break
    case 'open-settings':
      ui.openSettings()
      break
    case 'check-updates':
      void runUpdateCheck('menu')
      break
    case 'open-external':
      if (event.url) void openExternalUrl(event.url)
      break
    case 'switch-tab':
      if (event.path) void switchToRepositoryTab(event.path)
      break
    default:
      break
  }
}

// Debounced tab push: the store mutates a few times per tab action, and each
// push rebuilds the native submenu.
let tabsPushTimer: ReturnType<typeof setTimeout> | undefined

function scheduleTabsPush(): void {
  if (tabsPushTimer) return
  tabsPushTimer = setTimeout(() => {
    tabsPushTimer = undefined
    const tabs = useRepoTabsStore()
    void wailsGitService.setAppTabs(
      tabs.tabs.map((tab) => ({ path: tab.path, name: tab.name })),
      tabs.activePath ?? '',
    )
  }, 60)
}

/** Install the wails menu bridge; no-op outside the Go webview. */
export function setupWailsMenuBridge(): void {
  if (window.__GITFORGE_BACKEND__ !== 'wails') return

  wailsGitService.onMenuAction(handleMenuAction)
  useRepoTabsStore().$subscribe(scheduleTabsPush, { detached: true })
  scheduleTabsPush()

  // Startup update check (3s delay, snooze-aware) — parity with the old
  // Tauri startup hook.
  window.setTimeout(() => {
    void runUpdateCheck('startup')
  }, 3000)
}
