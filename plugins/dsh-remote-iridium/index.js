// dsh-remote-iridium: a cordis plugin for dsh's web profile that makes this machine an executor
// of an Iridium (Iridium docs/exec.md). The first time it asks the Iridium to pair, showing a code
// to match on the page; once a panel allows it, it keeps the TOKEN and connects by itself. Each
// session the Iridium starts here is a `dsh --profile sdk` child of this process.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import z from '@deepseek-ai/schemastery'
import { OmdAdapter } from './adapters/omd.js'
import { dshMethods } from './dsh.js'
import { Executor, iridiumKey, refused } from './executor.js'
import { createFeatures, unfeaturedList } from './features.js'
import { followHeld } from './held.js'
import { bridgeEndpoint, createBridge } from './bridge.js'

export const name = 'remote-iridium'

/** The profile entry cordis.patch.yml inserts: the settings namespace of the url. */
const ENTRY = 'remote-iridium'

const say = (m) => console.log(`dsh-remote-iridium: ${m}`)

/** The dsh this process runs: its node and bin.js, its package's @deepseek-ai, its version. */
export function findDsh() {
  const bin = fs.realpathSync(process.argv[1])
  const pkgAt = (dir) => {
    try {
      return JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
    } catch {}
  }
  const asDsh = (dir, pkg) => {
    // Installed globally its packages are its own; in a project they may be hoisted beside it.
    const scope = [path.join(dir, 'node_modules', '@deepseek-ai'), path.dirname(dir)].find((s) =>
      fs.existsSync(path.join(s, 'dsh-web-app', 'presets')),
    )
    if (!scope) throw new Error(`no dsh-web-app presets in the dsh at ${dir}`)
    const entry = typeof pkg.bin === 'string' ? pkg.bin : (pkg.bin?.dsh ?? 'lib/bin.js')
    return { argv: [process.execPath, path.join(dir, entry)], scope, version: pkg.version }
  }
  for (let dir = path.dirname(bin); ; dir = path.dirname(dir)) {
    const pkg = pkgAt(dir)
    if (pkg?.name === '@deepseek-ai/dsh') return asDsh(dir, pkg)
    // dsh's desktop app runs a profile in its own host (@deepseek-ai/dsh-desktop-host), and the
    // process this plugin runs in is that host: the CLI is then a dependency of the runtime beside
    // it, not a package above it, so this dir's node_modules is looked in too.
    const cli = path.join(dir, 'node_modules', '@deepseek-ai', 'dsh')
    const cliPkg = pkgAt(cli)
    if (cliPkg?.name === '@deepseek-ai/dsh') return asDsh(cli, cliPkg)
    if (path.dirname(dir) === dir) throw new Error(`${bin} is in no @deepseek-ai/dsh package`)
  }
}

/** The machine's default name: its host name, as a terminal name may be (at most 12). */
function machineName() {
  return os.hostname().replace(/\.local$/i, '').replace(/[^\p{L}\p{N}_-]+/gu, '-').slice(0, 12) || 'dsh'
}

const base = (url) => url.replace(/\/+$/, '')
const wsURL = (url) => base(url).replace(/^http/i, 'ws') + '/api/exec'

/** Asks the Iridium to pair until a panel allows it: the TOKEN; null if denied, closed or stopped. */
async function pair(url, hello, stopped, show) {
  for (let again = false; ; again = true) {
    if (stopped()) return null
    let req
    try {
      const r = await fetch(`${base(url)}/api/pair`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(hello) })
      if (r.status === 403) {
        show({ state: 'closed' })
        say(`${url} takes no pairing requests now (its settings): ask again with /iridium ${url}`)
        return null
      }
      if (r.status === 429) {
        const wait = Math.min(Math.max(Number(r.headers.get('retry-after')) || 600, 30), 3600)
        show({ state: 'busy', until: Date.now() + wait * 1000 })
        say(`${url} has too many pairing requests: trying again in ${Math.round(wait / 60)} min`)
        await sleep(wait * 1000)
        continue
      }
      if (!r.ok) throw new Error(`HTTP ${r.status} ${(await r.text()).trim()}`.trim())
      req = await r.json()
    } catch (e) {
      show({ state: 'unreachable', error: e.message })
      say(`cannot reach ${url} to pair: ${e.message}; trying again in 30s`)
      await sleep(30000)
      continue
    }
    show({ state: 'pairing', code: req.code, renewed: again })
    say(`pairing code ${req.code}: allow "${hello.name}" on the Iridium page`)
    for (;;) {
      if (stopped()) return null
      let st
      try {
        const r = await fetch(`${base(url)}/api/pair/${encodeURIComponent(req.request)}`)
        st = r.ok ? await r.json() : { status: r.status === 404 || r.status === 422 ? 'expired' : 'pending' }
      } catch {
        st = { status: 'pending' }
      }
      if (st.status === 'allowed' && st.token) return st.token
      if (st.status === 'denied') {
        show({ state: 'denied' })
        say('pairing was declined')
        return null
      }
      if (st.status === 'expired') {
        say('the pairing code expired: asking for a new one')
        break
      }
      await sleep(2000)
    }
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** The one TOKEN of 0.1 (token/token, its Iridium in token/url), filed under that Iridium. */
function migrate(tokens, tokenOf) {
  const old = path.join(tokens, 'token')
  try {
    const url = fs.readFileSync(path.join(tokens, 'url'), 'utf8').trim()
    if (fs.statSync(old).isFile() && !fs.existsSync(tokenOf(url))) fs.renameSync(old, tokenOf(url))
  } catch {}
  for (const f of ['token', 'url']) {
    try {
      if (fs.statSync(path.join(tokens, f)).isFile()) fs.rmSync(path.join(tokens, f))
    } catch {}
  }
}

/** A volatile config field's value now. */
const volatile = (v) => (typeof v?.get === 'function' ? v.get() : v)

/** An Iridium address as typed: http(s), no path; null if it is not one. */
export function iridiumURL(s) {
  let u
  try {
    u = new URL(s.trim())
  } catch {
    return null
  }
  if (u.protocol !== 'https:' && u.protocol !== 'http:') return null
  return u.origin
}

/**
 * Whether url may be used: https, or http to this machine or a private network (loopback, 10/8,
 * 172.16/12, 192.168/16, 100.64/10 as Tailscale's, fc00::/7, fe80::/10, localhost, *.local).
 * Over http elsewhere the TOKEN and everything sent would cross the internet in the clear.
 */
export function secureURL(url) {
  const u = new URL(url)
  if (u.protocol === 'https:') return true
  const h = u.hostname.toLowerCase().replace(/^\[|\]$/g, '')
  if (h === 'localhost' || h.endsWith('.localhost') || h.endsWith('.local')) return true
  const v4 = h.match(/^(\d+)\.(\d+)\.\d+\.\d+$/)
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])]
    return a === 127 || a === 10 || (a === 172 && b >= 16 && b < 32) || (a === 192 && b === 168) || (a === 100 && b >= 64 && b < 128)
  }
  return h === '::1' || /^f[cd][0-9a-f]{0,2}:/.test(h) || /^fe[89ab][0-9a-f]?:/.test(h)
}

/**
 * Config: url, the Iridium (https://…), set on its page in dsh's settings (client.js), with
 * `/iridium <url>`, or in the profile; name, this machine's (default: its host name); roots, the
 * directories the Iridium may read and write files under (default: all but the denied); shell,
 * whether command lines from the Iridium run here, not only dsh (default: yes); agentShell, whether
 * its agents may run them too (ir sh), not only a person on its page (default: no); unsynced, the
 * folders of the workspaces the Iridium does not list (switched off on the plugin's settings page;
 * a name in dsh's sidebar does too); ungrouped, whether it lists the sessions in none of the
 * workspaces (dsh's 未分组; default: yes); unfeatured, the features (features.js: other plugins'
 * switches on a session, OMD's Ultra) not offered to the Iridium, by id (default: none). The url,
 * unsynced, ungrouped and unfeatured are volatile: taken at once, without dsh starting the plugin
 * again.
 */
export const Config = z.object({
  url: z.string().default('').volatile(),
  name: z.string(),
  roots: z.array(z.string()),
  shell: z.boolean().default(true),
  agentShell: z.boolean().default(false),
  // A list; text with commas from an earlier build is taken too (unsyncedList).
  unsynced: z.union([z.array(z.string()), z.string()]).default([]).volatile(),
  ungrouped: z.boolean().default(true).volatile(),
  unfeatured: z.union([z.array(z.string()), z.string()]).default([]).volatile(),
})

/** The workspaces kept from the Iridium, as the setting lists them (folders or names; a list, or
 *  text with commas as an earlier build kept it). */
export function unsyncedList(v) {
  const all = Array.isArray(v) ? v : String(v ?? '').split(/[,，;；\n]/)
  return [...new Set(all.map((x) => String(x).trim()).filter(Boolean))]
}

export function apply(ctx, config) {
  const home = process.env.DSH_HOME || path.join(os.homedir(), '.dsh')
  // Each Iridium its own TOKEN (token/<host>) and holders (<host>/): one set aside for another is
  // there again when it comes back, unpaired only if that Iridium removed it.
  const state = path.join(home, 'remote-iridium')
  const tokens = path.join(state, 'token')
  const tokenOf = (url) => path.join(tokens, iridiumKey(url))
  migrate(tokens, tokenOf)
  // The address as set, or the http(s) one in it (pasted with something before or after it).
  const urlNow = () => {
    const s = (volatile(config?.url) || '').trim()
    return iridiumURL(s.match(/https?:\/\/\S+/i)?.[0] ?? s) ?? s
  }
  let status = { state: 'idle' }
  let run = null // { stop, url }: the loop for one url
  let offer = null // what this machine gives the Iridium: dsh's version, commands, roots

  // Other plugins' switches on a session, for the Iridium's page (features.js), as the service
  // iridiumFeatures; OMD's Ultra one of them, by a sub-plugin on while OMD is here. The page
  // learns of a change by held.changed, over whichever connection is up.
  const features = createFeatures({ unfeatured: () => unfeaturedList(volatile(config?.unfeatured)) })
  ctx.provide('iridiumFeatures', features)
  ctx.plugin(OmdAdapter)
  // The dsh processes the Iridium starts here run their sessions' features themselves (session.js,
  // which dsh.session's patch loads there): they tell them over this bridge.
  const bridge = createBridge({ endpoint: bridgeEndpoint(state), changed: (s) => features.changed(s), log: say })
  const sessionFile = path.join(state, 'session.patch.yml')
  const follow = followHeld(ctx, features, (event, params) => run?.ex?.notify(event, params), { bridge })
  ctx.effect(() => () => {
    follow.stop()
    features.dispose()
    bridge.close()
  }, 'remote-iridium: features')
  // A dsh with agents (DSH Desktop's, dsh web's) holds sessions; one without has nothing to say.
  const caps = () => (ctx.get('agents') ? ['held', 'held.features'] : [])

  function start() {
    run?.stop()
    run = null
    const url = urlNow()
    if (!url) {
      status = { state: 'no-url' }
      say('no url set: set the Iridium address on the Iridium page of dsh\'s settings, or type /iridium https://your-iridium')
      return
    }
    if (!iridiumURL(url)) {
      status = { state: 'bad-url', url }
      say(`not an Iridium address: ${url}`)
      return
    }
    if (!secureURL(url)) {
      status = { state: 'insecure-url', url }
      say(`${url} is plain http to a public host: use https (http only to this machine or a private network)`)
      return
    }
    const tokenFile = tokenOf(url)
    let stopped = false
    let ex = null
    status = { state: 'idle', url }
    const show = (st) => {
      if (!stopped) status = { url, ...st }
    }
    run = {
      url,
      get ex() {
        return ex
      },
      stop() {
        stopped = true
        ex?.stop()
      },
    }
    ;(async () => {
      let dsh
      try {
        dsh = { agent: 'dsh', ...findDsh(), home }
      } catch (e) {
        show({ state: 'error', error: e.message })
        say(e.message)
        return
      }
      // What it asks with is what its hello will say: the Iridium's card shows the agents and
      // whether command lines would run here.
      const noShell = config?.shell === false
      const agentShell = !noShell && config?.agentShell === true
      const who = {
        name: config?.name || machineName(), os: process.platform === 'win32' ? 'windows' : process.platform, arch: process.arch,
        program: `dsh-remote-iridium ${version()}`, agents: [{ agent: 'dsh', version: dsh.version }],
        ...(noShell ? { noShell } : {}), ...(agentShell ? { agentShell } : {}),
      }
      offer = { dsh: dsh.version, noShell, agentShell, roots: config?.roots ?? [] }
      if (older(dsh.version, MIN_DSH)) say(`dsh ${dsh.version} is older than ${MIN_DSH}: the Iridium starts no dsh here`)
      for (;;) {
        let token = null
        try {
          token = fs.readFileSync(tokenFile, 'utf8').trim()
        } catch {}
        if (!token) {
          token = await pair(url, who, () => stopped, show)
          if (!token) return
          fs.mkdirSync(path.dirname(tokenFile), { recursive: true, mode: 0o700 })
          fs.writeFileSync(tokenFile, token, { mode: 0o600 })
          say(`paired with ${url}`)
        }
        if (stopped) return
        show({ state: 'paired', name: who.name })
        // The models this dsh's settings give, for the sessions it starts (dsh.llm): keys may be in it.
        const llmFile = path.join(state, 'llm.patch.yml')
        ex = new Executor({ url: wsURL(url), token, state: path.join(state, iridiumKey(url)), denied: [tokens, llmFile, sessionFile], roots: config?.roots ?? [], noShell, agentShell, agents: [dsh], caps, methods: dshMethods(ctx, () => ({ unsynced: unsyncedList(volatile(config?.unsynced)), ungrouped: volatile(config?.ungrouped) !== false }), { file: llmFile, scope: dsh.scope }, { features, follow, bridge, sessionFile }), program: who.program, ir: fileURLToPath(new URL('./ir.js', import.meta.url)), log: (m) => say(m) })
        const running = ex.run()
        // A TOKEN turned down (its Bot removed) is forgotten, and the machine pairs again.
        while (!stopped) {
          await sleep(60000)
          if (!ex.conn && (await refused(url, token))) {
            say('the Iridium no longer takes this machine: pairing again')
            show({ state: 'revoked' })
            ex.stop()
            fs.rmSync(tokenFile, { force: true })
            break
          }
        }
        await running
        if (stopped) return
      }
    })()
  }

  /** What this machine gives the Iridium, a line each. */
  function offered() {
    if (!offer) return []
    const dsh = `dsh ${offer.dsh}${older(offer.dsh, MIN_DSH) ? `（低于 ${MIN_DSH}，Iridium 不会在本机开 dsh）` : ''}`
    const commands = offer.noShell ? '不执行，仅启动 dsh' : offer.agentShell ? '执行网页上的命令和 agent 的 ir sh' : '执行网页上的命令（agent 的 ir sh 不执行）'
    const roots = offer.roots.length ? offer.roots.join('、') : '全部（TOKEN、dsh 的密钥和配置、各 agent 的登录、~/.ssh 等除外）'
    return [`agent：${dsh}`, `命令：${commands}`, `文件：${roots}`]
  }

  function describe() {
    const at = status.url ? `（${status.url}）` : ''
    switch (status.state) {
      case 'no-url':
        return '未设置 Iridium 地址。在设置的 Iridium 一页填写，或输入 /iridium https://你的-iridium'
      case 'bad-url':
        return `不是 Iridium 地址：${status.url}。用法：/iridium https://你的-iridium`
      case 'insecure-url':
        return `不安全的地址：${status.url}。公网地址须用 https，http 仅限本机和内网`
      case 'unreachable':
        return `无法连接 Iridium${at}：${status.error}。30 秒后重试`
      case 'closed':
        return `Iridium 已停止接收连接申请${at}。对方开放后重新申请：/iridium ${status.url}`
      case 'busy':
        return `Iridium 的连接申请过多${at}，${ago(status.until - Date.now()) || '1 分钟'}内重试`
      case 'pairing':
        return [`等待批准${at}，配对码 ${status.code}${status.renewed ? '（原配对码已过期，此为新码）' : ''}`, '在 Iridium 网页上核对配对码后允许。申请内容：', ...offered()].join('\n')
      case 'denied':
        return `配对被拒绝${at}。重新申请：/iridium ${status.url}`
      case 'revoked':
        return `已被 Iridium 移除${at}，重新申请中`
      case 'paired':
        return [`已接入${at}，名为 ${run?.ex?.name || status.name}`, connection(), ...offered(), running()].filter(Boolean).join('\n')
      case 'error':
        return `无法启动：${status.error}`
      default:
        return `连接中${at}`
    }
  }

  /** The connection: up since when, or down since when and why. */
  function connection() {
    const ex = run?.ex
    if (!ex) return '连接：连接中'
    const span = ex.since ? ago(Date.now() - ex.since) : ''
    if (ex.conn) return span ? `连接：已连接 ${span}` : '连接：已连接'
    const why = ex.error ? `（${ex.error}）` : ''
    if (!ex.since) return `连接：连接中${why}`
    return `连接：已断开${span ? ` ${span}` : ''}，重连中${why}`
  }

  /** The sessions' processes it runs now. */
  function running() {
    const n = run?.ex?.running() ?? 0
    return n ? `进程：${n} 个运行中` : '进程：无'
  }

  ctx.effect(() => {
    start()
    return () => run?.stop()
  }, 'remote-iridium')
  // The address changed on its settings page: ask the new one. A feature switched off or on
  // there: the sessions the Iridium watches are looked at again.
  ctx.on('loader/volatile-update', () => {
    if (urlNow() !== run?.url) start()
    features.changed(null)
  })

  ctx.inject(['commands', 'settings'], (ctx) => {
    ctx.commands.register({
      name: 'iridium',
      description: 'Connect this machine to an Iridium, or show its pairing state',
      input: { hint: '[https://your-iridium]' },
      handler: async ({ rawInput }) => {
        const arg = (rawInput ?? '').trim()
        if (!arg) return { kind: 'success', text: describe() }
        const url = iridiumURL(arg)
        if (!url) return { kind: 'error', text: `不是 Iridium 地址：${arg}` }
        if (!secureURL(url)) return { kind: 'error', text: `不安全的地址：${url}。公网地址须用 https，http 仅限本机和内网` }
        if (url === urlNow()) {
          start() // asked again, after a refusal or to see a new code
        } else {
          await ctx.settings.update(ENTRY, { url })
          if (urlNow() !== run?.url) start()
        }
        return { kind: 'success', text: `已向 ${url} 申请连接。/iridium 查看配对码` }
      },
    })
  })
}

/** The oldest dsh the Iridium starts here (its MinRemote; dsh 0.1 has no sdk profile). */
const MIN_DSH = '0.2.2'

/** Whether version a is older than b, as the Iridium compares them (agents.RemoteVersionOK): the
 *  first three numbers, "0.2.2-rc.1" as 0.2.2. */
export function older(a, b) {
  const parse = (v) => String(v).split('-', 1)[0].split('.').map((x) => Number.parseInt(x, 10) || 0)
  const x = parse(a)
  const y = parse(b)
  for (let i = 0; i < 3; i++) if ((x[i] ?? 0) !== (y[i] ?? 0)) return (x[i] ?? 0) < (y[i] ?? 0)
  return false
}

/** A rough span: 5 分钟, 3 小时, 2 天; '' under a minute. */
function ago(ms) {
  const m = Math.floor(Math.max(ms, 0) / 60000)
  if (m < 1) return ''
  if (m < 60) return `${m} 分钟`
  if (m < 48 * 60) return `${Math.floor(m / 60)} 小时`
  return `${Math.floor(m / 1440)} 天`
}

function version() {
  try {
    return JSON.parse(fs.readFileSync(new URL('./package.json', import.meta.url), 'utf8')).version
  } catch {
    return '0'
  }
}
