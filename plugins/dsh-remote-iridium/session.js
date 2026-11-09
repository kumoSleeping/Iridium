// The part of this plugin in a dsh process the Iridium starts here (dsh --profile sdk, a session):
// the features registry (iridiumFeatures) for the plugins in that process, OMD's Ultra one of them
// (the same adapter as in DSH Desktop), told to the plugin in Desktop over its bridge (bridge.js),
// which answers the Iridium for it. The patch dsh.session writes loads it; config: endpoint and
// secret, the bridge's.
import net from 'node:net'
import { OmdAdapter } from './adapters/omd.js'
import { lines } from './bridge.js'
import { RemoteError } from './executor.js'
import { createFeatures } from './features.js'

export const name = 'remote-iridium-session'
export const inject = ['agents']

/** The least and most wait before connecting again. */
const RETRY = [500, 10_000]

/**
 * A call from the bridge on registry features (the session's: params.session): live {lang}, its
 * features as the Iridium shows them and the feature that owns its effort now ({features, owner?,
 * effort?}); set {id, on, option?}; setEffort {level}, through that owner (unsupported if none).
 */
export async function answer(features, method, p) {
  const session = p.session
  switch (method) {
    case 'live': {
      const lang = p.lang === 'en' || p.lang === 'zh' ? p.lang : ''
      const [list, own] = await Promise.all([features.views(session, { lang, held: true }), features.effortNow(session)])
      return { features: list, ...(own.owner ? { owner: own.owner.id, ...(own.effort ? { effort: own.effort } : {}) } : {}) }
    }
    case 'set':
      if (typeof p.id !== 'string' || typeof p.on !== 'boolean') throw new RemoteError('bad_request', 'set: {id, on, option?}')
      await features.set(session, p.id, { on: p.on, ...(typeof p.option === 'string' && p.option ? { option: p.option } : {}) })
      return {}
    case 'setEffort': {
      if (typeof p.level !== 'string' || !p.level) throw new RemoteError('bad_request', 'setEffort: a level')
      const owner = await features.owner(session)
      if (!owner?.setEffort) throw new RemoteError('unsupported', 'no feature owns the effort')
      await features.setEffort(owner, session, p.level)
      return {}
    }
  }
  throw new RemoteError('not_found', `no method ${method}`)
}

export function apply(ctx, config) {
  const features = createFeatures()
  ctx.provide('iridiumFeatures', features)
  ctx.plugin(OmdAdapter)
  const sessions = new Set()
  let sock = null
  let stopped = false
  let wait = RETRY[0]
  let timer = null
  const send = (m) => sock?.writable && sock.write(JSON.stringify(m) + '\n')
  const tell = () => send({ t: 'sessions', ids: [...sessions] })

  function connect() {
    if (stopped || !config?.endpoint) return
    const s = net.connect(config.endpoint)
    sock = s
    s.on('connect', () => {
      wait = RETRY[0]
      s.write(JSON.stringify({ t: 'hello', secret: config.secret }) + '\n')
      tell()
    })
    s.on('error', () => {})
    s.on('close', () => {
      if (sock === s) sock = null
      if (stopped) return
      timer = setTimeout(connect, wait)
      timer.unref?.()
      wait = Math.min(wait * 2, RETRY[1])
    })
    lines(s, async (m) => {
      if (m?.t !== 'call') return
      try {
        const result = await answer(features, m.method, m.params ?? {})
        send({ t: 're', id: m.id, result })
      } catch (e) {
        send({ t: 're', id: m.id, error: { code: e?.code ?? 'failed', message: String(e?.message ?? e) } })
      }
    })
  }

  ctx.on('agent/created', ({ agent }) => {
    sessions.add(agent.id)
    tell()
  })
  ctx.on('agent/disposed', ({ agent }) => {
    sessions.delete(agent.id)
    tell()
  })
  const off = features.listen((session) => {
    if (session === null) for (const s of sessions) send({ t: 'changed', session: s })
    else if (sessions.has(session)) send({ t: 'changed', session })
  })
  connect()
  ctx.effect(() => () => {
    stopped = true
    clearTimeout(timer)
    off()
    features.dispose()
    sock?.destroy()
  }, 'remote-iridium: the session\'s features')
}
