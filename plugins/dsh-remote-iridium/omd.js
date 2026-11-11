// OMD's (oh-my-dsh, a third party's dsh plugin) Ultra on the Iridium's page: its collaboration
// mode past the effort track, with a sub-mode and a level OMD picks for it, through OMD's own
// service (omd.ultracode). It hangs a key at the place "effort" of each session here with this
// plugin's service iridium (in DSH Desktop's process, and in a dsh process the Iridium started
// here: index.js and session.js run it), and a row of its sub-modes while on; a press switches
// it. A sub-plugin: on only while OMD is there (inject), and the only file here that knows OMD. Ultra on owns the effort: a level picked
// in the Iridium is a plain model selection, which OMD takes for leaving Ultra, so it is turned
// on again at the new level.

export const name = 'remote-iridium-omd'
export const inject = ['omd', 'iridium']

const SUBMODES = ['workflow', 'team', 'free']
const KEY = 'omd.ultra'

/** How often the sessions shown are looked at again, for a change nothing here tells of. */
const POLL = 5000

export const texts = {
  zh: {
    hint: 'OMD 的协作模式；子模式在下面选',
    unavailable: '会话未打开，或未用 OMD 的预设',
    workflow: 'Workflow',
    team: 'Agent Team',
    free: '自由',
  },
  en: {
    hint: "OMD's collaboration mode; its sub-mode below",
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
 * it cannot be switched; while on, a button a sub-mode, the one picked pressed.
 */
export function contentOf(v) {
  const on = v?.mode === 'ultra'
  const available = !!v?.eligible
  const out = {}
  for (const [lang, t] of Object.entries(texts)) {
    const title = available ? t.hint : v?.reason ? String(v.reason) : t.unavailable
    let s = `<button id="${KEY}" type="action"${on ? ' pressed' : ''}${available ? '' : ' disabled'} title="${esc(title)}">Ultra</button>`
    if (on) {
      for (const sub of SUBMODES) {
        if (sub === 'team' && !v.teamAvailable) continue
        s += `<button id="${KEY}.${sub}" type="action"${(v.submode ?? 'free') === sub ? ' pressed' : ''}>${esc(t[sub])}</button>`
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
  // A model selected: OMD's own (Ultra switched) or a level picked elsewhere (in the Iridium: a
  // plain selection, which takes OMD's Ultra off): Ultra was on, it is on again at that level.
  // A session first heard of by its events (it began before this plugin did): shown once.
  const seen = new Set()
  on('session/event', (session, event) => {
    const id = session?.id
    if (!id) return
    if (!seen.has(id) && !shown.has(id) && live(id)) {
      seen.add(id)
      render(id)
    }
    if (event?.type !== 'model/selection') return
    const was = shown.get(id)
    if (busy.has(id) || !was?.on) return void render(id)
    switching(id, async () => {
      const v = await omd.inspect(id)
      if (v?.mode === 'ultra') return
      const effort = event.data?.reasoningEffort ?? v?.selected?.reasoningEffort
      await omd.select(id, true, { option: was.submode, ...(effort ? { effort } : {}) })
    }).catch(() => {})
  })

  // The sessions open here now, and again now and then.
  const sweep = () => {
    const ids = new Set(shown.keys())
    try {
      for (const s of ctx.get('sessions')?.list?.() ?? []) if (live(s.id)) ids.add(s.id)
    } catch {}
    for (const id of ids) render(id)
  }
  sweep()
  const timer = setInterval(sweep, o.poll ?? POLL)
  timer.unref?.()
  ctx.effect?.(() => () => {
    clearInterval(timer)
    offButton()
    for (const id of shown.keys()) {
      try {
        iridium.attach(id, 'effort', null)
      } catch {}
    }
    shown.clear()
  }, 'remote-iridium-omd')
}

/** This file as a cordis sub-plugin (ctx.plugin). */
export const OmdUltra = { name, inject, apply }
