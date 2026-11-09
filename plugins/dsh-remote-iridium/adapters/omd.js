// OMD's (oh-my-dsh, a third party's dsh plugin) Ultra as an Iridium feature: its collaboration mode
// past the effort track, with a sub-mode and a level OMD picks for it, through OMD's own service in
// this process (omd.ultracode): DSH Desktop's, or a session's dsh process the Iridium started
// (session.js), held there. The only file here that knows OMD: what the Iridium sees is the
// feature `omd.ultra`. Optional: in dsh it runs as a sub-plugin only while both OMD and the
// registry are there, and goes with OMD.
import { RemoteError } from '../executor.js'

const SUBMODES = ['workflow', 'team', 'free']

const texts = {
  zh: {
    hint: 'OMD 的协作模式；子模式在下面选',
    unavailable: '会话不在 DSH Desktop 里打开着，或没用 OMD 的预设',
    workflow: 'Workflow',
    team: 'Agent Team',
    free: '自由',
  },
  en: {
    hint: "OMD's collaboration mode; its sub-mode below",
    unavailable: 'Not open in DSH Desktop, or not on an OMD preset',
    workflow: 'Workflow',
    team: 'Agent Team',
    free: 'Free',
  },
}

/**
 * The feature over omd, OMD's service: inspect(session) → {eligible, mode: 'ultra'|'off', submode,
 * teamAvailable, selected: {provider, model, reasoningEffort?}, revision, reason?}; select(session,
 * {provider, model, reasoningEffort?, mode, submode?, expectedRevision}). Ultra on without a level
 * leaves it to OMD (xhigh, else the highest) unless it is on already (its level kept: a sub-mode
 * alone); off runs at the track's level, the one selected now. While on it owns the effort.
 */
export function omdFeature(omd) {
  const ultra = () => {
    const u = omd?.ultracode
    if (!u?.inspect || !u?.select) throw new Error('no oh-my-dsh here')
    return u
  }
  const inspect = (session) => ultra().inspect(session)
  // A change from a fresh look: OMD refuses one made over a selection changed since.
  const select = async (session, on, { option, effort } = {}) => {
    const u = ultra()
    const before = await u.inspect(session)
    const sel = before?.selected
    if (!sel) throw new Error('no model selected in this session')
    const level = effort ?? (on ? (before.mode === 'ultra' ? sel.reasoningEffort : undefined) : sel.reasoningEffort)
    await u.select(session, {
      provider: sel.provider, model: sel.model, ...(level === undefined ? {} : { reasoningEffort: level }),
      mode: on ? 'ultra' : 'off', ...(option ? { submode: option } : {}), expectedRevision: before.revision,
    })
  }
  return {
    id: 'omd.ultra',
    name: 'Ultra',
    place: 'effort',
    async get(session, { lang, held }) {
      const t = texts[lang === 'en' ? 'en' : 'zh']
      let v
      try {
        v = await inspect(session)
      } catch (e) {
        // One Desktop does not hold may be one OMD has never seen: not an error, only off.
        if (held) throw e
      }
      const available = held && !!v?.eligible
      const options = SUBMODES.filter((x) => x !== 'team' || v?.teamAvailable).map((id) => ({ id, name: t[id] }))
      // OMD keeps its mode with the session, held or not: on is in effect (Desktop runs it
      // whenever it has the session), as the page showed it before (the tag warm, the mark lit).
      const on = v?.mode === 'ultra'
      return {
        name: 'Ultra', on, active: on, available, hint: t.hint, options, option: v?.submode ?? 'free', place: 'effort', tone: 'warm',
        ...(available ? {} : { reason: held && v?.reason ? String(v.reason) : t.unavailable }),
      }
    },
    async set(session, { on, option }) {
      if (option !== undefined && !SUBMODES.includes(option)) throw new RemoteError('bad_request', `no sub-mode ${option}`)
      await select(session, on, { option })
    },
    ownsEffort: async (session) => (await inspect(session))?.mode === 'ultra',
    effort: async (session) => (await inspect(session))?.selected?.reasoningEffort,
    setEffort: (session, level) => select(session, true, { effort: level }),
  }
}

/** Gives features (the registry) OMD's Ultra over omd; the function returned takes it away. */
export function registerOmd(features, omd) {
  return features.provide(omdFeature(omd))
}

/**
 * The sub-plugin index.js runs: on while OMD and the registry both are (cordis starts it when
 * both services come and stops it when either goes), its feature taken away with it.
 */
export const OmdAdapter = {
  name: 'remote-iridium-omd',
  inject: ['omd', 'iridiumFeatures'],
  apply(ctx) {
    ctx.effect(() => registerOmd(ctx.get('iridiumFeatures'), ctx.get('omd')), 'remote-iridium: OMD Ultra')
  },
}
