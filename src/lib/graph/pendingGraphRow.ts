/**
 * Locates the HEAD commit row for graph anchoring. Prefers the repository
 * `headSha`, falling back to the `isHead` decoration when HEAD is detached
 * or unknown.
 */

export function headCommitIndex(
  commits: { sha: string; refs: { isHead?: boolean }[] }[],
  headSha?: string,
): number {
  if (headSha) {
    const bySha = commits.findIndex((c) => c.sha === headSha)
    if (bySha >= 0) return bySha
  }
  return commits.findIndex((c) => c.refs.some((r) => r.isHead))
}
