// The sessions the dsh this plugin runs in holds (DSH Desktop has them loaded, its writer lock),
// for the Iridium (its docs/exec.md "远端 DSH"): it cannot resume them, so it asks here. Apart from
// index.js so that it loads without dsh's packages (the tests' run.mjs).
import { RemoteError } from './executor.js'

/** A session watched this long after the Iridium last asked about it. */
const WATCH = 10 * 60_000

/** How often a watched session is looked at again, for a change nothing here tells of. */
const POLL = 3000

const bad = (m) => new RemoteError('bad_request', m)
const sessionOf = (p, what) => {
  if (typeof p?.session !== 'string' || !p.session) throw bad(`${what}: a session`)
  return p.session
}

/**
 * Session's live state here (wire.HeldLive): held, whether an agent of it lives in this process;
 * then its model ("<provider>/<model>") and the level it runs at.
 */
export async function heldLive(ctx, session) {
  const agent = ctx.get('agents')?.get(session)
  const out = { held: !!agent }
  if (agent) {
    const cur = agent.model?.current?.()
    if (cur?.provider && cur?.model) out.model = `${cur.provider}/${cur.model}`
    if (cur?.reasoningEffort) out.effort = cur.reasoningEffort
  }
  return out
}

/**
 * held.list, held.send, held.get, held.set (wire CapHeld). kept(): what the plugin's settings keep
 * from the Iridium (unsynced, the workspaces by folder or name; ungrouped false, the sessions in
 * none of them); follow: followHeld's, told of each session the Iridium asks about.
 */
export function heldMethods(ctx, kept = () => ({ unsynced: [], ungrouped: true }), follow) {
  // running: those whose agent here works now (a turn, or one waiting on an approval or an
  // answer). A session Desktop opened stays loaded, its writer lock held, till Desktop quits:
  // the Iridium says "elsewhere" only while it runs, and gives the others' messages to it here.
  const list = async () => {
    const k = kept()
    const ids = (ctx.get('sessions')?.list() ?? []).map((s) => s.id)
    const agents = ctx.get('agents')
    return { sessions: ids, running: ids.filter((id) => agents?.get(id)?.status === 'running'), unsynced: k.unsynced, ...(k.ungrouped === false ? { unsyncedUngrouped: true } : {}) }
  }
  // A message for a session this dsh holds: its agent here takes it, as a next turn or (steer)
  // into the running one; not_found if no agent of it lives here.
  const send = async (p) => {
    if (typeof p.session !== 'string' || !p.session || typeof p.text !== 'string' || !p.text.trim()) throw bad('held.send: session and text')
    const a = ctx.get('agents')?.get(p.session)
    if (!a) throw new RemoteError('not_found', `no agent of ${p.session} here`)
    const input = { content: [{ type: 'text', text: p.text }], source: { kind: 'user' } } // yours, through the Iridium (its tag before the words)
    try {
      await (p.steer ? a.steer(input) : a.followup(input))
    } catch (e) {
      throw new RemoteError('failed', e.message)
    }
    return {}
  }
  const reply = async (session) => {
    const live = await heldLive(ctx, session)
    follow?.seen(session, live)
    return live
  }
  return {
    'held.list': list,
    'held.send': send,
    'held.get': async (p) => reply(sessionOf(p, 'held.get')),
    // One of model ("<provider>/<model>") and effort on a session held here, through dsh's own
    // model selection (as Desktop's picker makes it); its live state after. A model is kept at the
    // level it runs at if the new one has it.
    'held.set': async (p) => {
      const session = sessionOf(p, 'held.set')
      const asked = ['model', 'effort'].filter((k) => p[k] !== undefined && p[k] !== null && p[k] !== '')
      if (asked.length !== 1) throw bad('held.set: one of model and effort')
      const agent = ctx.get('agents')?.get(session)
      if (!agent) throw new RemoteError('not_found', `no agent of ${session} here`)
      if (p.effort !== undefined && p.effort !== null && p.effort !== '') {
        if (typeof p.effort !== 'string' || p.effort.length > 64) throw bad('held.set: effort')
        const cur = agent.model?.current?.()
        if (!cur?.provider || !cur?.model) throw new RemoteError('failed', 'no model selected in this session')
        await selectModel(ctx, session, { provider: cur.provider, model: cur.model, reasoningEffort: p.effort })
      } else {
        const m = typeof p.model === 'string' ? p.model.indexOf('/') : -1
        if (m <= 0 || m === p.model.length - 1) throw bad('held.set: model as <provider>/<model>')
        const provider = p.model.slice(0, m)
        const model = p.model.slice(m + 1)
        const effort = agent.model?.current?.()?.reasoningEffort
        const has = effort === undefined ? false : await hasEffort(ctx, provider, model, effort)
        try {
          await selectModel(ctx, session, { provider, model, ...(has === false ? {} : { reasoningEffort: effort }) })
        } catch (e) {
          // Not known whether the model has that level: without it then.
          if (has !== undefined) throw e
          await selectModel(ctx, session, { provider, model })
        }
      }
      return reply(session)
    },
  }
}

/** Selects a model on a session loaded here, as Desktop's picker does (dsh's session controller). */
async function selectModel(ctx, sessionId, sel) {
  const sc = ctx.get('sessionController')
  if (typeof sc?.selectModel !== 'function') throw new RemoteError('unsupported', 'no session controller in this dsh')
  try {
    await sc.selectModel({ sessionId, ...sel })
  } catch (e) {
    throw new RemoteError('failed', String(e?.message ?? e))
  }
}

/** Whether provider/model has the effort level; undefined if this dsh cannot tell. */
async function hasEffort(ctx, provider, model, effort) {
  const llm = ctx.get('llm')
  if (typeof llm?.resolveModelInfo !== 'function') return undefined
  try {
    const info = await llm.resolveModelInfo(provider, model)
    return !!info?.reasoning?.efforts?.some((e) => e.id === effort)
  } catch {
    return undefined
  }
}

/**
 * held.changed for the Iridium: a session it asked about (held.get, held.set) in the last ten
 * minutes, or since and held here all along (the Iridium keeps what it was told and asks again
 * only when the session's state is built anew), sent by emit(event, params) when its live state
 * is not what was last sent or answered. Told by dsh (a model selected, an agent loaded or gone:
 * ctx.on where there is one), and every 3s for one held here (a change nothing tells of). Returns
 * {seen, stop}: seen is heldMethods' follow. o: watch, poll (ms; tests).
 */
export function followHeld(ctx, emit, o = {}) {
  const watch = o.watch ?? WATCH
  const poll = o.poll ?? POLL
  const watched = new Map() // session → { until, last: JSON of the live state last sent }
  const holds = (id) => {
    try {
      return !!ctx.get('agents')?.get(id)
    } catch {
      return false
    }
  }
  // Watched still: asked about lately, or held here (its watch renewed while it is).
  const kept = (id, w) => {
    if (holds(id)) w.until = Math.max(w.until, Date.now() + watch)
    return Date.now() <= w.until
  }
  // One look at a time per session; one asked for meanwhile follows it.
  async function send(session) {
    const w = watched.get(session)
    if (!w) return
    if (!kept(session, w)) return void watched.delete(session)
    if (w.busy) return void (w.again = true)
    w.busy = true
    try {
      const live = await heldLive(ctx, session)
      const s = JSON.stringify(live)
      if (s !== w.last && watched.get(session) === w) {
        w.last = s
        w.held = live.held
        emit('held.changed', { session, live })
      }
    } catch {
    } finally {
      w.busy = false
      if (w.again) {
        w.again = false
        send(session)
      }
    }
  }
  const offs = []
  if (typeof ctx.on === 'function') {
    const on = (name, fn) => {
      try {
        const off = ctx.on(name, fn, { global: true })
        if (typeof off === 'function') offs.push(off)
      } catch {}
    }
    on('session/event', (session, event) => {
      if (event?.type === 'model/selection' && watched.has(session?.id)) send(session.id)
    })
    for (const name of ['agent/created', 'agent/disposed']) on(name, (e) => watched.has(e?.agent?.id) && send(e.agent.id))
  }
  const timer = setInterval(() => {
    for (const [id, w] of watched) {
      if (!kept(id, w)) watched.delete(id)
      else if (w.held || holds(id)) send(id)
    }
  }, poll)
  timer.unref?.()
  return {
    seen(session, live) {
      watched.set(session, { until: Date.now() + watch, last: JSON.stringify(live), held: live.held })
    },
    stop() {
      clearInterval(timer)
      for (const off of offs) off()
      watched.clear()
    },
  }
}
