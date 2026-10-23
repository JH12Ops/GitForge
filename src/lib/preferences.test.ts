import { describe, expect, it } from 'vitest'
import { migratePreferences, PREFERENCES_VERSION } from './preferences'

describe('migratePreferences', () => {
  it('passes through already-current preferences', () => {
    const saved = { prefsVersion: PREFERENCES_VERSION, graphScopeAll: true }
    expect(migratePreferences(saved)).toBe(saved)
  })

  it('flips the graph scope off for a pre-v3 stamp written by an intermediate build', () => {
    const saved = { prefsVersion: 2, graphScopeAll: true }
    const migrated = migratePreferences(saved)
    expect(migrated?.graphScopeAll).toBe(false)
    expect(migrated?.prefsVersion).toBe(PREFERENCES_VERSION)
  })

  it('flips the graph scope off for v1 preferences', () => {
    const saved = { graphScopeAll: true }
    const migrated = migratePreferences(saved)
    expect(migrated?.graphScopeAll).toBe(false)
    expect(migrated?.prefsVersion).toBe(PREFERENCES_VERSION)
  })

  it('keeps everything else on the migration path', () => {
    const saved = { leftSize: 30, graphScopeAll: true, sections: { worktrees: true } }
    const migrated = migratePreferences(saved)
    expect(migrated).toMatchObject({ leftSize: 30, sections: { worktrees: true } })
  })

  it('does not re-apply to later versions', () => {
    const saved = { prefsVersion: PREFERENCES_VERSION + 1, graphScopeAll: true }
    // An unknown newer version still passes through untouched.
    expect(migratePreferences(saved)).toEqual(saved)
  })
})
