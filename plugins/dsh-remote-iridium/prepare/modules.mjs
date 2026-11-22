// Iridium: a plugin module from dsh's profiles, which the sdk profile cannot resolve.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const spec = decodeURIComponent(new URL(import.meta.url).search.slice(1))
const parts = spec.split('/')
const pkg = parts.slice(0, spec.startsWith('@') ? 2 : 1).join('/')
const sub = '.' + spec.slice(pkg.length)
const home = process.env.DSH_HOME?.trim() ? process.env.DSH_HOME : path.join(os.homedir(), '.dsh')
const profiles = path.resolve(home, 'profiles')

// dsh's own packages (@deepseek-ai/*), which a bundle has as peers: dsh gives them its plugins,
// but a module imported here by its file looks for them from where it lies (the profile), where
// they are not. One not found there is taken from the dsh this process runs (its bin's package),
// once per process. Without module.registerHooks (Node before 22.15): as before.
function dshPeers() {
  const key = Symbol.for('iridium.dsh-peers')
  if (globalThis[key]) return
  globalThis[key] = true
  const register = process.getBuiltinModule?.('node:module')?.registerHooks
  let dir = ''
  try {
    dir = path.dirname(fs.realpathSync(process.argv[1]))
  } catch {}
  while (dir && !isDsh(dir)) dir = path.dirname(dir) === dir ? '' : path.dirname(dir)
  if (!register || !dir) return
  const parentURL = pathToFileURL(path.join(dir, 'package.json')).href
  register({
    resolve(specifier, context, next) {
      try {
        return next(specifier, context)
      } catch (e) {
        if (!specifier.startsWith('@deepseek-ai/') || e?.code !== 'ERR_MODULE_NOT_FOUND') throw e
        return next(specifier, { ...context, parentURL })
      }
    },
  })
}

// The root of dsh's own package: its package.json is @deepseek-ai/dsh.
function isDsh(dir) {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8')).name === '@deepseek-ai/dsh'
  } catch {
    return false
  }
}

// A package.json exports target under Node's import conditions.
function pick(t) {
  if (typeof t === 'string') return t
  if (Array.isArray(t)) {
    for (const x of t) {
      const r = pick(x)
      if (r !== undefined) return r
    }
    return
  }
  if (t && typeof t === 'object') {
    for (const [k, v] of Object.entries(t)) {
      if (k === 'node' || k === 'import' || k === 'default') {
        const r = pick(v)
        if (r !== undefined) return r
      }
    }
  }
}

// The file of subpath sub in the package at dir, relative to it.
function entry(dir, sub) {
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
  let ex = manifest.exports
  if (ex === undefined || ex === null) {
    if (sub === '.') return manifest.main ?? 'index.js'
    return ['', '.js', '.mjs'].map((e) => sub + e).find((f) => fs.existsSync(path.join(dir, f)))
  }
  if (typeof ex === 'string' || Array.isArray(ex) || !Object.keys(ex).some((k) => k.startsWith('.'))) ex = { '.': ex }
  if (Object.hasOwn(ex, sub)) return pick(ex[sub])
  let best
  for (const k of Object.keys(ex)) {
    const i = k.indexOf('*')
    if (i < 0) continue
    const pre = k.slice(0, i), post = k.slice(i + 1)
    if (sub.length >= pre.length + post.length && sub.startsWith(pre) && sub.endsWith(post) && (!best || pre.length > best.pre.length)) best = { k, pre, post }
  }
  return best && pick(ex[best.k])?.replaceAll('*', sub.slice(best.pre.length, sub.length - best.post.length))
}

let names = []
try {
  names = fs.readdirSync(profiles).sort()
} catch {}
let file
for (const p of ['web', ...names.filter((n) => n !== 'web')]) {
  const dir = path.join(profiles, p, 'node_modules', ...pkg.split('/'))
  if (!fs.existsSync(path.join(dir, 'package.json'))) continue
  const f = entry(dir, sub)
  if (f) {
    file = path.resolve(dir, f)
    break
  }
}
if (!file) throw new Error('iridium: no ' + spec + ' in the profiles at ' + profiles)
dshPeers()
const m = await import(pathToFileURL(file).href)
let plugin = m.default ?? m
if (plugin?.__esModule) plugin = plugin.default ?? plugin
export default plugin
