import { describe, expect, it } from 'vitest'
import { headCommitIndex } from './pendingGraphRow'

const commits = [
  { sha: 'c', refs: [{ isHead: true }] },
  { sha: 'b', refs: [] },
  { sha: 'a', refs: [] },
]

describe('pendingGraphRow', () => {
  it('finds HEAD by sha or ref', () => {
    expect(headCommitIndex(commits, 'c')).toBe(0)
    expect(headCommitIndex(commits)).toBe(0)
    expect(headCommitIndex(commits, 'missing')).toBe(0)
  })
})
