// The part of this plugin in a dsh process the Iridium starts here (dsh --profile sdk, a session),
// which the patch agent.prepare writes loads: the service `iridium` (attach.js) for the plugins
// in that process (OMD's), told to the plugin in DSH Desktop over its bridge (bridge.js), which
// sends it on to the Iridium; a button pressed there comes back the same way. It also tells
// Desktop of each session that begins here, which files it under its folder's workspace. Config:
// endpoint and secret, the bridge's.
import net from 'node:net'
import { lines } from './bridge.js'
import { contentOf } from './attach.js'
import { RemoteError } from './executor.js'

export const name = 'remote-iridium-session'
export const inject = ['agents']

/** The least and most wait before connecting again. */
const RETRY = [500, 10_000]

/**
 * A session controller for a process without dsh's (the web surface's): its model selection only,
 * as Desktop's picker makes one (a plugin there switches a session's model through it, as OMD's
 * Ultra does), not saved as the default model.
 */
export function sessionController(ctx) {
  const resolveAgent = async (sessionId) => {
    const agent = ctx.get('agents')?.get(sessionId)
    return agent ? { agent } : { error: new RemoteError('not_found', `session ${sessionId} is not loaded here`) }
  }
  return {
    resolveAgent,
    async selectModel({ sessionId, provider, model, reasoningEffort }) {
      const r = await resolveAgent(sessionId)
      if (r.error) throw r.error
      const selected = { provider, model, ...(reasoningEffort === undefined ? {} : { reasoningEffort }) }
      await ctx.get('llm')?.resolveCallConfig?.(selected)
      r.agent.model.select(selected)
      return { selected: { ...selected } }
    },
  }
}

export function apply(ctx, config) {
  if (!ctx.get('sessionController')) ctx.provide('sessionController', sessionController(ctx))
  const sessions = new Set()
  const began = new Map() // session → its folder, told once a connection
  const attached = new Map() // `${session}\n${place}` → {session, place, content}
  const listeners = new Set()
  let sock = null
  let up = false // sock has said hello: what is sent before waits for the next connect
  let stopped = false
  let wait = RETRY[0]
  let timer = null
  const send = (m) => up && sock?.writable && sock.write(JSON.stringify(m) + '\n')
  const tell = () => send({ t: 'sessions', ids: [...sessions] })

  ctx.provide('iridium', {
    attach(session, place, content) {
      if (typeof session !== 'string' || !session || typeof place !== 'string' || !place) throw new Error('attach: a session and a place')
      const c = contentOf(content)
      const k = `${session}\n${place}`
      if (c === null) attached.delete(k)
      else attached.set(k, { session, place, content: c })
      send({ t: 'attach', session, place, content: c })
    },
    onButton(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
  })

  function connect() {
    if (stopped || !config?.endpoint) return
    const s = net.connect(config.endpoint)
    sock = s
    s.on('connect', () => {
      wait = RETRY[0]
      s.write(JSON.stringify({ t: 'hello', secret: config.secret }) + '\n')
      up = true
      tell()
      for (const [session, cwd] of began) send({ t: 'started', session, cwd })
      for (const a of attached.values()) send({ t: 'attach', ...a })
    })
    s.on('error', () => {})
    s.on('close', () => {
      if (sock === s) {
        sock = null
        up = false
      }
      if (stopped) return
      timer = setTimeout(connect, wait)
      timer.unref?.()
      wait = Math.min(wait * 2, RETRY[1])
    })
    lines(s, (m) => {
      if (m?.t !== 'button' || typeof m.session !== 'string' || typeof m.id !== 'string') return
      for (const fn of listeners) {
        try {
          Promise.resolve(fn(m.session, m.id, m.user)).catch(() => {})
        } catch {}
      }
    })
  }

  ctx.on('agent/created', ({ agent }) => {
    sessions.add(agent.id)
    tell()
    let cwd = ''
    try {
      cwd = ctx.get('workingDirectory')?.get(agent.session) ?? ''
    } catch {}
    began.set(agent.id, cwd || process.cwd())
    send({ t: 'started', session: agent.id, cwd: began.get(agent.id) })
  })
  ctx.on('agent/disposed', ({ agent }) => {
    sessions.delete(agent.id)
    tell()
  })
  connect()
  ctx.effect(() => () => {
    stopped = true
    clearTimeout(timer)
    listeners.clear()
    sock?.destroy()
  }, 'remote-iridium: the session\'s part')
}
