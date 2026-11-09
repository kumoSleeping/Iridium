// The dsh processes the Iridium starts here (dsh --profile sdk, one a session) run their sessions'
// features themselves: a plugin there (OMD) is in that process, not in this one. Each loads
// session.js, which connects back here over a local socket; held.get and held.set for a session
// one of them runs go to it (held.js). Apart from index.js so that it loads without dsh's packages.
import { createHash, randomBytes } from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { RemoteError } from './executor.js'

/** The socket for state (this plugin's directory): a named pipe on Windows, a file elsewhere. */
export function bridgeEndpoint(state) {
  if (process.platform === 'win32') return `\\\\.\\pipe\\dsh-remote-iridium-${createHash('sha256').update(state).digest('hex').slice(0, 16)}`
  return path.join(state, 'bridge.sock')
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
 * changed(session) is called when one says a session's features changed, or that it runs one
 * now or no more. Returns {secret, has(session), call(session, method, params, ms), close()}.
 */
export function createBridge({ endpoint, changed = () => {}, log = () => {} }) {
  const secret = randomBytes(24).toString('hex')
  const bySession = new Map() // session → its process's connection
  const conns = new Set()
  let seq = 0

  const server = net.createServer((sock) => {
    const c = { sock, ok: false, sessions: new Set(), calls: new Map() }
    const drop = () => {
      conns.delete(c)
      for (const s of c.sessions) {
        if (bySession.get(s) === c) {
          bySession.delete(s)
          changed(s)
        }
      }
      for (const { reject } of c.calls.values()) reject(new RemoteError('failed', 'the session\'s dsh process is gone'))
      c.calls.clear()
    }
    sock.on('close', drop)
    sock.on('error', () => {})
    lines(sock, (m) => {
      if (!c.ok) {
        if (m?.t !== 'hello' || m.secret !== secret) return sock.destroy()
        c.ok = true
        conns.add(c)
        return
      }
      if (m.t === 'sessions' && Array.isArray(m.ids)) {
        const now = new Set(m.ids.filter((s) => typeof s === 'string' && s))
        for (const s of c.sessions) {
          if (!now.has(s) && bySession.get(s) === c) {
            bySession.delete(s)
            changed(s)
          }
        }
        for (const s of now) {
          if (bySession.get(s) !== c) {
            bySession.set(s, c)
            changed(s)
          }
        }
        c.sessions = now
      } else if (m.t === 'changed' && typeof m.session === 'string') {
        if (bySession.get(m.session) === c) changed(m.session)
      } else if (m.t === 're') {
        const p = c.calls.get(m.id)
        if (!p) return
        c.calls.delete(m.id)
        if (m.error) p.reject(new RemoteError(m.error.code || 'failed', String(m.error.message ?? 'failed')))
        else p.resolve(m.result)
      }
    })
  })
  server.on('error', (e) => log(`bridge: ${e.message}`))
  if (process.platform !== 'win32') {
    try {
      fs.mkdirSync(path.dirname(endpoint), { recursive: true, mode: 0o700 })
      fs.rmSync(endpoint, { force: true })
    } catch {}
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
    /** Asks the process that runs session; an error if none does, or no answer in ms. */
    call(session, method, params = {}, ms = 3000) {
      const c = bySession.get(session)
      if (!c) return Promise.reject(new RemoteError('not_found', `no dsh process here runs ${session}`))
      const id = ++seq
      return new Promise((resolve, reject) => {
        const t = setTimeout(() => {
          c.calls.delete(id)
          reject(new RemoteError('failed', `${method}: no answer in ${ms / 1000}s`))
        }, ms)
        t.unref?.()
        c.calls.set(id, {
          resolve: (v) => (clearTimeout(t), resolve(v)),
          reject: (e) => (clearTimeout(t), reject(e)),
        })
        c.sock.write(JSON.stringify({ t: 'call', id, method, params: { ...params, session } }) + '\n')
      })
    },
    close() {
      server.close()
      for (const c of conns) c.sock.destroy()
      if (process.platform !== 'win32') {
        try {
          fs.rmSync(endpoint, { force: true })
        } catch {}
      }
    },
  }
}
