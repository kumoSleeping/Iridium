// A developer's test Bot, not shipped (docs/exec.md "开发测试用的 Bot"): pairs with an Iridium once,
// then lets its agents run command lines on this machine (ir sh). No agents of its own; anyone can
// write one like it. ir join <url> --agent-shell --no-agents does the same without Node.
// node scripts/dev/shell-bot.mjs <iridium url> [name]
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { Executor } from '../../plugins/dsh-remote-iridium/executor.js'

const url = (process.argv[2] || '').replace(/\/+$/, '')
const name = process.argv[3] || 'mac-shell'
if (!/^https?:\/\//.test(url)) throw new Error('usage: node scripts/dev/shell-bot.mjs https://your-iridium [name]')
const state = path.join(os.homedir(), '.iridium-shell-bot')
const tokenFile = path.join(state, 'token')
const program = 'iridium-shell-bot 0.1'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const say = (m) => console.log(`${new Date().toISOString()} ${m}`)

async function pair() {
  const hello = { name, os: process.platform === 'win32' ? 'windows' : process.platform, arch: process.arch, program, agents: [] }
  for (;;) {
    const r = await fetch(`${url}/api/exec/pair`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(hello) })
    if (!r.ok) throw new Error(`pair: ${r.status} ${await r.text()}`)
    const req = await r.json()
    say(`pairing code ${req.code}: allow "${name}" on the Iridium page`)
    for (;;) {
      await sleep(2000)
      const s = await fetch(`${url}/api/exec/pair/${req.request}`)
      const st = s.ok ? await s.json() : { status: s.status === 404 ? 'expired' : 'pending' }
      if (st.status === 'allowed' && st.token) return st.token
      if (st.status === 'denied') throw new Error('declined')
      if (st.status === 'expired') break
    }
  }
}

let token = fs.existsSync(tokenFile) ? fs.readFileSync(tokenFile, 'utf8').trim() : ''
if (!token) {
  token = await pair()
  fs.mkdirSync(state, { recursive: true, mode: 0o700 })
  fs.writeFileSync(tokenFile, token, { mode: 0o600 })
  say('paired')
}
const ex = new Executor({ url: url.replace(/^http/, 'ws') + '/api/exec', token, state, agents: [], agentShell: true, program, log: say })
process.on('SIGTERM', () => (ex.stop(), setTimeout(() => process.exit(0), 200)))
await ex.run()
