// What plugins hang on a session in the Iridium's page (its docs/next/plugin.md "挂元素"): a piece
// of Satori elements at a place ("effort": past the effort track), in one text or one per language
// ({zh, en}). The service `iridium` a plugin injects: attach(session, place, content) and
// onButton(fn), fn(session, id, user) for a button of it pressed there. In DSH Desktop's process
// this registry is it, sending to the Iridium (session.attach) over the executor's connection;
// in a dsh process the Iridium started (session.js) its own sends here over the bridge. Apart
// from index.js so that it loads without dsh's packages (the tests).

/** A place's name, as the Iridium takes it. */
const PLACE = /^[a-z][a-z0-9-]{0,31}$/

/** At most this much text a content (each language's). */
const MAX = 4096

/**
 * content as the wire has it: a string, or {lang: string} with at least one; null for none (an
 * empty string, an empty object). An error if it is neither or too long.
 */
export function contentOf(content) {
  if (content === null || content === undefined || content === '') return null
  const ok = (s) => typeof s === 'string' && s.length <= MAX
  if (typeof content === 'string') {
    if (!ok(content)) throw new Error(`a content is at most ${MAX} characters`)
    return content
  }
  if (typeof content !== 'object' || Array.isArray(content)) throw new Error('a content is a string or {lang: string}')
  const out = {}
  for (const [lang, s] of Object.entries(content)) {
    if (!/^[a-z]{2}(-[A-Za-z]{2})?$/.test(lang) || !ok(s)) throw new Error(`content ${lang}: a string of at most ${MAX} characters`)
    if (s) out[lang] = s
  }
  return Object.keys(out).length ? out : null
}

/**
 * The registry. send(params) sends a session.attach event to the Iridium (false if not
 * connected); toProcess(session, id, user), a button pressed on one a dsh process the Iridium
 * started runs, to that process (bridge.js). Returns the service, and: resend() (each attached,
 * once connected again), button(session, id, user) (the Iridium's session.button), drop(owner)
 * (all an owner attached: a process gone), attached() (for tests).
 */
export function createAttach({ send = () => false, toProcess = () => {} } = {}) {
  const items = new Map() // `${session}\n${place}` → {session, place, content, owner}
  const listeners = new Set()
  const key = (session, place) => `${session}\n${place}`

  function put(session, place, content, owner = '') {
    if (typeof session !== 'string' || !session) throw new Error('attach: a session')
    if (typeof place !== 'string' || !PLACE.test(place)) throw new Error(`attach: no place ${place}`)
    const c = contentOf(content)
    const k = key(session, place)
    const was = items.get(k)
    if (c === null) {
      if (!was) return
      items.delete(k)
    } else {
      if (was && JSON.stringify(was.content) === JSON.stringify(c)) {
        was.owner = owner
        return
      }
      items.set(k, { session, place, content: c, owner })
    }
    send({ session, place, content: c })
  }

  return {
    /** The service: attach and onButton. */
    service: {
      attach: (session, place, content) => put(session, place, content),
      onButton(fn) {
        listeners.add(fn)
        return () => listeners.delete(fn)
      },
    },
    /** Puts as owner (a dsh process's connection): drop(owner) takes it away with the rest of it. */
    put,
    resend() {
      for (const { session, place, content } of items.values()) send({ session, place, content })
    },
    button(session, id, user) {
      if (typeof session !== 'string' || typeof id !== 'string' || !session || !id) return
      const u = user && typeof user === 'object' ? { id: String(user.id ?? ''), ...(user.name ? { name: String(user.name) } : {}) } : undefined
      for (const fn of listeners) {
        try {
          Promise.resolve(fn(session, id, u)).catch(() => {})
        } catch {}
      }
      toProcess(session, id, u)
    },
    drop(owner) {
      for (const [k, v] of items) {
        if (v.owner === owner) {
          items.delete(k)
          send({ session: v.session, place: v.place, content: null })
        }
      }
    },
    attached: () => [...items.values()].map(({ session, place, content }) => ({ session, place, content })),
  }
}
