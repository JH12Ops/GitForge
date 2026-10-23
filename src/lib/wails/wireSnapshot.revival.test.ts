import { expect, describe, it } from 'vitest'
import { wireSnapshotToRepositoryData } from '@/lib/git/wireAdapter'
import snapshot from './fixtures/wails-snapshot.json'

/**
 * Regression guard for the Go (Wails) backend: the exact wire JSON the Go
 * gateway produces must revive through wireAdapter without throwing — the
 * renderer previously crashed at mount with a real Go snapshot (see
 * ~/Library/Logs/gitforge.log).
 */
describe('wails wire snapshot revival', () => {
  it('revives a real Go snapshot', () => {
    const data = wireSnapshotToRepositoryData(
      snapshot as Parameters<typeof wireSnapshotToRepositoryData>[0],
    )
    expect(data.repository.name).toBe('gitforge')
    expect(data.commits.length).toBeGreaterThan(0)
    expect(data.workingTree.length).toBeGreaterThan(0)
  })
})
