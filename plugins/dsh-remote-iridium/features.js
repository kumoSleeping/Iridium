// The features other plugins add to a session for the Iridium (its docs/api.md "features"): a
// switch each, shown at the effort track's right on its page. A registry, provided in dsh as the
// cordis service `iridiumFeatures`; a plugin gives a feature with provide(), its own texts and
// semantics inside, so the Iridium never learns whose it is. Apart from index.js so that it loads
// without dsh's packages (the tests).
import { RemoteError } from './executor.js'

/** A feature's id: `<plugin>.<name>`, as the Iridium takes it. */
const ID = /^[a-z0-9-]+\.[a-z0-9-]+$/

/**
 * How long a provider may take: a view (get; ownsEffort and effort together, effortNow), a change
 * (set, setEffort). A held.get waits on get and effortNow at once: within 3s, the Iridium's 6s.
 */
export const TIMEOUTS = { get: 3000, set: 15000 }

/** At most two held.changed a second per session. */
export const COALESCE = 500

/** A message as a reason: one line, at most 120 characters. */
const reasonOf = (e) => String(e?.message ?? e ?? 'failed').replace(/\s+/g, ' ').trim().slice(0, 120) || 'failed'

/** p, or a timeout after ms (its message the reason shown). */
function within(p, ms, what) {
  let t
  return Promise.race([
    Promise.resolve().then(p),
    new Promise((_, reject) => {
      t = setTimeout(() => reject(new Error(`${what}: no answer in ${ms / 1000}s`)), ms)
      t.unref?.()
    }),
  ]).finally(() => clearTimeout(t))
}

/**
 * The registry. o: unfeatured, the ids the owner switched off on the plugin's settings page
 * (config `unfeatured`): not offered, not set; timeouts, coalesce (tests).
 *
 * A provider: { id, name?, place?, get(session, {lang, held}) → view, set(session, {on, option})
 * → void, ownsEffort?(session) → bool, effort?(session) → level, setEffort?(session, level) }. A
 * view: { name, on, active?, available, reason?, hint?, options?: [{id, name}], option?, place,
 * tone? }. One whose id is given already waits till the first is disposed; an error or a timeout
 * makes only its feature unavailable, the error its reason.
 */
export function createFeatures(o = {}) {
  const unfeatured = o.unfeatured ?? (() => [])
  const timeouts = { ...TIMEOUTS, ...o.timeouts }
  const gap = o.coalesce ?? COALESCE
  const byId = new Map() // id → providers, the first the one in use
  const listeners = new Set()
  const pending = new Map() // session → { timer, last }: changed() coalesced
  const off = (id) => new Set(unfeatured()).has(id)
  const live = () => [...byId.values()].map((list) => list[0])

  function notify(session) {
    for (const fn of listeners) {
      try {
        fn(session)
      } catch {}
    }
  }

  const registry = {
    /** Adds a feature; the function returned takes it away. */
    provide(p) {
      if (!p || typeof p.id !== 'string' || !ID.test(p.id)) throw new Error(`a feature's id is <plugin>.<name>: ${p?.id}`)
      if (typeof p.get !== 'function' || typeof p.set !== 'function') throw new Error(`feature ${p.id}: get and set`)
      const list = byId.get(p.id) ?? []
      list.push(p)
      byId.set(p.id, list)
      if (list.length === 1) registry.changed(null)
      let done = false
      return () => {
        if (done) return
        done = true
        const l = byId.get(p.id) ?? []
        const first = l[0] === p
        l.splice(l.indexOf(p), 1)
        if (!l.length) byId.delete(p.id)
        if (first) registry.changed(null)
      }
    },

    /** The features registered, switched off or not: {id, name}. */
    list() {
      return live().map((p) => ({ id: p.id, name: typeof p.name === 'string' ? p.name : p.id }))
    },

    /** Session's features as the Iridium shows them, but those switched off. */
    async views(session, { lang = '', held = false } = {}) {
      const ps = live().filter((p) => !off(p.id))
      return Promise.all(
        ps.map(async (p) => {
          try {
            return viewOf(p, await within(() => p.get(session, { lang, held }), timeouts.get, p.id))
          } catch (e) {
            return { id: p.id, name: typeof p.name === 'string' ? p.name : p.id, on: false, available: false, reason: reasonOf(e), place: p.place ?? 'effort', v: 1 }
          }
        }),
      )
    },

    /** Switches feature id on session (option: one of its options); not_found if no such one. */
    async set(session, id, { on, option } = {}) {
      const p = byId.get(id)?.[0]
      if (!p || off(id)) throw new RemoteError('not_found', `no feature ${id} here`)
      try {
        await within(() => p.set(session, { on: !!on, ...(option ? { option } : {}) }), timeouts.set, id)
      } catch (e) {
        throw e instanceof RemoteError ? e : new RemoteError('failed', reasonOf(e))
      }
      registry.changed(session)
    },

    /**
     * The feature that owns session's effort now (one running it at a level of its own), switched
     * off here or not: what runs is what counts. All asked at once, the first registered that says
     * so wins; none if none, or none answers in time.
     */
    async owner(session, ms = timeouts.get) {
      const ps = live().filter((p) => typeof p.ownsEffort === 'function')
      const says = await Promise.all(ps.map((p) => within(() => p.ownsEffort(session), ms, p.id).catch(() => false)))
      return ps.find((_, i) => says[i])
    },

    /**
     * Session's effort owner now and the level it runs it at ({owner, effort}; effort undefined if
     * it does not say), both within one timeouts.get; {} if none owns it or no answer in time.
     */
    async effortNow(session) {
      const end = Date.now() + timeouts.get
      try {
        const owner = await registry.owner(session, timeouts.get)
        if (!owner) return {}
        const left = end - Date.now()
        if (left <= 0 || typeof owner.effort !== 'function') return { owner }
        const v = await within(() => owner.effort(session), left, owner.id).catch(() => undefined)
        return { owner, effort: typeof v === 'string' && v ? v : undefined }
      } catch {
        return {}
      }
    },

    /** Sets session's effort through its owner p. */
    async setEffort(p, session, level) {
      if (typeof p?.setEffort !== 'function') throw new RemoteError('unsupported', `${p?.id} sets no effort`)
      try {
        await within(() => p.setEffort(session, level), timeouts.set, p.id)
      } catch (e) {
        throw e instanceof RemoteError ? e : new RemoteError('failed', reasonOf(e))
      }
      registry.changed(session)
    },

    /**
     * Session's features may have changed (a provider calls it; a set does): the listeners hear
     * of it at most once in `coalesce` ms per session, the last time at its end. null: every
     * session's (a feature came or went), at once.
     */
    changed(session) {
      if (session === null || session === undefined) return notify(null)
      const p = pending.get(session) ?? { timer: null, last: 0 }
      pending.set(session, p)
      if (p.timer) return
      const wait = Math.max(0, p.last + gap - Date.now())
      p.timer = setTimeout(() => {
        p.timer = null
        p.last = Date.now()
        notify(session)
      }, wait)
      p.timer.unref?.()
    },

    /** fn(session) on each change (session null: any); the function returned stops it. */
    listen(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },

    /** Stops the coalescing timers. */
    dispose() {
      for (const p of pending.values()) clearTimeout(p.timer)
      pending.clear()
      listeners.clear()
    },
  }
  return registry
}

/** A provider's view as the wire has it (wire.Feature): its id, the fields the Iridium reads. */
function viewOf(p, v) {
  if (!v || typeof v !== 'object') throw new Error(`${p.id}: no view`)
  const text = (x) => (typeof x === 'string' && x ? x : undefined)
  const options = Array.isArray(v.options)
    ? v.options.filter((x) => x && typeof x.id === 'string' && x.id).map((x) => ({ id: x.id, name: String(x.name ?? x.id) }))
    : []
  const out = {
    id: p.id,
    name: text(v.name) ?? text(p.name) ?? p.id,
    on: !!v.on,
    available: !!v.available,
    place: text(v.place) ?? p.place ?? 'effort',
    v: 1,
  }
  if (v.active) out.active = true
  if (text(v.reason)) out.reason = v.reason
  if (text(v.hint)) out.hint = v.hint
  if (options.length) out.options = options
  if (text(v.option)) out.option = v.option
  if (text(v.tone)) out.tone = v.tone
  return out
}

/** The ids switched off, as the config has them (a list; text with commas). */
export function unfeaturedList(v) {
  const all = Array.isArray(v) ? v : String(v ?? '').split(/[,，;；\n]/)
  return [...new Set(all.map((x) => String(x).trim()).filter(Boolean))]
}
