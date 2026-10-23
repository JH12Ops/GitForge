import { isThemePreference, type ThemePreference } from './themes'
import { sanitizeKeymap, type Keymap } from './keybindings'

export type CommitColumnKey = 'author' | 'sha' | 'when'

export type FileListView = 'path' | 'tree'

export const COLUMN_LABELS: Record<CommitColumnKey, string> = {
  author: 'Author',
  sha: 'SHA',
  when: 'When',
}

export const SECTION_KEYS = [
  'favorites',
  'localBranches',
  'remoteBranches',
  'tags',
  'stashes',
  'worktrees',
] as const

export type SectionKey = (typeof SECTION_KEYS)[number]

export const SECTION_LABELS: Record<SectionKey, string> = {
  favorites: 'Favorites',
  localBranches: 'Local Branches',
  remoteBranches: 'Remote Branches',
  tags: 'Tags',
  stashes: 'Stashes',
  worktrees: 'Worktrees',
}

export const ADVANCED_SECTION_KEYS = ['tags', 'stashes', 'worktrees'] as const satisfies readonly SectionKey[]

export type SidebarSectionState = 'expanded' | 'collapsed' | 'hidden'

export const SIDEBAR_SECTION_STATES: readonly SidebarSectionState[] = ['expanded', 'collapsed', 'hidden']

export const SIDEBAR_SECTION_STATE_LABELS: Record<SidebarSectionState, string> = {
  expanded: 'Expanded',
  collapsed: 'Collapsed',
  hidden: 'Hidden',
}

export const DEFAULT_VISIBLE_SECTIONS: Record<SectionKey, boolean> = {
  favorites: true,
  localBranches: true,
  remoteBranches: true,
  tags: true,
  stashes: true,
  worktrees: true,
}

export interface CommitColumnPrefs {
  author: boolean
  sha: boolean
  when: boolean
}

export const COMMIT_GRAPH_LIMIT_MIN = 25
export const COMMIT_GRAPH_LIMIT_MAX = 500
export const DEFAULT_COMMIT_GRAPH_LIMIT = 100

export function clampCommitGraphLimit(value: number): number {
  return Math.min(COMMIT_GRAPH_LIMIT_MAX, Math.max(COMMIT_GRAPH_LIMIT_MIN, Math.round(value)))
}

export type DiffViewMode = 'unified' | 'split'

export type UiMode = 'basic' | 'advanced'
export function isUiMode(value: unknown): value is UiMode {
  return value === 'basic' || value === 'advanced'
}

export const AUTO_FETCH_INTERVAL_OPTIONS = [0, 60, 300, 600, 1800, 3600, 7200] as const

export const DEFAULT_AUTO_FETCH_INTERVAL_SEC = 300

export function clampAutoFetchIntervalSec(value: unknown): number {
  const rounded =
    typeof value === 'number' && Number.isFinite(value) ? Math.round(value) : DEFAULT_AUTO_FETCH_INTERVAL_SEC
  return (AUTO_FETCH_INTERVAL_OPTIONS as readonly number[]).includes(rounded)
    ? rounded
    : DEFAULT_AUTO_FETCH_INTERVAL_SEC
}

export function formatAutoFetchInterval(sec: number): string {
  if (sec <= 0) return 'Disabled'
  if (sec < 60) return `Every ${sec}s`
  if (sec % 3600 === 0) {
    const hours = sec / 3600
    return hours === 1 ? 'Every hour' : `Every ${hours} hours`
  }
  const min = Math.round(sec / 60)
  return min === 1 ? 'Every minute' : `Every ${min} minutes`
}

export const WORKING_TREE_CHANGES_SIZE_MIN = 20
export const WORKING_TREE_CHANGES_SIZE_MAX = 80
export const DEFAULT_WORKING_TREE_CHANGES_SIZE = 62

export function clampWorkingTreeChangesSize(value: number): number {
  return Math.min(
    WORKING_TREE_CHANGES_SIZE_MAX,
    Math.max(WORKING_TREE_CHANGES_SIZE_MIN, Math.round(value)),
  )
}

export const LEFT_SIDEBAR_SIZE_MIN = 14
export const LEFT_SIDEBAR_SIZE_MAX = 36
export const LEFT_SIDEBAR_COLLAPSE_SIZE = 12
export const LEFT_SIDEBAR_COLLAPSE_HINT_SIZE = LEFT_SIDEBAR_COLLAPSE_SIZE + 6

export const RIGHT_SIDEBAR_SIZE_MIN = 16
export const RIGHT_SIDEBAR_SIZE_MAX = 44
export const RIGHT_SIDEBAR_COLLAPSE_SIZE = 14
export const RIGHT_SIDEBAR_COLLAPSE_HINT_SIZE = RIGHT_SIDEBAR_COLLAPSE_SIZE + 6

export function clampLeftSidebarSize(value: number): number {
  return Math.min(
    LEFT_SIDEBAR_SIZE_MAX,
    Math.max(LEFT_SIDEBAR_SIZE_MIN, Math.round(value)),
  )
}

export function clampRightSidebarSize(value: number): number {
  return Math.min(
    RIGHT_SIDEBAR_SIZE_MAX,
    Math.max(RIGHT_SIDEBAR_SIZE_MIN, Math.round(value)),
  )
}

export function shouldCollapseLeftSidebar(size: number): boolean {
  return size <= LEFT_SIDEBAR_COLLAPSE_SIZE
}

export function shouldCollapseRightSidebar(size: number): boolean {
  return size <= RIGHT_SIDEBAR_COLLAPSE_SIZE
}

export function isInLeftSidebarCollapseHintZone(size: number): boolean {
  return size <= LEFT_SIDEBAR_COLLAPSE_HINT_SIZE
}

export function isInRightSidebarCollapseHintZone(size: number): boolean {
  return size <= RIGHT_SIDEBAR_COLLAPSE_HINT_SIZE
}

export function shouldShowLeftSidebarCollapseHint(
  liveSize: number,
  storedSize: number,
): boolean {
  return liveSize < storedSize && isInLeftSidebarCollapseHintZone(liveSize)
}

export function shouldShowRightSidebarCollapseHint(
  liveSize: number,
  storedSize: number,
): boolean {
  return liveSize < storedSize && isInRightSidebarCollapseHintZone(liveSize)
}

export interface RepoTabPref {
  path: string
  name: string
}

export interface UserPreferences {
  /** Preferences schema version; stamped on write, used for migrations. */
  prefsVersion?: number
  leftSize: number
  rightSize: number
  workingTreeChangesSize: number
  leftCollapsed: boolean
  rightCollapsed: boolean
  sections: Record<string, boolean>
  visibleSections: Record<string, boolean>
  columns: CommitColumnPrefs
  columnWidths: CommitColumnWidths
  columnOrder: CommitColumnWidthKey[]
  graphScopeAll: boolean
  commitGraphLimit: number
  fileListView: FileListView
  lastRepositoryPath: string | null
  openRepoTabs: RepoTabPref[]
  diffViewMode: DiffViewMode
  theme: ThemePreference
  uiMode: UiMode
  ignoreWhitespace: boolean
  blameWordWrap: boolean
  diffWordWrap: boolean
  signOffCommit: boolean
  signCommit: boolean
  autoFetchIntervalSec: number
  keybindings?: Keymap
}

export const PREFERENCES_KEY = 'gitforge:preferences'
export const LEGACY_PREFERENCES_KEY = 'git-light:preferences'

/**
 * Schema version for migrations. Bump + extend `migratePreferences` when a
 * shipped default changes so existing installs that never flipped the
 * corresponding switch follow the new behavior.
 */
export const PREFERENCES_VERSION = 3

export function migratePreferences(
  saved: Partial<UserPreferences> | null,
): Partial<UserPreferences> | null {
  if (!saved || typeof saved !== 'object') return saved
  const version = saved.prefsVersion ?? 1
  if (version >= PREFERENCES_VERSION) return saved
  const migrated: Partial<UserPreferences> = { ...saved }
  if (version < 3) {
    // v2 flipped the graph scope default to current-branch only, but v2
    // stamps were also written by builds that loaded the pre-v2 stored
    // `true` before migrating, so the flip must re-apply below v3.
    migrated.graphScopeAll = false
  }
  migrated.prefsVersion = PREFERENCES_VERSION
  return migrated
}

export const DEFAULT_SECTIONS: Record<SectionKey, boolean> = {
  favorites: true,
  localBranches: true,
  remoteBranches: false,
  tags: false,
  stashes: false,
  worktrees: false,
}

export const DEFAULT_COLUMNS: CommitColumnPrefs = {
  author: false,
  sha: false,
  when: true,
}

export type CommitColumnWidthKey = 'refs' | 'graph' | 'commit' | 'sha' | 'when'

export interface CommitColumnWidths {
  refs: number
  graph: number
  commit: number
  sha: number
  when: number
}

export const DEFAULT_COLUMN_WIDTHS: CommitColumnWidths = {
  refs: 140,
  graph: 300,
  commit: 240,
  sha: 56,
  when: 96,
}

export const COLUMN_WIDTH_LABELS: Record<CommitColumnWidthKey, string> = {
  refs: 'Branch / tag',
  graph: 'Graph',
  commit: 'Commit',
  sha: 'SHA',
  when: 'When',
}

export type CommitColumnDropSide = 'left' | 'right'

/**
 * Column order from left to right, fully user-reorderable. The graph lane
 * overlay and refs→node connector lines read the order to position
 * themselves, so no position is off limits.
 */
export const DEFAULT_COLUMN_ORDER: CommitColumnWidthKey[] = [
  'refs',
  'graph',
  'commit',
  'sha',
  'when',
]

const COLUMN_ORDER_KEYS = new Set<string>(DEFAULT_COLUMN_ORDER)

export function sanitizeColumnOrder(saved: unknown): CommitColumnWidthKey[] {
  const order: CommitColumnWidthKey[] = []
  if (Array.isArray(saved)) {
    for (const key of saved) {
      if (
        typeof key === 'string' &&
        COLUMN_ORDER_KEYS.has(key) &&
        !order.includes(key as CommitColumnWidthKey)
      ) {
        order.push(key as CommitColumnWidthKey)
      }
    }
  }
  for (const key of DEFAULT_COLUMN_ORDER) {
    if (!order.includes(key)) order.push(key)
  }
  return order
}

export const COLUMN_WIDTH_LIMITS: Record<CommitColumnWidthKey, { min: number; max: number }> = {
  refs: { min: 56, max: 360 },
  graph: { min: 56, max: 360 },
  commit: { min: 120, max: 720 },
  sha: { min: 56, max: 360 },
  when: { min: 56, max: 360 },
}

export function clampColumnWidth(key: CommitColumnWidthKey, value: number): number {
  const { min, max } = COLUMN_WIDTH_LIMITS[key]
  return Math.min(max, Math.max(min, Math.round(value)))
}

export function defaultPreferences(): UserPreferences {
  return {
    prefsVersion: PREFERENCES_VERSION,
    leftSize: 18,
    rightSize: 24,
    workingTreeChangesSize: DEFAULT_WORKING_TREE_CHANGES_SIZE,
    leftCollapsed: false,
    rightCollapsed: false,
    sections: { ...DEFAULT_SECTIONS },
    visibleSections: { ...DEFAULT_VISIBLE_SECTIONS },
    columns: { ...DEFAULT_COLUMNS },
    columnWidths: { ...DEFAULT_COLUMN_WIDTHS },
    columnOrder: [...DEFAULT_COLUMN_ORDER],
    graphScopeAll: false,
    commitGraphLimit: DEFAULT_COMMIT_GRAPH_LIMIT,
    fileListView: 'path',
    lastRepositoryPath: null,
    openRepoTabs: [],
    diffViewMode: 'unified',
    theme: 'default',
    uiMode: 'advanced',
    ignoreWhitespace: false,
    blameWordWrap: false,
    diffWordWrap: false,
    signOffCommit: false,
    signCommit: false,
    autoFetchIntervalSec: DEFAULT_AUTO_FETCH_INTERVAL_SEC,
    keybindings: undefined,
  }
}

export function mergePreferences(saved: Partial<UserPreferences> | null): UserPreferences {
  const defaults = defaultPreferences()
  if (!saved) return defaults
  // Older prefs carried a dedicated Author column width, now gone: the author
  // always renders inside the graph column. `sanitizeColumnOrder` handles the
  // matching stale column-order entry; drop the stale width here.
  const savedWidths: Record<string, unknown> = { ...(saved.columnWidths ?? {}) }
  delete savedWidths.author
  return {
    leftSize: clampLeftSidebarSize(saved.leftSize ?? defaults.leftSize),
    rightSize: clampRightSidebarSize(saved.rightSize ?? defaults.rightSize),
    workingTreeChangesSize: clampWorkingTreeChangesSize(
      saved.workingTreeChangesSize ?? defaults.workingTreeChangesSize,
    ),
    leftCollapsed: saved.leftCollapsed ?? defaults.leftCollapsed,
    rightCollapsed: saved.rightCollapsed ?? defaults.rightCollapsed,
    sections: { ...DEFAULT_SECTIONS, ...(saved.sections ?? {}) },
    visibleSections: { ...DEFAULT_VISIBLE_SECTIONS, ...(saved.visibleSections ?? {}) },
    columns: { ...DEFAULT_COLUMNS, ...(saved.columns ?? {}) },
    columnWidths: { ...DEFAULT_COLUMN_WIDTHS, ...savedWidths } as CommitColumnWidths,
    columnOrder: sanitizeColumnOrder(saved.columnOrder),
    graphScopeAll: saved.graphScopeAll ?? defaults.graphScopeAll,
    commitGraphLimit: clampCommitGraphLimit(saved.commitGraphLimit ?? defaults.commitGraphLimit),
    fileListView: saved.fileListView ?? defaults.fileListView,
    lastRepositoryPath: saved.lastRepositoryPath ?? defaults.lastRepositoryPath,
    openRepoTabs: Array.isArray(saved.openRepoTabs)
      ? saved.openRepoTabs.filter(
          (tab): tab is RepoTabPref =>
            typeof tab === 'object' &&
            tab != null &&
            typeof tab.path === 'string' &&
            typeof tab.name === 'string',
        )
      : defaults.openRepoTabs,
    diffViewMode: saved.diffViewMode === 'split' ? 'split' : 'unified',
    theme: isThemePreference(saved.theme) ? saved.theme : defaults.theme,
    uiMode: isUiMode(saved.uiMode) ? saved.uiMode : defaults.uiMode,
    ignoreWhitespace: saved.ignoreWhitespace ?? defaults.ignoreWhitespace,
    blameWordWrap: saved.blameWordWrap ?? defaults.blameWordWrap,
    diffWordWrap: saved.diffWordWrap ?? defaults.diffWordWrap,
    signOffCommit: saved.signOffCommit ?? defaults.signOffCommit,
    signCommit: saved.signCommit ?? defaults.signCommit,
    autoFetchIntervalSec: clampAutoFetchIntervalSec(
      saved.autoFetchIntervalSec ?? defaults.autoFetchIntervalSec,
    ),
    keybindings: saved.keybindings !== undefined ? sanitizeKeymap(saved.keybindings) : undefined,
    prefsVersion: PREFERENCES_VERSION,
  }
}
