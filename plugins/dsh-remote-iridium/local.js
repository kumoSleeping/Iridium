// The Iridium on this computer, without pairing (Iridium's docs/exec.md "本机"): while it runs it
// writes local-exec.json in its state directory, readable by this user alone, with a loopback
// WebSocket address and a TOKEN of its own; whoever can read it is this user on this computer, so
// it is taken as is. Another of this user's Iridiums (a test one) is reached by its directory:
// IRIDIUM_STATE, or the plugin's setting.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

export const LOCAL_FILE = 'local-exec.json'

/** The state directory of this user's Iridium, as Iridium has it (config.StatePath's default). */
export function stateDir(env = process.env, platform = process.platform, home = os.homedir()) {
  if (env.IRIDIUM_STATE) return env.IRIDIUM_STATE
  if (env.XDG_STATE_HOME) return path.join(env.XDG_STATE_HOME, 'iridium')
  if (platform === 'win32' && env.LOCALAPPDATA) return path.join(env.LOCALAPPDATA, 'Iridium', 'state')
  return path.join(home, '.local', 'state', 'iridium')
}

/** Whether pid is a process running now (unknown: taken as running). */
function alive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return true
  try {
    process.kill(pid, 0)
    return true
  } catch (e) {
    return e.code === 'EPERM'
  }
}

/**
 * The local Iridium in dir: {ws, token, pid}; null if none runs there (no file, a file of one that
 * is gone, or not one to trust: not loopback, not a local TOKEN).
 */
export function readLocal(dir) {
  let v
  try {
    v = JSON.parse(fs.readFileSync(path.join(dir, LOCAL_FILE), 'utf8'))
  } catch {
    return null
  }
  if (typeof v?.ws !== 'string' || typeof v?.token !== 'string' || !v.token.startsWith('irl_')) return null
  let u
  try {
    u = new URL(v.ws)
  } catch {
    return null
  }
  const host = u.hostname.replace(/^\[|\]$/g, '')
  if (u.protocol !== 'ws:' || !(host === '127.0.0.1' || host === '::1' || host === 'localhost')) return null
  if (!alive(v.pid)) return null
  return { ws: v.ws, token: v.token, pid: v.pid }
}

/**
 * Follows the local Iridium in dir: on(found) each time one is there that was not (or another one:
 * a new TOKEN), off() when it goes. The file is looked at when its directory changes, and every
 * `every` ms as well (a directory watch is not sure everywhere). Returns stop().
 */
export function watchLocal(dir, on, off, { every = 15000 } = {}) {
  let cur = null
  const look = () => {
    const v = readLocal(dir)
    const key = v ? `${v.ws}\n${v.token}` : null
    if (key === cur) return
    cur = key
    if (v) on(v)
    else off()
  }
  let w = null
  const watch = () => {
    if (w) return
    try {
      w = fs.watch(dir, { persistent: false }, (_e, name) => {
        if (!name || name === LOCAL_FILE) setTimeout(look, 100)
      })
      w.on('error', () => {
        w?.close()
        w = null
      })
    } catch {
      w = null // no directory yet: the timer below finds it
    }
  }
  watch()
  look()
  const t = setInterval(() => {
    watch()
    look()
  }, every)
  t.unref?.()
  return () => {
    clearInterval(t)
    w?.close()
  }
}
