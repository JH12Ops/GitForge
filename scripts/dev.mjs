#!/usr/bin/env node
// Browser dev orchestrator: vite dev server + headless Go devgateway.
//
// The gateway serves /gitforge/* (API + SSE) with real git data; vite.config
// proxies that prefix to it when GITFORGE_LOCAL_TEST_ADDR is exported. Live
// reload splits by layer:
//
//   - frontend (src/**, *.vue, *.ts): native vite HMR, instant, no restarts
//   - backend (go-backend/**/*.go): rebuilt and restarted here; the open tab
//     re-fetches on the next action while SSE reconnects automatically
//
// Kill with Ctrl+C (children are cleaned up).
import { spawn, spawnSync } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import url from 'node:url'

const REPO = path.resolve(path.dirname(url.fileURLToPath(import.meta.url)), '..')
const GO_DIR = path.join(REPO, 'go-backend')
const ADDR = process.env.GITFORGE_LOCAL_TEST_ADDR || '127.0.0.1:18333'
// vite.config reads this env to wire the /gitforge proxy (and the gateway
// child inherits it); make sure it is set even when the default was used.
process.env.GITFORGE_LOCAL_TEST_ADDR ||= ADDR
const GATEWAY_BIN = path.join(os.tmpdir(), 'gitforge-devgateway')
const [host, port] = ADDR.split(':')

const tag = (name, color) => (line) =>
  process.stdout.write(`\x1b[${color}m${name}\x1b[0m ${line}\n`)
const prefixStream = (stream, tag, color) => {
  let buffer = ''
  stream.setEncoding('utf8')
  stream.on('data', (chunk) => {
    buffer += chunk
    let newline
    while ((newline = buffer.indexOf('\n')) !== -1) {
      const line = buffer.slice(0, newline)
      buffer = buffer.slice(newline + 1)
      if (line.trim()) tag(line, color)
    }
  })
}
const say = tag('[go]', '36')
const die = (msg) => {
  console.error(`[dev] ${msg}`)
  process.exit(1)
}

/** Builds ./cmd/devgateway; returns false on compile error (keeps vite up). */
function buildGateway() {
  const res = spawnSync('go', ['build', '-o', GATEWAY_BIN, './cmd/devgateway'], {
    cwd: GO_DIR,
    env: process.env,
    stdio: 'inherit',
  })
  if (res.status !== 0) {
    console.error('[dev] go build failed — fix the errors; retrying on the next .go save')
    return false
  }
  return true
}

let gateway = null
let vite = null
let gatewayGeneration = 0

function startGateway() {
  gatewayGeneration++
  const generation = gatewayGeneration
  gateway = spawn(GATEWAY_BIN, [], {
    cwd: GO_DIR,
    env: { ...process.env, GITFORGE_LOCAL_TEST_ADDR: ADDR },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  prefixStream(gateway.stdout, say, '36')
  prefixStream(gateway.stderr, say, '31')
  gateway.on('exit', (code) => {
    if (generation === gatewayGeneration) {
      gateway = null
      if (shuttingDown || restarting) return // deliberate kill or rebuild
      // Died before its first successful serve is unrecoverable in practice
      // (port race, vague host config): report now instead of spin-respawning.
      if (!gatewayEverReady) {
        die(
          `gateway died immediately (exit ${code ?? '?'}). ` +
            `Is something already on ${ADDR}? Try: lsof -ti :${port || 18333}`,
        )
      }
      // Runtime crash: report and schedule one respawn.
      console.error(`[dev] gateway exited (${code}); restarting…`)
      setTimeout(restartGateway, 800)
    }
  })
}

/** Six-pin wait until the gateway answers the ping route (or timeout). */
async function waitForGateway(timeoutMs) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (gateway === null) return false // died mid-build
    if (await probeGateway()) return true
    await new Promise((r) => setTimeout(r, 300))
  }
  return false
}

function probeGateway() {
  return new Promise((resolve) => {
    const socket = net.connect(Number(port || 18333), host, () => {
      socket.destroy()
      resolve(true)
    })
    socket.on('error', () => resolve(false))
    // A gateway that accepts but never answers is still dead for our purposes.
    socket.setTimeout(2000, () => {
      socket.destroy()
      resolve(false)
    })
  })
}

/** True when something already listens on host:port. */
function isPortInUse(host, portNo) {
  return new Promise((resolve) => {
    const socket = net.connect(portNo, host, () => {
      socket.destroy()
      resolve(true)
    })
    socket.on('error', () => resolve(false))
    socket.setTimeout(1000, () => {
      socket.destroy()
      resolve(false)
    })
  })
}

let restarting = false
let pendingRestart = false
let shuttingDown = false
let gatewayEverReady = false

async function restartGateway() {
  if (shuttingDown) return
  if (restarting) {
    pendingRestart = true // collapse a burst of saves into one rebuild
    return
  }
  restarting = true
  console.log('[dev] .go change detected — rebuilding gateway…')
  if (gateway !== null) {
    const child = gateway
    gateway = null
    gatewayGeneration++ // swallow this child's exit event
    child.kill('SIGTERM')
    await new Promise((resolve) => child.on('exit', resolve))
  }
  if (buildGateway()) startGateway()
  restarting = false
  if (pendingRestart) {
    pendingRestart = false
    void restartGateway()
  }
}

async function main() {
  if (!fs.existsSync(path.join(REPO, 'node_modules', '.bin', 'vite'))) {
    die('node_modules missing — run `npm install` first')
  }

  // Fail fast on occupied ports with an actionable message, instead of the
  // cryptic vite "Port already in use" or a hung gateway probe.
  const vitePortConflict = await isPortInUse('127.0.0.1', 1420)
  const gatewayPortConflict = await isPortInUse(host, Number(port || 18333))
  if (vitePortConflict || gatewayPortConflict) {
    die(
      'another dev session (or orphan) holds a needed port' +
        ` — free it and rerun:  lsof -ti :${vitePortConflict ? 1420 : port || 18333} | xargs kill`,
    )
  }

  process.on('SIGINT', shutdown)
  process.on('SIGTERM', shutdown)

  console.log(
    '[dev] browser dev — frontend on http://localhost:1420 with the live Go gateway. ' +
      '(No desktop window opens here; that is `npm run dev:go`.)',
  )
  console.log('[dev] building the Go gateway (first build can take a few seconds)…')
  if (!buildGateway()) die('the devgateway failed to compile; see the Go errors above')
  startGateway()

  const ready = await waitForGateway(40_000)
  if (!ready) die(`gateway never came up on ${ADDR}`)
  gatewayEverReady = true
  console.log(`[dev] gateway ready on ${ADDR}`)

  console.log('[dev] starting vite…')
  if (vite === null) {
    vite = spawn(path.join(REPO, 'node_modules', '.bin', 'vite'), ['--open'], {
      cwd: REPO,
      env: process.env, // carries GITFORGE_LOCAL_TEST_ADDR for the vite proxy
      stdio: ['ignore', 'inherit', 'inherit'],
    })
  }
  vite.on('exit', (code) => shutdown(code ?? 0))
  fs.watch(GO_DIR, { recursive: true }, (_event, filename) => {
    if (filename && filename.endsWith('.go')) void restartGateway()
  })
}

function shutdown(code = 0) {
  if (shuttingDown) return
  shuttingDown = true
  gateway?.kill('SIGTERM')
  vite?.kill('SIGTERM')
  process.exit(code)
}

main().catch((err) => die(String(err?.stack ?? err)))
