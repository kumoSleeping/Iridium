// OMD's (oh-my-dsh, a third party's dsh plugin) Ultra on the Iridium's page: its collaboration
// mode past the effort track, with a sub-mode and a level OMD picks for it, through OMD's own
// service (omd.ultracode). It hangs a key at the place "effort" of each session here with this
// plugin's service iridium (in DSH Desktop's process, and in a dsh process the Iridium started
// here: index.js runs it), and its sub-modes, each with its mark: the page shows one
// key, off or the sub-mode on, a press the next (off, then each sub-mode, then off). A sub-plugin: on only while OMD is there (inject), and the only file here that knows OMD. Ultra on owns the effort: a level picked
// in the Iridium is a plain model selection, which OMD takes for leaving Ultra, so it is turned
// on again at the new level. The same key, off, is one a new session may start with (startKeys,
// told in desktop.config; applyStartKey, at its first launch).

export const name = 'remote-iridium-omd'
export const inject = ['omd', 'iridium']

const SUBMODES = ['workflow', 'team', 'free']
const KEY = 'omd.ultra'

/** Each sub-mode's mark: lucide's workflow, users and infinity, drawn by the page in its own colour
 *  (only the shape counts). */
const svg = (body) =>
  'data:image/svg+xml;base64,' +
  btoa(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#000" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${body}</svg>`)
export const MARKS = {
  workflow: svg('<rect width="8" height="8" x="3" y="3" rx="2"/><path d="M7 11v4a2 2 0 0 0 2 2h4"/><rect width="8" height="8" x="13" y="13" rx="2"/>'),
  team: svg('<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><path d="M16 3.128a4 4 0 0 1 0 7.744"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><circle cx="9" cy="7" r="4"/>'),
  free: svg('<path d="M6 16c5 0 7-8 12-8a4 4 0 0 1 0 8c-5 0-7-8-12-8a4 4 0 1 0 0 8"/>'),
}

/** How long a session's events are gathered before it is looked at again. */
const GATHER = 200

export const texts = {
  zh: {
    hint: 'OMD 的协作模式',
    unavailable: '会话未打开，或未用 OMD 的预设',
    workflow: 'Workflow',
    team: 'Agent Team',
    free: '自由',
  },
  en: {
    hint: "OMD's collaboration mode",
    unavailable: 'Not open here, or not on an OMD preset',
    workflow: 'Workflow',
    team: 'Agent Team',
    free: 'Free',
  },
}

/** Text or an attribute value in Satori's element syntax. */
export const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/**
 * What hangs at "effort" for OMD's view v of a session ({eligible, mode, submode, teamAvailable,
 * reason?}), in each language: the key, pressed while on, disabled (its reason the title) while
 * it cannot be switched; while it can, a button a sub-mode with its mark, the one on pressed.
 */
export function contentOf(v) {
  const on = v?.mode === 'ultra'
  const available = !!v?.eligible
  const out = {}
  for (const [lang, t] of Object.entries(texts)) {
    const title = available ? t.hint : v?.reason ? String(v.reason) : t.unavailable
    let s = `<button id="${KEY}" type="action"${on ? ' pressed' : ''}${available ? '' : ' disabled'} title="${esc(title)}">Ultra</button>`
    if (available) {
      for (const sub of SUBMODES) {
        if (sub === 'team' && !v.teamAvailable) continue
        const cur = on && (v.submode ?? 'free') === sub
        s += `<button id="${KEY}.${sub}" type="action"${cur ? ' pressed' : ''}><img src="${MARKS[sub]}"/>${esc(t[sub])}</button>`
      }
    }
    out[lang] = s
  }
  return out
}

/**
 * Switching over omd's ultracode: inspect(session) → {eligible, mode: 'ultra'|'off', submode,
 * teamAvailable, selected: {provider, model, reasoningEffort?}, revision, reason?};
 * select(session, {provider, model, reasoningEffort?, mode, submode?, expectedRevision}). Ultra on
 * without a level leaves it to OMD (xhigh, else the highest) unless it is on already (its level
 * kept: a sub-mode alone); off runs at the level selected now.
 */
export function ultra(omd) {
  const u = () => {
    const x = omd?.ultracode
    if (!x?.inspect || !x?.select) throw new Error('no oh-my-dsh here')
    return x
  }
  return {
    inspect: (session) => u().inspect(session),
    async select(session, on, { option, effort } = {}) {
      const before = await u().inspect(session)
      const sel = before?.selected
      if (!sel) throw new Error('no model selected in this session')
      const level = effort ?? (on ? (before.mode === 'ultra' ? sel.reasoningEffort : undefined) : sel.reasoningEffort)
      await u().select(session, {
        provider: sel.provider, model: sel.model, ...(level === undefined ? {} : { reasoningEffort: level }),
        mode: on ? 'ultra' : 'off', ...(option ? { submode: option } : {}), expectedRevision: before.revision,
      })
    },
  }
}

export function apply(ctx, o = {}) {
  const iridium = ctx.get('iridium')
  const omd = ultra(ctx.get('omd'))
  const shown = new Map() // session → { content: JSON last attached, on, submode }
  const busy = new Set() // sessions being switched here: their selection events are this plugin's
  const live = (id) => {
    try {
      return !!ctx.get('agents')?.get(id)
    } catch {
      return false
    }
  }

  async function render(session) {
    let v = null
    try {
      v = await omd.inspect(session)
    } catch {}
    // One OMD has never seen, or not open here: nothing.
    const content = v && live(session) ? contentOf(v) : null
    const s = JSON.stringify(content)
    const was = shown.get(session)
    if (content) shown.set(session, { content: s, on: v.mode === 'ultra', submode: v.submode, effort: v.selected?.reasoningEffort })
    else shown.delete(session)
    if ((was?.content ?? 'null') === s) return
    try {
      iridium.attach(session, 'effort', content)
    } catch {}
  }

  async function switching(session, fn) {
    busy.add(session)
    try {
      await fn()
    } finally {
      busy.delete(session)
      await render(session)
    }
  }

  const offButton = iridium.onButton(async (session, id) => {
    if (id !== KEY && !id.startsWith(KEY + '.')) return
    const sub = id === KEY ? '' : id.slice(KEY.length + 1)
    if (sub && !SUBMODES.includes(sub)) return
    await switching(session, async () => {
      if (sub) await omd.select(session, true, { option: sub })
      else {
        const v = await omd.inspect(session)
        await omd.select(session, v?.mode !== 'ultra')
      }
    }).catch(() => {})
  })

  const on = (name, fn) => {
    try {
      const off = ctx.on(name, fn, { global: true })
      if (typeof off === 'function') ctx.effect?.(() => off, `remote-iridium-omd: ${name}`)
    } catch {}
  }
  on('agent/created', (e) => e?.agent?.id && render(e.agent.id))
  on('agent/disposed', (e) => {
    const id = e?.agent?.id
    if (!id || !shown.has(id)) return
    shown.delete(id)
    try {
      iridium.attach(id, 'effort', null)
    } catch {}
  })
  // A session looked at again shortly after its events (OMD's Ultra switched in Desktop's window
  // writes some): once for a burst of them.
  const soon = new Map() // session → timer
  const later = (id) => {
    if (soon.has(id)) return
    const t = setTimeout(() => {
      soon.delete(id)
      render(id)
    }, o.gather ?? GATHER)
    t.unref?.()
    soon.set(id, t)
  }
  // A model selected: OMD's own (Ultra switched) or a level picked elsewhere (in the Iridium: a
  // plain selection, which takes OMD's Ultra off): Ultra was on, it is on again at that level.
  on('session/event', (session, event) => {
    const id = session?.id
    if (!id || !live(id)) return
    if (event?.type !== 'model/selection') return later(id)
    const was = shown.get(id)
    if (busy.has(id) || !was?.on) return later(id)
    switching(id, async () => {
      const v = await omd.inspect(id)
      if (v?.mode === 'ultra') return
      const effort = event.data?.reasoningEffort ?? v?.selected?.reasoningEffort
      await omd.select(id, true, { option: was.submode, ...(effort ? { effort } : {}) })
    }).catch(() => {})
  })

  // The sessions open here now.
  try {
    for (const s of ctx.get('sessions')?.list?.() ?? []) if (live(s.id)) render(s.id)
  } catch {}
  ctx.effect?.(() => () => {
    for (const t of soon.values()) clearTimeout(t)
    soon.clear()
    offButton()
    for (const id of shown.keys()) {
      try {
        iridium.attach(id, 'effort', null)
      } catch {}
    }
    shown.clear()
  }, 'remote-iridium-omd')
}

/** The plugin rows whose presence makes an agent preset OMD's (its Workflow), and dsh's Agent Team. */
const WORKFLOW = 'oh-my-dsh/host/tool-workflow'
const TEAM = '@deepseek-ai/dsh-tool-agent-team'

/**
 * OMD's Ultra as a key a new session may start with (desktop.config's keys), off: its sub-modes
 * as OMD offers them (Agent Team while dsh's is loaded, running and on for new sessions), for the
 * agent presets with OMD's Workflow on (presets: all, when the registry cannot tell). None without
 * OMD, or with no such preset. Read through ctx at each call: OMD may come and go.
 */
export async function startKeys(ctx) {
  try {
    if (!ctx.get('omd')?.ultracode) return []
    // A new session is in team mode only where dsh turns it on for each new one (its
    // defaultEnabled); else Agent Team is not there at its first turn.
    const teamAvailable =
      ctx.get('agentTeams')?.defaultEnabled === true &&
      [...(ctx.get('loader')?.entries?.() ?? [])].some((e) => e?.options?.name === TEAM && !e.disabled && e.fiber?.state === 2)
    const key = { place: 'effort', from: '', content: contentOf({ eligible: true, mode: 'off', teamAvailable }) }
    const reg = ctx.get('agentPresets')
    if (typeof reg?.compositionInventory !== 'function') return [key]
    const presets = []
    for (const p of (await reg.compositionInventory()) ?? []) {
      if (p?.broken != null || !Array.isArray(p.rows)) continue
      if (p.rows.some((r) => r?.moduleName === WORKFLOW && r.enabled === true)) presets.push(String(p.id))
    }
    return presets.length ? [{ ...key, presets }] : []
  } catch {
    return []
  }
}

/**
 * The start key pressed for a new session (id: the head, on at OMD's own sub-mode; or a sub-mode),
 * turned on before its first turn. Other ids, or no OMD: nothing.
 */
export async function applyStartKey(ctx, session, id) {
  const omd = ctx.get('omd')
  if (!omd?.ultracode || typeof id !== 'string') return
  if (id === KEY) return ultra(omd).select(session, true)
  const sub = id.startsWith(KEY + '.') ? id.slice(KEY.length + 1) : ''
  if (SUBMODES.includes(sub)) return ultra(omd).select(session, true, { option: sub })
}

/** This file as a cordis sub-plugin (ctx.plugin). */
export const OmdUltra = { name, inject, apply }
