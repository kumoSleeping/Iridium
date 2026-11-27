// What the dsh this plugin runs in (DSH Desktop's process, a dsh web profile) tells the Iridium
// (its docs/exec.md "DSH Desktop"), asked (desktop.* requests) and as it changes (desktop.*
// events): its models, its agent presets, its workspaces, its open sessions, the plugin's "sync new
// sessions" switch, the keys a new session may start with; and, in a windowless one the Iridium started, the hand-over to Desktop
// (desktop.freeze). Nothing is polled: dsh's own events say when to tell again.
import { RemoteError } from './executor.js'

/** How long changes are gathered before one event tells of them. */
const GATHER = 300

/** The models dsh offers here: every provider's, Value "<provider>/<model>", and the default. */
export async function modelsOf(ctx) {
  const llm = ctx.get('llm')
  if (!llm?.listProviders) throw new RemoteError('unsupported', 'no models service in this dsh')
  const models = []
  for (const p of llm.listProviders()) {
    for (const m of await llm.listModels(p.id).catch(() => [])) {
      const info = await llm.resolveModelInfo?.(p.id, m.id).catch(() => undefined)
      const efforts = info?.reasoning?.efforts ?? []
      const x = { value: `${p.id}/${m.id}`, group: p.name || p.id }
      if (m.name) x.name = m.name
      if (m.description) x.description = m.description
      if (efforts.length) {
        x.efforts = efforts.map((e) => e.id).filter(Boolean)
        const names = Object.fromEntries(efforts.filter((e) => e.name && e.name !== e.id).map((e) => [e.id, e.name]))
        if (Object.keys(names).length) x.effortNames = names
      } else x.noEffort = true
      models.push(x)
    }
  }
  const sel = ctx.get('agentDefaultModel')?.currentSelection?.()
  const out = { models }
  if (sel?.provider && sel?.model) {
    out.default = `${sel.provider}/${sel.model}`
    for (const m of models) if (m.value === out.default) m.default = true
  }
  return out
}

/** Desktop's workspaces in its order, and the sessions archived there. */
export function workspacesOf(ctx) {
  const reg = ctx.get('workspaceRegistry')
  if (!reg) return { workspaces: [] }
  const workspaces = reg.list().map((w) => ({
    id: String(w.id),
    title: w.title ?? '',
    path: w.path,
    sessions: [...(w.sessionIds ?? [])].map(String),
    ...(w.updatedAt ? { updatedAt: Date.parse(w.updatedAt) || 0 } : {}),
  }))
  return { workspaces, archived: [...(reg.archivedSessionIds ?? [])].map(String) }
}

/** One session as the Iridium is told of it (discover's DesktopSession, harness's too). */
export function sessionView(ctx, session, summary) {
  const h = session?.header ?? {}
  const id = String(session?.id ?? summary?.sessionId ?? h.id)
  const v = summary?.projections?.values ?? {}
  const agent = ctx.get('agents')?.get(id)
  const reg = ctx.get('workspaceRegistry')
  const out = { id }
  const cwd = h.cwd ?? summary?.cwd
  if (cwd) out.cwd = cwd
  if (v.title || summary?.title) out.title = v.title || summary.title
  const last = v.sessionListMetadata?.lastPromptAt ?? summary?.updatedAt
  if (last) (out.last = last), (out.updatedAt = summary?.updatedAt ?? last)
  if (h.origin === 'subagent') out.sub = true
  if (h.parentSession) out.parent = String(h.parentSession)
  if (agent?.session === session || summary?.agentAvailable) out.open = true
  if (agent?.status === 'running' || summary?.running) out.running = true
  else if (out.open) out.ended = true
  if (reg?.archivedSessionIds?.includes?.(id)) out.archived = true
  const ws = reg?.list?.().find((w) => w.sessionIds?.includes?.(id))
  if (ws) out.workspaceId = String(ws.id)
  return out
}

/** The sessions open here now (a live agent each), subagents' too. */
export function openSessions(ctx) {
  const sc = ctx.get('sessionController')
  const out = []
  for (const agent of ctx.get('agents')?.list?.() ?? []) {
    let summary
    try {
      summary = sc?.listState?.summaryFor?.(agent.session)
    } catch {}
    out.push(sessionView(ctx, agent.session, summary))
  }
  return out
}

/**
 * desktop.* for the executor (methods) and its events (sent by emit(event, params) while
 * connected). o: sync() the "sync new sessions" switch; keys() the keys a new session may start
 * with (async, api.Attached's; none given: not told); config() what else desktop.config says;
 * freeze() the hand-over of a windowless one ({frozen, running}).
 */
export function createDesktop(ctx, emit, o = {}) {
  const pending = new Map() // event → timer
  const later = (event, make) => {
    if (pending.has(event)) return
    const t = setTimeout(async () => {
      pending.delete(event)
      try {
        emit(event, await make())
      } catch {}
    }, o.gather ?? GATHER)
    t.unref?.()
    pending.set(event, t)
  }
  const tellSessions = () => later('desktop.sessions.changed', () => ({ full: true, sessions: openSessions(ctx) }))
  const tellModels = () => later('desktop.models.changed', () => modelsOf(ctx))
  const tellPresets = () => later('desktop.presets.changed', () => ({}))
  const tellWorkspaces = () => later('desktop.workspaces.changed', () => workspacesOf(ctx))
  const keys = async () => {
    try {
      const k = await o.keys()
      return Array.isArray(k) ? k : []
    } catch {
      return []
    }
  }
  const config = async () => ({ syncNewSessions: !!o.sync?.(), ...(o.keys ? { keys: await keys() } : {}), ...(o.config?.() ?? {}) })
  const tellConfig = () => later('desktop.config', config)

  const on = (name, fn) => {
    try {
      const off = ctx.on(name, fn, { global: true })
      if (typeof off === 'function') ctx.effect?.(() => off, `remote-iridium-desktop: ${name}`)
    } catch {}
  }
  for (const e of ['agent/created', 'agent/disposed', 'agent/status', 'session/created', 'session/disposed', 'api-session/activity'])
    on(e, tellSessions)
  // A session's title, its last prompt: what a sessions event shows.
  on('session/event', (_s, event) => {
    if (event?.type === 'session/title' || event?.type === 'user/message' || event?.type === 'turn/end') tellSessions()
  })
  for (const e of ['llm/models-updated', 'llm/adapters-updated']) on(e, tellModels)
  on('plugin-manager/changed', () => (tellPresets(), tellModels(), tellConfig()))
  // Plugins turned on or off, presets edited (the start keys: OMD, its presets, Agent Team).
  for (const e of ['loader/config-update', 'settings/document-updated']) on(e, tellConfig)
  on('domain/changed', (change) => {
    // The workspace registry's (sessions filed, archived): dsh's storage domains all say this.
    if (!change?.domain || /workspace/i.test(String(change.domain))) tellWorkspaces()
  })
  ctx.effect?.(() => () => {
    for (const t of pending.values()) clearTimeout(t)
    pending.clear()
  }, 'remote-iridium-desktop: pending')

  /** All of it again, as on a new connection: the config first. */
  const tellAll = async () => {
    emit('desktop.config', await config())
    emit('desktop.workspaces.changed', workspacesOf(ctx))
    emit('desktop.sessions.changed', { full: true, sessions: openSessions(ctx) })
    tellModels()
  }

  const methods = {
    'desktop.models': () => modelsOf(ctx),
    'desktop.workspaces': () => workspacesOf(ctx),
    // On demand (the Iridium's search): Desktop's sessions, open or not, matching query.
    'desktop.sessions': async (p) => {
      const sc = ctx.get('sessionController')
      if (!sc) throw new RemoteError('unsupported', 'no session controller in this dsh')
      const q = typeof p?.query === 'string' ? p.query.trim() : ''
      const limit = Math.min(Math.max(Number(p?.limit) || 50, 1), 500)
      const signal = AbortSignal.timeout(20000)
      let items
      try {
        items = q && sc.search ? (await sc.search({ query: q }, signal)).items : (await sc.list({}, signal)).items
      } catch (e) {
        throw new RemoteError('failed', e?.message ?? String(e))
      }
      const sessions = []
      for (const it of items ?? []) {
        const live = ctx.get('agents')?.get(it.sessionId)?.session
        sessions.push(sessionView(ctx, live ?? { id: it.sessionId, header: { cwd: it.cwd, ...(it.origin ? { origin: it.origin } : {}) } }, it))
        if (sessions.length >= limit) break
      }
      sessions.sort((a, b) => (b.last ?? 0) - (a.last ?? 0))
      return { sessions }
    },
    'desktop.config.set': async (p) => {
      if (typeof p?.syncNewSessions === 'boolean') {
        if (!o.setSync) throw new RemoteError('unsupported', 'syncNewSessions cannot be changed here')
        await o.setSync(p.syncNewSessions)
      }
      const c = await config()
      emit('desktop.config', c)
      return c
    },
    'desktop.freeze': async () => {
      if (!o.freeze) throw new RemoteError('unsupported', 'not a windowless dsh of the Iridium\'s')
      return o.freeze()
    },
  }
  return { methods, tellAll, tellConfig: async () => emit('desktop.config', await config()), tellSessions }
}

/**
 * The hand-over of a windowless dsh the Iridium started (IRIDIUM_DESKTOP_HEADLESS) to DSH Desktop:
 * its stores written no more (Desktop writes the same files; dsh's JSON storage has no lock between
 * processes), no session made here, and the process ends once no agent of it runs. refuse(): what
 * the inproc holds take for "make no session".
 */
export function createFreeze(ctx, { refuse = () => {}, exit = (code) => process.exit(code), log = () => {} } = {}) {
  let frozen = false
  const running = () => (ctx.get('agents')?.list?.() ?? []).filter((a) => a.status === 'running').length
  return async () => {
    if (!frozen) {
      frozen = true
      refuse()
      const b = ctx.get('storage.backend.json')
      const names = []
      for (const [n, u] of b?.open ?? []) {
        names.push(n)
        if (typeof u.publish === 'function') u.publish = async () => {}
        if (typeof u.writeDocument === 'function') u.writeDocument = async () => {}
      }
      log(`handed over to DSH Desktop: ${names.length} stores written no more`)
      let ended = false
      const idle = () => {
        if (ended || running() > 0) return
        ended = true
        log('no turn running: ending')
        exit(0)
      }
      ctx.on('agent/status', () => setImmediate(idle), { global: true })
      setImmediate(idle)
    }
    return { frozen: true, running: running() }
  }
}
