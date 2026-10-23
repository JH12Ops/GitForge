import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { CommitColumnWidthKey } from '@/lib/preferences'
import { useUiStore } from './ui'

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('sidebar section visibility', () => {
  it('shows every section by default', () => {
    const ui = useUiStore()
    for (const key of [
      'favorites',
      'localBranches',
      'remoteBranches',
      'tags',
      'stashes',
      'worktrees',
    ]) {
      expect(ui.isSectionVisible(key)).toBe(true)
    }
  })

  it('toggles and sets section visibility without touching collapse state', () => {
    const ui = useUiStore()
    expect(ui.isSectionOpen('tags')).toBe(false)

    ui.toggleSectionVisible('tags')
    expect(ui.isSectionVisible('tags')).toBe(false)
    expect(ui.isSectionOpen('tags')).toBe(false)

    ui.setSectionVisible('tags', true)
    expect(ui.isSectionVisible('tags')).toBe(true)
  })

  it('allows hiding every section', () => {
    const ui = useUiStore()
    for (const key of [
      'favorites',
      'localBranches',
      'remoteBranches',
      'tags',
      'stashes',
      'worktrees',
    ]) {
      ui.setSectionVisible(key, false)
      expect(ui.isSectionVisible(key)).toBe(false)
    }
  })

  it('derives a tri-state per section', () => {
    const ui = useUiStore()
    expect(ui.sectionState('tags')).toBe('collapsed')

    ui.setSectionState('tags', 'expanded')
    expect(ui.sectionState('tags')).toBe('expanded')

    ui.setSectionState('tags', 'collapsed')
    expect(ui.sectionState('tags')).toBe('collapsed')

    ui.setSectionState('tags', 'hidden')
    expect(ui.sectionState('tags')).toBe('hidden')
    expect(ui.isSectionVisible('tags')).toBe(false)
  })

  it('restores the remembered open state when unhiding a section', () => {
    const ui = useUiStore()
    ui.setSectionState('localBranches', 'expanded')
    ui.setSectionState('localBranches', 'hidden')
    ui.setSectionState('localBranches', 'hidden')
    expect(ui.sectionState('localBranches')).toBe('hidden')

    ui.setSectionState('localBranches', 'expanded')
    expect(ui.sectionState('localBranches')).toBe('expanded')
  })
})

describe('commit graph column widths', () => {
  it('clamps and resets widths', () => {
    const ui = useUiStore()
    ui.setColumnWidth('graph', 1)
    expect(ui.columnWidths.graph).toBe(56)

    ui.setColumnWidth('graph', 10_000)
    expect(ui.columnWidths.graph).toBe(360)

    ui.resetColumnWidths()
    expect(ui.columnWidths.graph).toBe(300)
  })
})

describe('commit graph column order', () => {
  it('starts with the default order including the pinned pair', () => {
    const ui = useUiStore()
    expect(ui.columnOrder).toEqual(['refs', 'graph', 'commit', 'sha', 'when'])
  })

  it('renders every visible column in saved order, including left of graph', () => {
    const ui = useUiStore()
    ui.setColumnVisible('when', true)
    // Drop `When` onto the graph header cell's left edge.
    ui.moveColumn('when', 'graph', 'left')
    expect(ui.columnOrder).toEqual(['refs', 'when', 'graph', 'commit', 'sha'])
    expect(ui.orderedColumnOrder).toEqual(['refs', 'when', 'graph', 'commit'])
    expect(ui.tailColumnOrder).toEqual(['when', 'commit'])
  })

  it('derives tail order from visible columns', () => {
    const ui = useUiStore()
    expect(ui.tailColumnOrder).toEqual(['commit', 'when'])

    ui.setColumnVisible('sha', true)
    ui.setColumnVisible('author', true)
    ui.setColumnOrder([
      'refs',
      'graph',
      'sha',
      'sha',
      'bogus',
      'when',
      'author',
      undefined,
    ] as unknown as CommitColumnWidthKey[])
    // Stale Author entries are dropped: the author always renders inside the
    // graph column instead of taking a column of its own.
    expect(ui.columnOrder).toEqual(['refs', 'graph', 'sha', 'when', 'commit'])
    expect(ui.tailColumnOrder).toEqual(['sha', 'when', 'commit'])

    ui.setColumnVisible('sha', false)
    expect(ui.tailColumnOrder).toEqual(['when', 'commit'])
  })

  it('keeps the author inline in the graph column instead of a dedicated column', () => {
    const ui = useUiStore()
    ui.setColumnVisible('author', true)
    expect(ui.tailColumnOrder).toEqual(['commit', 'when'])
    // Even the default narrow-width edge case never resurrects the column.
    ui.setColumnWidth('graph', 56)
    expect(ui.tailColumnOrder).toEqual(['commit', 'when'])
  })

  it('moves a column to the right or left edge of a target', () => {
    const ui = useUiStore()
    ui.setColumnVisible('sha', true)

    ui.moveColumn('when', 'commit', 'left')
    expect(ui.columnOrder).toEqual(['refs', 'graph', 'when', 'commit', 'sha'])

    ui.moveColumn('when', 'sha', 'right')
    expect(ui.columnOrder).toEqual(['refs', 'graph', 'commit', 'sha', 'when'])
  })

  it('can move columns left of the graph column', () => {
    const ui = useUiStore()
    ui.moveColumn('when', 'graph', 'left')
    expect(ui.columnOrder).toEqual(['refs', 'when', 'graph', 'commit', 'sha'])

    ui.moveColumn('graph', 'when', 'right')
    expect(ui.columnOrder).toEqual(['refs', 'when', 'graph', 'commit', 'sha'])

    ui.moveColumn('graph', 'refs', 'left')
    expect(ui.columnOrder).toEqual(['graph', 'refs', 'when', 'commit', 'sha'])
  })

  it('is a no-op when moving to the same column', () => {
    const ui = useUiStore()
    ui.setColumnOrder(['refs', 'graph', 'when', 'sha', 'commit'])
    ui.moveColumn('when', 'when', 'left')
    expect(ui.columnOrder).toEqual(['refs', 'graph', 'when', 'sha', 'commit'])
  })
})

describe('auto sidebar toggle for diff view', () => {
  it('collapses an expanded sidebar and requests instant panes', () => {
    const ui = useUiStore()
    expect(ui.leftCollapsed).toBe(false)

    ui.autoCollapseLeftForDiff()

    expect(ui.leftCollapsed).toBe(true)
    expect(ui.suppressPaneAnimation).toBe(true)
  })

  it('ignores duplicate open notifications while a diff is active', () => {
    const ui = useUiStore()

    ui.autoCollapseLeftForDiff()
    ui.leftCollapsed = false // user manually re-expanded mid-diff
    ui.autoCollapseLeftForDiff()

    // Still tracking the original pre-diff state, not the manual toggle.
    ui.restoreLeftAfterDiff()
    expect(ui.leftCollapsed).toBe(false)
  })

  it('restores an expanded sidebar on diff close', () => {
    const ui = useUiStore()

    ui.autoCollapseLeftForDiff()
    ui.restoreLeftAfterDiff()

    expect(ui.leftCollapsed).toBe(false)
    expect(ui.suppressPaneAnimation).toBe(true)
    expect(ui.leftCollapsedBeforeDiff).toBeNull()
  })

  it('leaves an already-collapsed sidebar collapsed without animation', () => {
    const ui = useUiStore()
    ui.leftCollapsed = true

    ui.autoCollapseLeftForDiff()

    expect(ui.leftCollapsed).toBe(true)
    expect(ui.suppressPaneAnimation).toBe(false)

    ui.restoreLeftAfterDiff()

    expect(ui.leftCollapsed).toBe(true)
    expect(ui.leftCollapsedBeforeDiff).toBeNull()
  })

  it('ignores restore without a preceding auto-collapse', () => {
    const ui = useUiStore()

    ui.restoreLeftAfterDiff()

    expect(ui.leftCollapsed).toBe(false)
    expect(ui.suppressPaneAnimation).toBe(false)
  })
})
