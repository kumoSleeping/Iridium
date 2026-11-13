// A developer's test Bot, not shipped (docs/exec.md "开发测试用的 Bot"): pairs with an Iridium once,
// then lets its agents run command lines on this machine (ir sh). No agents of its own; anyone can
// write one like it. ir join <url> --shell, its agents turned off in 应用管理, does the same without Node.
// Started when wanted, not kept running: it exits after IDLE minutes with nothing started (30 by
// default, 0 never), and when the Iridium no longer takes its TOKEN (its Bot removed there).
// node scripts/dev/shell-bot.mjs <iridium url> [name]
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { Executor, iridiumKey, refused } from '../../plugins/dsh-remote-iridium/executor.js'

const url = (process.argv[2] || '').replace(/\/+$/, '')
const name = process.argv[3] || 'mac-shell'
if (!/^https?:\/\//.test(url)) throw new Error('usage: node scripts/dev/shell-bot.mjs https://your-iridium [name]')
// One per Iridium: its TOKEN and holders' files under the Iridium's host, as ir join keeps them.
const state = path.join(os.homedir(), '.iridium-shell-bot', iridiumKey(url))
const tokenFile = path.join(state, 'token')
const program = 'iridium-shell-bot 0.2'
const idle = Number(process.env.IDLE ?? 30) * 60000
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const say = (m) => console.log(`${new Date().toISOString()} ${m}`)

async function pair() {
  const hello = { name, os: process.platform === 'win32' ? 'windows' : process.platform, arch: process.arch, program, agents: [], agentShell: true }
  for (;;) {
    const r = await fetch(`${url}/v1/internal/pair`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(hello) })
    if (r.status === 403) {
      say('pairing is closed there (配对 in 应用管理): waiting')
      await sleep(5000)
      continue
    }
    if (!r.ok) throw new Error(`pair: ${r.status} ${await r.text()}`)
    const req = await r.json()
    say(`pairing code ${req.code}: allow "${name}" on the Iridium page`)
    for (;;) {
      await sleep(2000)
      const s = await fetch(`${url}/v1/internal/pair/${req.request}`)
      const st = s.ok ? await s.json() : { status: s.status === 404 || s.status === 422 ? 'expired' : 'pending' }
      if (st.status === 'allowed' && st.token) return st.token
      if (st.status === 'denied') throw new Error('declined')
      if (st.status === 'expired') break
    }
  }
}

// One run per Iridium: a second would take the same TOKEN and the two would keep pushing each
// other off. The lock is the running one's pid.
fs.mkdirSync(state, { recursive: true, mode: 0o700 })
const lock = path.join(state, 'lock')
try {
  process.kill(Number(fs.readFileSync(lock, 'utf8')), 0)
  throw new Error(`already running for ${url} (pid ${fs.readFileSync(lock, 'utf8').trim()})`)
} catch (e) {
  if (e.code !== 'ESRCH' && e.code !== 'ENOENT' && e.code !== 'EINVAL') throw e
}
fs.writeFileSync(lock, String(process.pid))

let ex = null
const quit = (code) => (ex?.stop(), fs.rmSync(lock, { force: true }), setTimeout(() => process.exit(code), 200))
process.on('SIGTERM', () => quit(0))
process.on('SIGINT', () => quit(0))
let token = fs.existsSync(tokenFile) ? fs.readFileSync(tokenFile, 'utf8').trim() : ''
if (!token) {
  token = await pair()
  fs.writeFileSync(tokenFile, token, { mode: 0o600 })
  say('paired')
}
ex = new Executor({ url: url.replace(/^http/, 'ws') + '/v1/internal/exec', token, state, agents: [], agentShell: true, program, log: say })
// Idle: nothing started for a while, and nothing of its own still running.
let last = Date.now()
const handle = ex.handle.bind(ex)
ex.handle = (c, method, p) => (method === 'hold.start' && (last = Date.now()), handle(c, method, p))
void ex.run()
for (;;) {
  await sleep(60000)
  if (!ex.conn && (await refused(url, token))) {
    // Its Bot removed there: the TOKEN is no use any more; pair again by starting it again.
    fs.rmSync(tokenFile, { force: true })
    say('the Iridium no longer takes this machine: exiting')
    quit(1)
    break
  }
  if (ex.running() > 0) last = Date.now()
  if (idle > 0 && Date.now() - last >= idle) {
    say(`idle for ${idle / 60000} minutes: exiting`)
    quit(0)
    break
  }
}
