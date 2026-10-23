#!/usr/bin/env node
// Desktop dev orchestrator: rebuild + relaunch GitForge.app on changes.
//
// `dev:go` is one-shot; this wraps it in a watch loop so iteration happens
// in the real window. Reload is whole-app by design: the Wails asset handler
// serves `dist/` statically, so every layer restarts together.
//
//   - go-backend/**/*.go and frontend sources (src/**, shared/**, index.html)
//     each trigger: quit app → `npm run dev:go` (vite build + go build + open)
//
// Kill with Ctrl+C (builds stop; the already-open window stays).
import { spawn, spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import url from 'node:url'

const REPO = path.resolve(path.dirname(url.fileURLToPath(import.meta.url)), '..')
const APP_BIN = path.join(REPO, 'go-backend', 'GitForge.app')

const say = (line) => process.stdout.write(`[dev:go] ${line}\n`)
const die = (msg) => {
  console.error(`[dev:go] ${msg}`)
  process.exit(1)
}

/** Watches that imply a rebuild. `$file` matches a single watched file. */
const WATCHED = [
  { root: path.join(REPO, 'go-backend'), match: (file) => file.endsWith('.go') },
  { root: path.join(REPO, 'src'), match: (file) => /\.(ts|vue|css|html)$/.test(file) },
  { root: path.join(REPO, 'shared'), match: (file) => /\.(ts|css|html)$/.test(file) },
  { root: path.join(REPO, 'index.html'), match: () => true },
]

if (!fs.existsSync(path.join(REPO, 'node_modules', '.bin', 'vite'))) {
  die('node_modules missing — run `npm install` first')
}

/** True while `npm run dev:go` runs; watch events are collapsed meanwhile. */
let building = false
let pendingRebuild = false
let debounceTimer

/** Quit the running app so its bundle binary is freely replaceable. */
function quitApp() {
  spawnSync('pkill', ['-x', 'GitForge'])
  // Give the watchdog a beat to release the bundle before codesign rewrites it.
  return new Promise((resolve) => setTimeout(resolve, 300))
}

async function rebuild() {
  if (building) {
    pendingRebuild = true // collapse a burst of saves into one relaunch
    return
  }
  building = true
  say('change detected — quitting app and rebuilding…')
  await quitApp()
  const res = spawnSync('npm', ['run', 'dev:go'], {
    cwd: REPO,
    env: process.env,
    stdio: 'inherit',
  })
  building = false
  if (res.status !== 0) {
    say('build failed — fix the errors; retriggering on the next save')
  }
  if (pendingRebuild) {
    pendingRebuild = false
    void rebuild()
  }
}

function scheduleRebuild() {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => void rebuild(), 200)
}

for (const { root, match } of WATCHED) {
  if (!fs.existsSync(root)) continue
  fs.watch(root, { recursive: true }, (_event, filename) => {
    if (!filename || !match(filename)) return
    scheduleRebuild()
  })
}

say('watching go-backend + renderer sources; Ctrl+C stops (window stays open)')
void rebuild()

process.on('SIGINT', () => process.exit(0))
process.on('SIGTERM', () => process.exit(0))
