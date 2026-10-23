/**
 * Row model for the commit graph list.
 *
 * Order (top to bottom): commits newest-first, with the uncommitted-changes
 * (pending) row anchored immediately above the HEAD commit's row and each
 * stash inserted immediately above its base commit (GitKraken-style). When
 * HEAD is not in the loaded commit window the pending row falls back to the
 * top, right below unanchored stashes.
 */

export type GraphRow =
  | { kind: 'pending' }
  | { kind: 'stash'; stashIndex: number }
  | { kind: 'commit'; commitIndex: number }

import { buildCommitIndex } from './commitIndex'

export function buildGraphRows(
  commits: { sha: string }[],
  hasPendingRow: boolean,
  stashes: { baseSha: string | null }[],
  headCommitIndex = -1,
): GraphRow[] {
  const indexBySha = buildCommitIndex(commits)
  const byBase = new Map<number, number[]>()
  const unanchored: number[] = []
  for (let i = 0; i < stashes.length; i++) {
    const baseSha = stashes[i].baseSha
    const baseIndex = baseSha ? (indexBySha.get(baseSha) ?? -1) : -1
    if (baseIndex >= 0) {
      const list = byBase.get(baseIndex) ?? []
      list.push(i)
      byBase.set(baseIndex, list)
    } else {
      unanchored.push(i)
    }
  }

  const rows: GraphRow[] = []
  for (const stashIndex of unanchored) rows.push({ kind: 'stash', stashIndex })
  for (let commitIndex = 0; commitIndex < commits.length; commitIndex++) {
    for (const stashIndex of byBase.get(commitIndex) ?? []) {
      rows.push({ kind: 'stash', stashIndex })
    }
    // The pending row tracks the working tree of the HEAD commit, so it
    // anchors there rather than blindly at the top of the list (with
    // `--all` scope, other refs can sort above HEAD).
    if (hasPendingRow && commitIndex === headCommitIndex) rows.push({ kind: 'pending' })
    rows.push({ kind: 'commit', commitIndex })
  }
  if (hasPendingRow && !rows.some((row) => row.kind === 'pending')) {
    rows.unshift({ kind: 'pending' })
  }
  return rows
}

/** Maps each commit index to its virtual row index. */
export function commitVirtualIndexByCommit(rows: GraphRow[]): Map<number, number> {
  const map = new Map<number, number>()
  rows.forEach((row, virtualIndex) => {
    if (row.kind === 'commit') map.set(row.commitIndex, virtualIndex)
  })
  return map
}

/** Maps each stash index to its virtual row index. */
export function stashVirtualIndexByStash(rows: GraphRow[]): Map<number, number> {
  const map = new Map<number, number>()
  rows.forEach((row, virtualIndex) => {
    if (row.kind === 'stash') map.set(row.stashIndex, virtualIndex)
  })
  return map
}
