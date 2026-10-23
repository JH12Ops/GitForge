/**
 * Row model for the commit graph list.
 *
 * Order (top to bottom): the uncommitted-changes (pending) row pinned at the
 * very top of the graph (GitKraken anchors WIP to the top so later refs —
 * pulled remote tips, other-branch commits — sort below it), then the current
 * branch's HEAD row, then every stash pinned directly below it in stash-index
 * order regardless of which commit each stash was created on (GitKraken-style;
 * a dotted connector drops from the stash box to its base commit), then the
 * remaining commits newest-first.
 */

export type GraphRow =
  | { kind: 'pending' }
  | { kind: 'stash'; stashIndex: number }
  | { kind: 'commit'; commitIndex: number }

export function buildGraphRows(
  commits: { sha: string }[],
  headIndex: number,
  hasPendingRow: boolean,
  stashes: { baseSha: string | null }[],
): GraphRow[] {
  const rows: GraphRow[] = []
  if (hasPendingRow) rows.push({ kind: 'pending' })
  for (
    let commitIndex = 0;
    commitIndex <= Math.min(headIndex, commits.length - 1);
    commitIndex++
  ) {
    rows.push({ kind: 'commit', commitIndex })
  }
  for (let stashIndex = 0; stashIndex < stashes.length; stashIndex++) {
    rows.push({ kind: 'stash', stashIndex })
  }
  for (let commitIndex = Math.max(headIndex + 1, 0); commitIndex < commits.length; commitIndex++) {
    rows.push({ kind: 'commit', commitIndex })
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
