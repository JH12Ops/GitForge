#!/usr/bin/env node
/**
 * Decide (and optionally apply) the automatic version bump for releases from
 * `main`. Invoked by .github/workflows/release.yml; also runnable locally for
 * a dry run (it only writes package.json when passed --apply).
 *
 * Decision rules:
 * - Tag v<package.json version> already exists → nothing to release.
 * - The package.json version is already ahead of the highest `v*` tag → ship
 *   it as-is (recovery: the bump commit landed but the tag push failed).
 * - Otherwise bump the highest released version from commit messages since
 *   that tag:
 *   - default: patch
 *   - `feat: …` / `feat(scope): …` → minor
 *   - breaking changes (`feat!: …`, a `BREAKING CHANGE:` footer,
 *     or `+semver: major`) → major
 *   - `+semver: minor` / `+semver: patch` in any commit message overrides
 *
 * Output (GITHUB_OUTPUT-compatible `key=value` lines on stdout; logs on stderr):
 *   should_release  "true" | "false"
 *   previous        last released version ("" before the first tag)
 *   version         the version to ship
 *   level           patch | minor | major | none
 *
 * Usage:
 *   node scripts/release-bump.mjs           # decide only (dry run)
 *   node scripts/release-bump.mjs --apply   # also bump package.json when releasing
 */
import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const apply = process.argv.includes('--apply')
const packageJsonPath = join(root, 'package.json')

const LEVEL_ORDER = { patch: 0, minor: 1, major: 2 }

function git(args) {
  return execFileSync('git', args, { encoding: 'utf8', cwd: root }).trim()
}

function log(...parts) {
  console.error(...parts)
}

/**
 * @param {string} version
 * @returns {[number, number, number] | null}
 */
function parseSemver(version) {
  const match = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(version.trim())
  return match ? [Number(match[1]), Number(match[2]), Number(match[3])] : null
}

/** @param {string} a @param {string} b @returns {number} positive when `a` outranks `b` */
function compareSemver(a, b) {
  const parsedA = parseSemver(a)
  const parsedB = parseSemver(b)
  if (!parsedA || !parsedB) return 0
  for (let i = 0; i < 3; i += 1) {
    if (parsedA[i] !== parsedB[i]) return parsedA[i] - parsedB[i]
  }
  return 0
}

/**
 * @param {string} version
 * @param {'major' | 'minor' | 'patch'} level
 * @returns {string}
 */
function bumpVersion(version, level) {
  const [major, minor, patch] = parseSemver(version) ?? [0, 0, 0]
  if (level === 'major') return `${major + 1}.0.0`
  if (level === 'minor') return `${major}.${minor + 1}.0`
  return `${major}.${minor}.${patch + 1}`
}

/**
 * @param {string} text commit messages since the last release tag
 * @returns {'major' | 'minor' | 'patch'}
 */
function bumpLevel(text) {
  let level = 'patch'

  /** @param {'major' | 'minor' | 'patch'} candidate */
  const raise = (candidate) => {
    if (LEVEL_ORDER[candidate] > LEVEL_ORDER[level]) level = candidate
  }

  const override = /(?:^|\s)\+semver:\s*(major|minor|patch)\b/i.exec(text)
  if (override) raise(override[1].toLowerCase())
  if (/^BREAKING CHANGE[: ]/m.test(text)) raise('major')

  for (const line of text.split('\n')) {
    const header = /^(\w+)(?:\([^)]*\))?(!)?:/.exec(line)
    if (!header) continue
    if (header[2] === '!') {
      raise('major')
    } else if (header[1] === 'feat') {
      raise('minor')
    }
  }

  return level
}

/**
 * @param {'true' | 'false'} shouldRelease
 * @param {string} previous
 * @param {string} version
 * @param {'major' | 'minor' | 'patch' | 'none'} level
 */
function emit(shouldRelease, previous, version, level) {
  console.log(`should_release=${shouldRelease}`)
  console.log(`previous=${previous}`)
  console.log(`version=${version}`)
  console.log(`level=${level}`)
}

function main() {
  const packageJson = JSON.parse(readFileSync(packageJsonPath, 'utf8'))
  const current = packageJson.version

  if (!parseSemver(current)) {
    throw new Error(`Invalid semver in package.json: ${current}`)
  }

  const tags = git(['tag', '--list', 'v*'])
    .split('\n')
    .map((tag) => tag.trim())
    .filter((tag) => tag && parseSemver(tag))
  tags.sort(compareSemver)

  const previousTag = tags[tags.length - 1] ?? ''
  const previous = previousTag ? previousTag.slice(1) : ''

  // Already released: the current version's tag points at HEAD (the bump
  // commit itself, or a re-run via workflow_dispatch). A tag elsewhere in the
  // history is the start of a normal next release and must still bump.
  let currentTagAtHead = false

  if (tags.includes(`v${current}`)) {
    // ^{commit} dereferences annotated tags to the commit they point to
    currentTagAtHead =
      git(['rev-parse', '--verify', `v${current}^{commit}`]) ===
      git(['rev-parse', 'HEAD'])
  }

  if (currentTagAtHead) {
    log(`v${current} already points at HEAD; nothing to release.`)
    emit('false', previous, current, 'none')
    return
  }

  let version
  let level

  if (!previousTag || compareSemver(current, previous) > 0) {
    version = current
    level = 'none'
    log(
      `package.json ${current} is ahead of ${previousTag || 'any tag'}; shipping it as-is.`,
    )
  } else {
    level = bumpLevel(git(['log', '--format=%B', `${previousTag}..HEAD`]))
    version = bumpVersion(previous, level)
    log(`Commits since ${previousTag} suggest a ${level} bump: ${current} → ${version}.`)
  }

  if (apply && version !== current) {
    const raw = readFileSync(packageJsonPath, 'utf8')
    const updated = raw.replace(
      /^(  "version": ")\d+\.\d+\.\d+(",$)/m,
      `$1${version}$2`,
    )

    if (updated === raw) {
      throw new Error(`Could not rewrite "version" in package.json for ${version}`)
    }

    writeFileSync(packageJsonPath, updated)
    log(`package.json → ${version}`)
  }

  emit('true', previous, version, level)
}

try {
  main()
} catch (error) {
  log(`release-bump failed: ${error instanceof Error ? error.message : error}`)
  process.exit(1)
}
