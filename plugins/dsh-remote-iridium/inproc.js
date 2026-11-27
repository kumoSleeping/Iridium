// Iridium's dsh sessions run in the dsh this plugin is in (DSH Desktop's own process, or a dsh web
// profile), not in a `dsh --profile sdk` child (Iridium's docs/exec.md "DSH Desktop"): a hold the
// Iridium starts with argv[0] INPROC is served here by dsh's own SDK JSON-RPC server
// (@deepseek-ai/dsh-sdk-jsonrpc-server), over streams instead of a child's stdio, so the Iridium's
// driver speaks the protocol it speaks to a child. Its sessions are Desktop's: taken through
// Desktop's session controller, one agent in the process for a session whichever side opened it,
// with its own preset, models, plugins and persona; no writer lock between the two. An approval or
// a question goes both to Desktop's window and to the Iridium; the first answer wins and the
// other's card goes away.
import fs from 'node:fs'
import path from 'node:path'
import { Writable } from 'node:stream'
import { pathToFileURL } from 'node:url'

/** argv[0] of a hold run in this process. */
export const INPROC = '@dsh-desktop'

/** The system prompt's section Iridium's prompt is (after the deployment's persona suffix). */
const SECTION = 'iridium:system'
const SECTION_ORDER = 10250

/** The DSH_* variables a shell call of an Iridium session gets (ir.js reads them). */
const ENV = {
  DSH_IRIDIUM_URL: 'The Iridium this session is hosted by.',
  DSH_IRIDIUM_TOKEN: "The session's own TOKEN at that Iridium.",
  DSH_IRIDIUM_SESSION: "The session's id at that Iridium.",
}

/** dsh's SDK server and its line transport, from the dsh this process runs (scope: its @deepseek-ai). */
export async function loadSdk(scope) {
  const at = (pkg) => pathToFileURL(path.join(scope, pkg, 'lib', 'index.js')).href
  const [server, protocol] = await Promise.all([import(at('dsh-sdk-jsonrpc-server')), import(at('dsh-sdk-protocol'))])
  if (typeof server.HarnessSdkJsonRpcServer !== 'function' || typeof protocol.JsonRpcLineTransport !== 'function')
    throw new Error('this dsh has no SDK JSON-RPC server')
  return { Server: desktopServer(server.HarnessSdkJsonRpcServer), Transport: protocol.JsonRpcLineTransport }
}

/** What a hold's argv after INPROC says: --preset <id> (a new session's), --system <file>,
 *  --persona own (its persona is the Iridium's), --keep-model (resumed with the model it has),
 *  --attach <id> (the start key a new session is made with: a button id of desktop.config's keys). */
export function inprocArgs(argv) {
  const o = {}
  for (let i = 1; i < argv.length; i++) {
    const k = argv[i]
    const v = argv[i + 1]
    if (k === '--preset' && v !== undefined) (o.preset = v), i++
    else if (k === '--system' && v !== undefined) (o.system = v), i++
    else if (k === '--persona' && v !== undefined) (o.persona = v), i++
    else if (k === '--attach' && v !== undefined) (o.attach = v), i++
    else if (k === '--keep-model') o.keepModel = true
  }
  return o
}

/** A notification of session id: whose (the field naming it). */
const sessionOf = (method, p) =>
  method === 'subagent.started' || method === 'subagent.finished' ? p?.parentSessionId : p?.sessionId

/**
 * dsh's SDK server as one hold here serves it. Its sessions are Desktop's (createSession); only
 * theirs reach its transport (dsh's server, made for a process of one session, sends every
 * session's events); scheduled restores stay Desktop's; approvals and questions are raced with
 * Desktop's window (createInproc). Stopped, it cancels a turn its sessions run but leaves their
 * agents to Desktop.
 */
export function desktopServer(Base) {
  return class DesktopSdkServer extends Base {
    constructor(ctx, transport, o) {
      const own = new Set() // sessions it opened or opens
      const kids = new Set() // their subagents' sessions
      const filtered = {
        request: (...a) => transport.request(...a),
        notify(method, p) {
          if (method === 'subagent.started' && own.has(p?.parentSessionId)) kids.add(p.childSessionId)
          const id = sessionOf(method, p)
          if (id === undefined || own.has(id) || kids.has(id)) transport.notify(method, p)
        },
      }
      super(ctx, filtered, {})
      this.own = own
      this.o = o
      // Its first two listeners: approvals for its own agents (raced instead), and the restore of
      // a scheduled session, which Desktop's session controller does here.
      if (this.disposers.length < 2) throw new Error('this dsh\'s SDK server is not the one this plugin knows')
      for (const off of this.disposers.splice(0, 2)) off()
      this.userQuestions.unregister()
      this.userQuestions.unregister = () => {}
    }

    openSession(sessionId, resume) {
      this.own.add(sessionId)
      return super.openSession(sessionId, resume)
    }

    /** The session as Desktop has it: its live agent, resumed or made by its session controller. */
    async createSession(sessionId, resume) {
      const ctx = this.ctx
      const sc = ctx.get('sessionController')
      if (!sc) throw new Error('no session controller in this dsh')
      let agent
      if (resume) {
        const r = await sc.resolveAgent(sessionId)
        if (r?.error) throw r.error
        agent = r.agent
      } else {
        if (this.o.refuse?.()) throw new Error('this dsh hands its sessions over to DSH Desktop: no new session here')
        const live = ctx.agents.get(sessionId)
        if (live || (await ctx.get('sessionPersistence')?.stat(sessionId)))
          throw new Error(`session "${sessionId}" already exists`)
        const ws = await workspaceOf(ctx, this.cwd)
        const made = await sc.create({
          sessionId,
          ...(ws ? { workspaceId: ws.id } : { cwd: this.cwd }),
          ...(this.o.preset ? { agentPreset: this.o.preset } : {}),
        })
        if (made?.sessionId !== sessionId) throw new Error(`Desktop made session "${made?.sessionId}", not "${sessionId}"`)
        agent = ctx.agents.get(sessionId)
        if (!agent) throw new Error(`no agent of the new session "${sessionId}"`)
      }
      if (!(resume && this.o.keepModel)) this.route(agent)
      // A new session's start key, on before its first turn; one that fails leaves it off.
      if (!resume && this.o.startKey) {
        try {
          await this.o.startKey(agent)
        } catch (e) {
          this.o.log?.(`start key: ${e?.message ?? e}`)
        }
      }
      const detach = this.o.attach?.(agent) ?? (() => {})
      const rec = {
        handle: {
          agent,
          // Its agent stays Desktop's: a turn running is cancelled, Iridium's parts taken off.
          dispose: async () => {
            detach()
            if (agent.status === 'running') agent.cancel({ kind: 'user' }, { keepInbox: true })
          },
        },
        failure: undefined,
      }
      this.sessions.set(sessionId, rec)
      return rec
    }

    /** The route it was initialized with, on agent, as dsh's SDK server resumes with it (the
     *  level kept from the session's own selection if not given and the route is the same). */
    route(agent) {
      const cur = agent.model?.current?.()
      const sel = { provider: this.provider, model: this.model }
      if (this.reasoningEffort !== undefined) sel.reasoningEffort = this.reasoningEffort
      else if (cur?.provider === sel.provider && cur?.model === sel.model && cur.reasoningEffort !== undefined) sel.reasoningEffort = cur.reasoningEffort
      if (this.maxTokens !== undefined) sel.maxTokens = this.maxTokens
      if (cur?.provider === sel.provider && cur?.model === sel.model && cur?.reasoningEffort === sel.reasoningEffort && sel.maxTokens === undefined) return
      agent.model.select(sel)
    }

    async performShutdown() {
      const recs = [...this.sessions.values()]
      this.sessions.clear() // not drained nor disposed by dsh's teardown: Desktop's agents
      try {
        return await super.performShutdown()
      } finally {
        await Promise.allSettled(recs.map((r) => r.handle.dispose()))
      }
    }
  }
}

/** The workspace Desktop files a session at cwd under: the one at that folder, else a new one. */
async function workspaceOf(ctx, cwd) {
  const reg = ctx.get('workspaceRegistry')
  if (!reg) return null
  return (await reg.resolveByPath(cwd)) ?? (await reg.create(cwd))
}

/** Whether a waterfall's own end answered (no answerer of Desktop's took it). */
const noAnswer = (v) => v === 'unavailable'
const noProvider = (e) => e?.code === 'NO_PROVIDER'

/**
 * Races ask (Iridium's answer, given its signal) with next (Desktop's answerers: its window) for
 * req, whose signal is replaced so that Desktop's card goes when the Iridium answers first; the
 * Iridium's ask is cancelled when Desktop does. An answer of nobody (no window open) waits for the
 * other side.
 */
export function race(req, next, ask) {
  const desk = new AbortController()
  const ir = new AbortController()
  const orig = req.signal
  req.signal = orig ? AbortSignal.any([orig, desk.signal]) : desk.signal
  const irSignal = orig ? AbortSignal.any([orig, ir.signal]) : ir.signal
  return new Promise((resolve, reject) => {
    let done = false
    let left = 2
    let last = { ok: true, v: 'unavailable' }
    const end = (r) => {
      if (done) return
      if (r) {
        done = true
        return r.ok ? resolve(r.v) : reject(r.v)
      }
      if (--left === 0) {
        done = true
        last.ok ? resolve(last.v) : reject(last.v)
      }
    }
    Promise.resolve()
      .then(next)
      .then(
        (v) => {
          if (done) return
          if (noAnswer(v)) return (last = { ok: true, v }), end()
          ir.abort(new Error('answered in DSH Desktop'))
          end({ ok: true, v })
        },
        (e) => {
          if (done) return
          if (desk.signal.aborted || noProvider(e)) return (last = { ok: false, v: e }), end()
          ir.abort(e)
          end({ ok: false, v: e })
        },
      )
    Promise.resolve()
      .then(() => ask(irSignal))
      .then(
        (v) => {
          if (done) return
          desk.abort(new Error('answered on the Iridium'))
          end({ ok: true, v })
        },
        (e) => {
          if (done) return
          if (orig?.aborted) {
            desk.abort(e)
            return end({ ok: false, v: e })
          }
          end() // its server stopping, the connection gone: Desktop's window alone
        },
      )
  })
}

/**
 * The holds run in this process. ctx: the plugin's; scope: the dsh's @deepseek-ai; log; persona:
 * persona.js's ownPersona (a session whose persona is the Iridium's), if any; ready(), once holds
 * can run here; startKey(agent, id), the start key id pressed for a new session (--attach). start()
 * serves one: {hold, argv, env, input (its stdin), out (fd of its out.log), err (fd), exited(x)};
 * returns {stop(hard)}.
 */
export function createInproc(ctx, { scope, log = () => {}, persona = null, ready = () => {}, startKey = null } = {}) {
  const servers = new Set()
  let refused = false // handed over to Desktop (desktop.freeze): no session made here
  // dsh's SDK server reads its services as properties: a context that has them injected, while
  // they are there (Desktop's session controller comes up after this plugin).
  let here = null
  ctx.inject(['agents', 'workingDirectory', 'sessionController'], (c) => {
    here = c
    setImmediate(ready)
    c.effect(() => () => {
      if (here === c) here = null
    }, 'remote-iridium: inproc services')
  })
  const ids = new Map() // dsh session id → {url, token, session}: for its shell calls
  let sdk = null
  const owner = (agent, exact) => {
    if (!agent) return null
    for (const s of servers) if (exact ? s.sessions.get(agent.session?.id)?.handle.agent === agent : s.ownsAgent(agent)) return s
    return null
  }
  ctx.on(
    'approval/request',
    function (req, next) {
      const s = owner(req.agent, false)
      if (!s || !s.approvalRequests) return next()
      const params = {
        sessionId: String(req.agent.session.id),
        toolName: req.toolName,
        ...(req.callId === undefined ? {} : { callId: req.callId }),
        ...(req.reason === undefined ? {} : { reason: req.reason }),
      }
      return race(req, next, async (signal) => {
        const r = await s.transport.request('approval/request', params, AbortSignal.any([signal, s.lifetime.signal]))
        if (r?.outcome !== 'allowed-once' && r?.outcome !== 'rejected' && r?.outcome !== 'cancelled')
          throw new TypeError('approval/request response requires an allowed-once, rejected, or cancelled outcome')
        return r.outcome
      })
    },
    true,
  )
  ctx.on(
    'user-questions/request',
    function (req, next) {
      const s = owner(req.agent, true)
      if (!s || !s.userQuestions.enabled) return next()
      return race(req, next, (signal) => s.userQuestions.ask({ ...req, signal }, String(req.agent.session.id)))
    },
    true,
  )
  ctx.inject(['shellEnv'], (c) => {
    c.shellEnv.register({
      name: 'iridium',
      variables: Object.fromEntries(Object.entries(ENV).map(([k, description]) => [k, { description }])),
      resolve(execution) {
        let agent = execution.agent
        let id = agent && ids.get(String(agent.session.id))
        if (!id && agent) {
          try {
            agent = ctx.get('subagents')?.rootOf?.(agent)
          } catch {
            agent = undefined
          }
          id = agent && ids.get(String(agent.session.id))
        }
        if (!id) return {}
        return { DSH_IRIDIUM_URL: id.url, DSH_IRIDIUM_TOKEN: id.token, DSH_IRIDIUM_SESSION: id.session }
      },
    })
  })

  return {
    /** Whether holds run here: this dsh has a session controller (Desktop's, dsh web's). */
    available: () => here !== null,

    /** No session is made here from now on (handed over to Desktop). */
    refuse: () => {
      refused = true
    },

    /** How many holds it serves. */
    serving: () => servers.size,

    async start({ hold, argv, env = {}, input, out, err, exited }) {
      const o = inprocArgs(argv)
      let system = ''
      if (o.system) system = fs.readFileSync(o.system, 'utf8')
      sdk ??= loadSdk(scope)
      const { Server, Transport } = await sdk
      const output = new Writable({
        write(chunk, _enc, cb) {
          try {
            fs.writeSync(out, chunk)
            cb()
          } catch (e) {
            cb(e)
          }
        },
      })
      const transport = new Transport(input, output)
      const session = env.IRIDIUM_SESSION
      const attach = (agent) => {
        const offs = []
        if (env.IRIDIUM_TOKEN) {
          ids.set(String(agent.session.id), { url: env.IRIDIUM_URL ?? '', token: env.IRIDIUM_TOKEN, session: session ?? '' })
          offs.push(() => ids.delete(String(agent.session.id)))
        }
        if (o.persona === 'own' && persona) offs.push(persona.own(String(agent.session.id)))
        const sp = agent.ctx?.get?.('systemPrompt')
        if (system.trim() && sp) {
          try {
            offs.push(sp.section({ name: SECTION, order: SECTION_ORDER, text: system, interpolate: false }))
          } catch (e) {
            log(`${hold}: Iridium's system prompt: ${e.message}`)
          }
        }
        return () => offs.forEach((f) => typeof f === 'function' && f())
      }
      if (!here) throw new Error('this dsh has no session controller (not DSH Desktop nor dsh web)')
      const server = new Server(here, transport, {
        preset: o.preset, keepModel: o.keepModel, attach, refuse: () => refused,
        ...(o.attach && startKey ? { startKey: (agent) => startKey(agent, o.attach), log: (m) => log(`${hold}: ${m}`) } : {}),
      })
      servers.add(server)
      let ended = false
      const end = async (x) => {
        if (ended) return
        ended = true
        try {
          await server.shutdown()
        } catch (e) {
          try {
            fs.writeSync(err, `shutdown: ${e?.message ?? e}\n`)
          } catch {}
        }
        try {
          await transport.flush()
        } catch {}
        transport.close()
        servers.delete(server)
        exited(x)
      }
      transport.onRequest(async (method, params) => {
        if (method === 'initialize') await here?.get('loader')?.await?.()
        const r = await server.handleRequest(method, params)
        if (method === 'shutdown') setImmediate(() => end({ from: 'agent', code: 0, signal: null }))
        return r
      })
      input.on('end', () => end({ from: 'agent', code: 0, signal: null }))
      transport.start()
      return {
        stop: (hard) => (hard ? end({ from: 'agent', code: null, signal: 'KILL' }) : input.end()),
      }
    },
  }
}
