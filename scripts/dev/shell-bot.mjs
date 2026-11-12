// A developer's test Bot, not shipped (docs/exec.md "开发测试用的 Bot"): pairs with an Iridium once,
// then lets its agents run command lines on this machine (ir sh). No agents of its own; anyone can
// write one like it. ir join <url> --shell, its agents turned off in 应用管理, does the same without Node.
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
const program = 'iridium-shell-bot 0.1'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const say = (m) => console.log(`${new Date().toISOString()} ${m}`)

async function pair() {
  const hello = { name, os: process.platform === 'win32' ? 'windows' : process.platform, arch: process.arch, program, agents: [], agentShell: true }
  for (;;) {
    const r = await fetch(`${url}/v1/internal/pair`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(hello) })
    if (r.status === 403) {
      say('pairing is closed there (开放配对 in 应用管理): waiting')
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
process.on('SIGTERM', () => (ex?.stop(), setTimeout(() => process.exit(0), 200)))
for (;;) {
  let token = fs.existsSync(tokenFile) ? fs.readFileSync(tokenFile, 'utf8').trim() : ''
  if (!token) {
    token = await pair()
    fs.writeFileSync(tokenFile, token, { mode: 0o600 })
    say('paired')
  }
  ex = new Executor({ url: url.replace(/^http/, 'ws') + '/v1/internal/exec', token, state, agents: [], agentShell: true, program, log: say })
  const running = ex.run()
  // Its Bot removed on that Iridium: forget the TOKEN and pair again.
  for (;;) {
    await sleep(60000)
    if (!ex.conn && (await refused(url, token))) break
  }
  say('the Iridium no longer takes this machine: pairing again')
  ex.stop()
  await running
  fs.rmSync(tokenFile, { force: true })
}
