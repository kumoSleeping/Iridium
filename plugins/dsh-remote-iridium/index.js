// dsh-remote-iridium: a cordis plugin for dsh's web profile that makes this machine an executor
// of an Iridium (Iridium docs/exec.md). The first time it asks the Iridium to pair, showing a code
// to match on the page; once a panel allows it, it keeps the TOKEN and connects by itself. Each
// session the Iridium starts here is a `dsh --profile sdk` child of this process.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { Executor } from './executor.js'

export const name = 'remote-iridium'

const say = (m) => console.log(`dsh-remote-iridium: ${m}`)

/** The dsh this process runs: its node and bin.js, its package's @deepseek-ai, its version. */
export function findDsh() {
  const bin = fs.realpathSync(process.argv[1])
  for (let dir = path.dirname(bin); ; dir = path.dirname(dir)) {
    let pkg
    try {
      pkg = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
    } catch {}
    if (pkg?.name === '@deepseek-ai/dsh') {
      // Installed globally its packages are its own; in a project they may be hoisted beside it.
      const scope = [path.join(dir, 'node_modules', '@deepseek-ai'), path.dirname(dir)].find((s) =>
        fs.existsSync(path.join(s, 'dsh-web-app', 'presets')),
      )
      if (!scope) throw new Error(`no dsh-web-app presets in the dsh at ${dir}`)
      return { argv: [process.execPath, bin], scope, version: pkg.version }
    }
    if (path.dirname(dir) === dir) throw new Error(`${bin} is in no @deepseek-ai/dsh package`)
  }
}

/** The machine's default name: its host name, as a terminal name may be (at most 12). */
function machineName() {
  return os.hostname().replace(/\.local$/i, '').replace(/[^\p{L}\p{N}_-]+/gu, '-').slice(0, 12) || 'dsh'
}

const base = (url) => url.replace(/\/+$/, '')
const wsURL = (url) => base(url).replace(/^http/i, 'ws') + '/api/exec'

/** Asks the Iridium to pair until a panel allows it: the TOKEN; null if denied or stopped. */
async function pair(url, hello, stopped) {
  for (;;) {
    if (stopped()) return null
    let req
    try {
      const r = await fetch(`${base(url)}/api/exec/pair`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(hello) })
      if (!r.ok) throw new Error(`${r.status} ${await r.text()}`)
      req = await r.json()
    } catch (e) {
      say(`cannot reach ${url} to pair: ${e.message}; trying again in 30s`)
      await sleep(30000)
      continue
    }
    say(`pairing code ${req.code}: allow "${hello.name}" on the Iridium page`)
    for (;;) {
      if (stopped()) return null
      let st
      try {
        const r = await fetch(`${base(url)}/api/exec/pair/${encodeURIComponent(req.request)}`)
        st = r.ok ? await r.json() : { status: r.status === 404 ? 'expired' : 'pending' }
      } catch {
        st = { status: 'pending' }
      }
      if (st.status === 'allowed' && st.token) return st.token
      if (st.status === 'denied') {
        say('pairing was declined')
        return null
      }
      if (st.status === 'expired') break
      await sleep(2000)
    }
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** Whether the Iridium turns the TOKEN down (a removed Bot): then it pairs again. */
async function refused(url, token) {
  try {
    const r = await fetch(`${base(url)}/api/exec`, { headers: { Authorization: `Bearer ${token}` } })
    return r.status === 401 || r.status === 403
  } catch {
    return false
  }
}

/**
 * Config: url, the Iridium (https://…); name, this machine's (default: its host name); roots, the
 * directories the Iridium may read and write files under (default: all but the denied).
 */
export function apply(ctx, config) {
  const url = config?.url
  if (!url) {
    say('no url set: set url (your Iridium, e.g. https://ir.example.com) in remote-iridium\'s config, then restart dsh web')
    return
  }
  const home = process.env.DSH_HOME || path.join(os.homedir(), '.dsh')
  const state = path.join(home, 'remote-iridium')
  const tokenFile = path.join(state, 'token', 'token')
  let stopped = false
  let ex = null
  ctx.effect(() => {
    ;(async () => {
      let dsh
      try {
        dsh = { agent: 'dsh', ...findDsh(), home }
      } catch (e) {
        say(e.message)
        return
      }
      const who = { name: config.name || machineName(), os: process.platform === 'win32' ? 'windows' : process.platform, arch: process.arch, program: `dsh-remote-iridium ${version()}`, agents: [{ agent: 'dsh', version: dsh.version }] }
      for (;;) {
        let token = null
        try {
          token = fs.readFileSync(tokenFile, 'utf8').trim()
        } catch {}
        if (!token) {
          token = await pair(url, who, () => stopped)
          if (!token) return
          fs.mkdirSync(path.dirname(tokenFile), { recursive: true, mode: 0o700 })
          fs.writeFileSync(tokenFile, token, { mode: 0o600 })
          say(`paired with ${url}`)
        }
        if (stopped) return
        ex = new Executor({ url: wsURL(url), token, state, roots: config.roots ?? [], agents: [dsh], program: who.program, log: (m) => say(m) })
        const running = ex.run()
        // A TOKEN turned down (its Bot removed) is forgotten, and the machine pairs again.
        while (!stopped) {
          await sleep(60000)
          if (!ex.conn && (await refused(url, token))) {
            say('the Iridium no longer takes this machine: pairing again')
            ex.stop()
            fs.rmSync(tokenFile, { force: true })
            break
          }
        }
        await running
        if (stopped) return
      }
    })()
    return () => {
      stopped = true
      ex?.stop()
    }
  }, 'remote-iridium')
}

function version() {
  try {
    return JSON.parse(fs.readFileSync(new URL('./package.json', import.meta.url), 'utf8')).version
  } catch {
    return '0'
  }
}
