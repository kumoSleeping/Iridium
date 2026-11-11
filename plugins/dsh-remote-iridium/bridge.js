// The dsh processes the Iridium starts here (dsh --profile sdk, one a session) have plugins of
// their own (OMD's, in a bundle's layer): what they hang on their sessions for the Iridium's page
// (attach.js) comes here, and goes on over the executor's connection; a button pressed there goes
// back to the process. Each loads session.js, which connects back here over a local socket.
// Apart from index.js so that it loads without dsh's packages.
import { createHash, randomBytes } from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'

/**
 * A socket of its own for a plugin started with state (its directory): a named pipe on Windows, a
 * file there elsewhere. Its own: one that closes takes its path with it (node unlinks it), and
 * one started before it may still be closing.
 */
export function bridgeEndpoint(state) {
  const own = randomBytes(6).toString('hex')
  if (process.platform === 'win32') return `\\\\.\\pipe\\dsh-remote-iridium-${createHash('sha256').update(state).digest('hex').slice(0, 12)}-${own}`
  return path.join(state, `bridge-${own}.sock`)
}

/** Removes the sockets in dir no plugin listens at any more (one that ended without closing). */
function sweep(dir, keep) {
  let names = []
  try {
    names = fs.readdirSync(dir).filter((n) => /^bridge-[0-9a-f]+\.sock$/.test(n))
  } catch {}
  for (const n of names) {
    const p = path.join(dir, n)
    if (p === keep) continue
    const s = net.connect(p)
    s.on('connect', () => s.destroy())
    s.on('error', (e) => {
      if (e.code === 'ECONNREFUSED') fs.rm(p, { force: true }, () => {})
    })
  }
}

/** Frames: one JSON object a line. */
export function lines(sock, fn) {
  let buf = ''
  sock.setEncoding('utf8')
  sock.on('data', (d) => {
    buf += d
    for (let i; (i = buf.indexOf('\n')) >= 0; ) {
      const l = buf.slice(0, i)
      buf = buf.slice(i + 1)
      let m
      try {
        m = JSON.parse(l)
      } catch {
        continue
      }
      fn(m)
    }
  })
}

/**
 * Listens at endpoint for the session processes; one that does not say secret first is let go.
 * A process says which sessions it runs ({t:'sessions', ids}), that one began ({t:'started',
 * session, cwd}: started(session, cwd)), and what it hangs on one ({t:'attach', session, place,
 * content}: attach(session, place, content, owner), owner its connection); gone(owner) once it
 * closes. Returns {secret, endpoint, has(session), button(session, id, user) (to the process that
 * runs it: {t:'button', …}), close()}.
 */
export function createBridge({ endpoint, started = () => {}, attach = () => {}, gone = () => {}, log = () => {} }) {
  const secret = randomBytes(24).toString('hex')
  const bySession = new Map() // session → its process's connection
  const conns = new Set()

  const server = net.createServer((sock) => {
    const c = { sock, ok: false, sessions: new Set() }
    sock.on('close', () => {
      conns.delete(c)
      for (const s of c.sessions) if (bySession.get(s) === c) bySession.delete(s)
      if (c.ok) gone(c)
    })
    sock.on('error', () => {})
    lines(sock, (m) => {
      if (!c.ok) {
        if (m?.t !== 'hello' || m.secret !== secret) return sock.destroy()
        c.ok = true
        conns.add(c)
        return
      }
      try {
        if (m.t === 'sessions' && Array.isArray(m.ids)) {
          const now = new Set(m.ids.filter((s) => typeof s === 'string' && s))
          for (const s of c.sessions) if (!now.has(s) && bySession.get(s) === c) bySession.delete(s)
          for (const s of now) bySession.set(s, c)
          c.sessions = now
        } else if (m.t === 'started' && typeof m.session === 'string' && m.session) {
          started(m.session, typeof m.cwd === 'string' ? m.cwd : '')
        } else if (m.t === 'attach' && typeof m.session === 'string' && m.session) {
          attach(m.session, m.place, m.content ?? null, c)
        }
      } catch (e) {
        log(`bridge: ${e.message}`)
      }
    })
  })
  server.on('error', (e) => log(`bridge: ${e.message}`))
  if (process.platform !== 'win32') {
    try {
      fs.mkdirSync(path.dirname(endpoint), { recursive: true, mode: 0o700 })
    } catch {}
    sweep(path.dirname(endpoint), endpoint)
  }
  server.listen(endpoint, () => {
    if (process.platform !== 'win32') {
      try {
        fs.chmodSync(endpoint, 0o600)
      } catch {}
    }
  })
  server.unref?.()

  return {
    secret,
    endpoint,
    has: (session) => bySession.has(session),
    /** A button pressed on session, to the process that runs it (none: nothing). */
    button(session, id, user) {
      const c = bySession.get(session)
      if (c?.sock.writable) c.sock.write(JSON.stringify({ t: 'button', session, id, ...(user ? { user } : {}) }) + '\n')
    },
    close() {
      server.close()
      for (const c of conns) c.sock.destroy()
    },
  }
}
