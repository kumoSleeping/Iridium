// The executor (Iridium docs/exec.md): connects out to an Iridium, starts the processes it asks
// for and relays them, and serves files under the roots it allows. It follows the reference
// executor in Go (internal/remote/executor); this file has no dsh in it, index.js has.
import { spawn, execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import fsp from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

export const PROTOCOL = 1
const MAX_READ = 4 << 20
const DEFAULT_GRACE = 5000
const CANNOT_START = 127
const OUT = 'out.log'
const ERR = 'err.log'
const EXIT = 'exit'

class RemoteError extends Error {
  constructor(code, message) {
    super(message)
    this.code = code
  }
}

const bad = (m) => new RemoteError('bad_request', m)

// Heartbeat (wire.PingEvery, wire.DeadAfter): a "ping" event every 25s keeps a proxy's idle timeout
// from cutting the connection; once the other side has pinged, 70s of nothing ends it.
const PING_EVERY = 25_000
const DEAD_AFTER = 70_000

/** A connection: requests both ways, answered by id; events one way. */
class Conn {
  constructor(ws, onRequest) {
    this.ws = ws
    this.onRequest = onRequest
    this.next = 0
    this.waits = new Map()
    this.heard = Date.now()
    this.pinged = false
    const beat = setInterval(() => {
      if (this.pinged && Date.now() - this.heard > DEAD_AFTER) return this.ws.close(4000, 'no heartbeat') // a client may close with 1000 or 3000–4999
      this.notify('ping')
    }, PING_EVERY)
    this.closed = new Promise((resolve) => {
      ws.addEventListener('close', () => {
        clearInterval(beat)
        for (const w of this.waits.values()) w.reject(new Error('connection closed'))
        this.waits.clear()
        resolve()
      })
    })
    ws.addEventListener('message', (e) => this.receive(e.data))
  }

  receive(data) {
    let f
    try {
      f = JSON.parse(typeof data === 'string' ? data : Buffer.from(data).toString('utf8'))
    } catch {
      this.ws.close(1002, 'bad frame')
      return
    }
    this.heard = Date.now()
    if (f.event === 'ping') this.pinged = true
    else if (f.method) {
      Promise.resolve()
        .then(() => this.onRequest(f.method, f.params ?? {}))
        .then(
          (result) => this.send({ id: f.id, result: result ?? {} }),
          (err) => this.send({ id: f.id, error: { code: codeOf(err), message: String(err.message ?? err) } }),
        )
    } else if (!f.event) {
      const w = this.waits.get(f.id)
      if (!w) return
      this.waits.delete(f.id)
      if (f.error) w.reject(new RemoteError(f.error.code, f.error.message))
      else w.resolve(f.result)
    }
  }

  send(f) {
    if (this.ws.readyState !== 1) return false
    this.ws.send(JSON.stringify(f))
    return true
  }

  call(method, params) {
    const id = ++this.next
    return new Promise((resolve, reject) => {
      this.waits.set(id, { resolve, reject })
      if (!this.send({ id, method, params })) {
        this.waits.delete(id)
        reject(new Error('not connected'))
      }
    })
  }

  notify(event, params) {
    return this.send({ event, params })
  }

  close() {
    try {
      this.ws.close(1000)
    } catch {}
  }
}

function codeOf(err) {
  if (err instanceof RemoteError) return err.code
  if (err && (err.code === 'ENOENT' || err.code === 'ENOTDIR')) return 'not_found'
  if (err && err.code === 'EEXIST') return 'exists'
  if (err && (err.code === 'EACCES' || err.code === 'EPERM')) return 'denied'
  return 'failed'
}

const fileSize = (p) => {
  try {
    return fs.statSync(p).size
  } catch {
    return 0
  }
}

/** One process and its files: out.log (stdout), err.log, in.seq, exit. */
class Holder {
  constructor(ex, id) {
    this.ex = ex
    this.id = id
    this.dir = path.join(ex.state, 'holds', id)
    this.child = null
    this.inSeq = 0
    this.exit = null
    this.tailN = 0
    this.tailOn = null
    this.tailAt = 0
    this.grace = DEFAULT_GRACE
    this.endedP = new Promise((r) => (this.markEnded = r))
    this.kick = null
  }

  state() {
    return { hold: this.id, outSize: fileSize(path.join(this.dir, OUT)), inSeq: this.inSeq, exit: this.exit }
  }

  finish(x) {
    if (this.exit) return
    try {
      fs.writeFileSync(path.join(this.dir, EXIT), JSON.stringify(x), { mode: 0o600 })
    } catch {}
    this.exit = x
    this.markEnded()
    this.poke()
  }

  poke() {
    const k = this.kick
    this.kick = null
    if (k) k()
  }

  start(spec) {
    fs.mkdirSync(this.dir, { recursive: true, mode: 0o700 })
    const out = fs.openSync(path.join(this.dir, OUT), 'a', 0o600)
    const err = fs.openSync(path.join(this.dir, ERR), 'a', 0o600)
    if (spec.stopGraceMs > 0) this.grace = spec.stopGraceMs
    const env = {}
    for (const kv of spec.env ?? []) {
      const i = kv.indexOf('=')
      if (i > 0) env[kv.slice(0, i)] = kv.slice(i + 1)
    }
    let child
    try {
      child = spawn(spec.argv[0], spec.argv.slice(1), {
        cwd: spec.cwd || undefined,
        env,
        stdio: ['pipe', out, err],
        detached: process.platform !== 'win32', // a group of its own, to end its whole tree
        windowsHide: true,
      })
    } catch (e) {
      fs.writeSync(err, `cannot start ${spec.argv[0]}: ${e.message}\n`)
      fs.closeSync(out)
      fs.closeSync(err)
      this.finish({ from: 'agent', code: CANNOT_START, signal: null })
      return 0
    }
    this.child = child
    let open = true
    const close = () => {
      if (!open) return
      open = false
      try {
        fs.closeSync(out)
        fs.closeSync(err)
      } catch {}
    }
    child.stdin.on('error', () => {})
    child.on('error', (e) => {
      if (open) {
        try {
          fs.writeSync(err, `cannot start ${spec.argv[0]}: ${e.message}\n`)
        } catch {}
      }
      close()
      this.finish({ from: 'agent', code: CANNOT_START, signal: null })
    })
    child.on('exit', (code, signal) => {
      close()
      this.finish(signal ? { from: 'agent', code: null, signal: signal.replace(/^SIG/, '') } : { from: 'agent', code, signal: null })
    })
    return child.pid ?? 0
  }

  write(seq, line) {
    if (seq <= this.inSeq) return { outSize: fileSize(path.join(this.dir, OUT)) }
    if (!this.child || this.exit) throw new RemoteError('failed', 'the process is not running')
    this.child.stdin.write(line + '\n')
    this.inSeq = seq
    try {
      fs.writeFileSync(path.join(this.dir, 'in.seq'), String(seq), { mode: 0o600 })
    } catch {}
    return { outSize: fileSize(path.join(this.dir, OUT)) }
  }

  tail(conn, from) {
    this.tailOn = conn
    this.tailAt = from
    const n = ++this.tailN
    this.push(conn, n)
    this.poke()
  }

  /** Pushes out.log from tailAt on conn, whole lines, as it grows; then the exit. */
  async push(conn, n) {
    const file = path.join(this.dir, OUT)
    const buf = Buffer.alloc(1 << 20)
    while (this.tailOn === conn && this.tailN === n) {
      const ended = this.exit !== null
      let data = null
      try {
        const fh = await fsp.open(file, 'r')
        try {
          const { bytesRead } = await fh.read(buf, 0, buf.length, this.tailAt)
          let chunk = buf.subarray(0, bytesRead)
          const nl = chunk.lastIndexOf(10)
          if (nl >= 0) chunk = chunk.subarray(0, nl + 1)
          else if (!ended || bytesRead === 0) chunk = null
          if (chunk && chunk.length) data = Buffer.from(chunk)
        } finally {
          await fh.close()
        }
      } catch {}
      // Stopping, its processes go with it: the other side learns so when it comes back.
      if (this.tailOn !== conn || this.tailN !== n || this.ex.stopped) return
      if (data) {
        if (!conn.notify('out', { hold: this.id, off: this.tailAt, data: data.toString('utf8') })) return
        this.tailAt += data.length
        continue
      }
      if (ended) {
        const x = this.exit
        conn.notify('exit', { hold: this.id, from: x.from, code: x.code ?? null, signal: x.signal ?? null, errTail: errTail(this.dir) })
        return
      }
      await new Promise((r) => {
        const t = setTimeout(r, 50)
        this.kick = () => {
          clearTimeout(t)
          r()
        }
      })
    }
  }

  stop(hard) {
    const child = this.child
    if (!child || this.exit) return
    if (hard) return killTree(child.pid, true)
    try {
      child.stdin.end()
    } catch {}
    const later = (ms, fn) => {
      const t = setTimeout(fn, ms)
      this.endedP.then(() => clearTimeout(t))
    }
    later(this.grace, () => {
      killTree(child.pid, false)
      later(this.grace, () => killTree(child.pid, true))
    })
  }
}

function errTail(dir) {
  try {
    const b = fs.readFileSync(path.join(dir, ERR))
    return b.subarray(Math.max(0, b.length - 2048)).toString('utf8')
  } catch {
    return ''
  }
}

/** Ends a process and its children: its group on Mac and Linux, taskkill /T on Windows. */
function killTree(pid, hard) {
  if (!pid) return
  if (process.platform === 'win32') {
    execFile('taskkill', [...(hard ? ['/F'] : []), '/T', '/PID', String(pid)], { windowsHide: true }, () => {})
    return
  }
  try {
    process.kill(-pid, hard ? 'SIGKILL' : 'SIGTERM')
  } catch {}
}

/**
 * The executor. Options: url (ws(s)://…/api/exec), token, state (its own directory), roots
 * (directories files may be under; none: all but the denied), agents (Hello's), noShell (start
 * nothing but the agents: no command line runs here), agentShell (say the Iridium's agents may run
 * command lines here, ir sh, not only a person on its page), env, program, log.
 */
export class Executor {
  constructor(o) {
    this.o = o
    this.state = o.state
    this.log = o.log ?? (() => {})
    this.holds = new Map()
    this.conn = null
    this.stopped = false
    fs.mkdirSync(path.join(this.state, 'holds'), { recursive: true, mode: 0o700 })
    this.load()
  }

  /** Holders of an earlier run: their processes went with it, so each has ended. */
  load() {
    for (const id of fs.readdirSync(path.join(this.state, 'holds'))) {
      const h = new Holder(this, id)
      try {
        h.inSeq = Number(fs.readFileSync(path.join(h.dir, 'in.seq'), 'utf8')) || 0
      } catch {}
      let x = null
      try {
        x = JSON.parse(fs.readFileSync(path.join(h.dir, EXIT), 'utf8'))
      } catch {}
      if (!x) {
        x = { from: 'holder', code: null, signal: null }
        try {
          fs.writeFileSync(path.join(h.dir, EXIT), JSON.stringify(x), { mode: 0o600 })
        } catch {}
      }
      h.exit = x
      h.markEnded()
      this.holds.set(id, h)
    }
  }

  /** Connects, and again whenever the connection ends, until stop. */
  async run() {
    // A Mac stays awake while a process it started runs (caffeinate -i for a minute, again every
    // 30s); a closed lid still sleeps it.
    const awake =
      process.platform === 'darwin' &&
      setInterval(() => {
        if ([...this.holds.values()].some((h) => !h.exit)) execFile('caffeinate', ['-i', '-t', '60'], () => {})
      }, 30000)
    let wait = 1000
    while (!this.stopped) {
      const began = Date.now()
      try {
        await this.session()
      } catch (e) {
        if (!this.stopped) this.log(`connection: ${e.message}`)
      }
      if (this.stopped) break
      if (Date.now() - began > 60000) wait = 1000
      await new Promise((r) => (this.wakeTimer = setTimeout(r, wait)))
      wait = Math.min(wait * 2, 30000)
    }
    if (awake) clearInterval(awake)
  }

  async session() {
    const ws = new WebSocket(this.o.url, { headers: { Authorization: `Bearer ${this.o.token}` } })
    await new Promise((resolve, reject) => {
      ws.addEventListener('open', resolve, { once: true })
      ws.addEventListener('error', (e) => reject(new Error(e.message || 'cannot connect')), { once: true })
    })
    const c = new Conn(ws, (m, p) => this.handle(c, m, p))
    this.conn = c
    try {
      await c.call('hello', this.hello())
      this.log('connected')
      await c.closed
    } finally {
      if (this.conn === c) this.conn = null
      for (const h of this.holds.values()) if (h.tailOn === c) h.tailOn = null
      c.close()
    }
  }

  hello() {
    return {
      protocol: PROTOCOL, program: this.o.program ?? 'dsh-remote-iridium', os: process.platform === 'win32' ? 'windows' : process.platform,
      arch: process.arch, home: os.homedir(), sep: path.sep, scratch: path.join(this.state, 'scratch'),
      env: this.o.env ?? Object.entries(process.env).map(([k, v]) => `${k}=${v}`),
      agents: this.o.agents ?? [], holds: [...this.holds.values()].map((h) => h.state()),
      ...(this.o.noShell ? { noShell: true } : {}),
      ...(this.o.agentShell ? { agentShell: true } : {}),
    }
  }

  /** Ends the connection now; it connects again. */
  drop() {
    this.conn?.close()
  }

  /** Stops connecting, and ends every process it runs. */
  stop() {
    this.stopped = true
    clearTimeout(this.wakeTimer)
    const c = this.conn
    this.conn = null
    for (const h of this.holds.values()) h.tailOn = null
    c?.close()
    for (const h of this.holds.values()) h.stop(true)
  }

  holder(id) {
    const h = this.holds.get(id)
    if (!h) throw new RemoteError('not_found', id)
    return h
  }

  async handle(c, method, p) {
    switch (method) {
      case 'hold.start': {
        if (!p.hold || /[\\/.]/.test(p.hold) || !Array.isArray(p.argv) || !p.argv.length) throw bad('bad hold.start')
        // With noShell, only an argv beginning with one of the agents' own.
        if (this.o.noShell && !(this.o.agents ?? []).some((a) => a.argv?.length && a.argv.every((x, i) => p.argv[i] === x)))
          throw new RemoteError('denied', 'only its agents start here')
        let h = this.holds.get(p.hold)
        if (h) return { pid: h.child?.pid ?? 0 }
        h = new Holder(this, p.hold)
        this.holds.set(p.hold, h)
        return { pid: h.start(p) }
      }
      case 'hold.write':
        return this.holder(p.hold).write(p.seq, p.line)
      case 'hold.tail':
        this.holder(p.hold).tail(c, p.from ?? 0)
        return {}
      case 'hold.stop':
        this.holder(p.hold).stop(false)
        return {}
      case 'hold.kill':
        this.holder(p.hold).stop(true)
        return {}
      case 'hold.forget': {
        const h = this.holder(p.hold)
        if (!h.exit) throw new RemoteError('failed', 'still running')
        this.holds.delete(p.hold)
        await fsp.rm(h.dir, { recursive: true, force: true })
        return {}
      }
      case 'fs.stat':
        return this.stat(p.path, p.head)
      case 'fs.list':
        return this.list(p.path)
      case 'fs.read':
        return this.read(p.path, p.offset ?? 0, p.length ?? 0)
      case 'fs.write':
        return this.write(p.path, p.data, p.sha256, p.replace)
      case 'fs.mkdir':
        await fsp.mkdir(await this.allowed(p.path), { recursive: true })
        return {}
    }
    throw new RemoteError('unsupported', method)
  }

  /** path, its links resolved, if under a root and not denied. */
  async allowed(p) {
    if (typeof p !== 'string' || !path.isAbsolute(p)) throw bad(`not absolute: ${p}`)
    p = path.resolve(p)
    let real = p
    for (let dir = p, rest = ''; ; ) {
      try {
        real = path.join(await fsp.realpath(dir), rest)
        break
      } catch {}
      const parent = path.dirname(dir)
      if (parent === dir) break
      rest = path.join(path.basename(dir), rest)
      dir = parent
    }
    const fold = (s) => (process.platform === 'win32' || process.platform === 'darwin' ? s.toLowerCase() : s)
    const under = (root) => {
      const rel = path.relative(fold(root), fold(real))
      return rel === '' || (!rel.startsWith('..') && !path.isAbsolute(rel))
    }
    for (const d of [path.join(this.state, 'token'), path.join(os.homedir(), '.ssh'), ...(this.o.denied ?? [])]) {
      if (under(d)) throw new RemoteError('denied', p)
    }
    const roots = this.o.roots ?? []
    if (!roots.length) return real
    for (const r of roots) {
      let rr = r
      try {
        rr = await fsp.realpath(r)
      } catch {}
      if (under(rr)) return real
    }
    throw new RemoteError('denied', p)
  }

  async stat(p, head) {
    const real = await this.allowed(p)
    const st = await fsp.stat(real)
    const out = { size: st.size, mtime: Math.floor(st.mtimeMs), type: typeOf(st) }
    if (head > 0 && st.isFile()) {
      const fh = await fsp.open(real, 'r')
      try {
        const b = Buffer.alloc(Math.min(head, st.size))
        const { bytesRead } = await fh.read(b, 0, b.length, 0)
        out.head = createHash('sha256').update(b.subarray(0, bytesRead)).digest('hex')
      } finally {
        await fh.close()
      }
    }
    return out
  }

  async list(p) {
    const real = await this.allowed(p)
    const entries = []
    for (const d of await fsp.readdir(real, { withFileTypes: true })) {
      try {
        const st = await fsp.stat(path.join(real, d.name))
        entries.push({ name: d.name, size: st.size, mtime: Math.floor(st.mtimeMs), type: typeOf(st) })
      } catch {}
    }
    return { entries }
  }

  async read(p, offset, length) {
    const real = await this.allowed(p)
    const fh = await fsp.open(real, 'r')
    try {
      const b = Buffer.alloc(Math.max(0, Math.min(length, MAX_READ)))
      const { bytesRead } = await fh.read(b, 0, b.length, offset)
      return { data: b.subarray(0, bytesRead).toString('base64') }
    } finally {
      await fh.close()
    }
  }

  async write(p, data, sha256, replace) {
    const real = await this.allowed(p)
    const b = Buffer.from(data ?? '', 'base64')
    if (createHash('sha256').update(b).digest('hex') !== sha256) throw bad('sha256 does not match')
    if (!replace && fs.existsSync(real)) throw new RemoteError('exists', p)
    await fsp.mkdir(path.dirname(real), { recursive: true })
    const tmp = `${real}.${process.pid}.${Date.now()}.tmp`
    await fsp.writeFile(tmp, b, { mode: 0o600 })
    await fsp.rename(tmp, real)
    return {}
  }
}

function typeOf(st) {
  if (st.isFile()) return 'file'
  if (st.isDirectory()) return 'dir'
  if (st.isSymbolicLink()) return 'link'
  return 'other'
}
