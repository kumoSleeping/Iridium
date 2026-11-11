// Requests about the dsh this plugin runs in, beside the executor's own. Apart from index.js so
// that it loads without dsh's packages (the tests' run.mjs).
import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { bound, boundPatch } from './compose.js'
import { RemoteError } from './executor.js'
import { heldMethods } from './held.js'

/** The agents it prepares launches of. */
const AGENT = 'dsh'

/**
 * What the dsh this plugin runs in knows, for the Iridium (its docs/exec.md "远端 DSH", docs/next/
 * plugin.md): the sessions it holds (held.js: held.list, held.send, held.get, held.set), with
 * kept() what this plugin's settings keep from it (unsynced, the workspaces, by folder or name;
 * ungrouped false, the sessions in none of them) and o.follow followHeld's; agent.presets, the
 * agent presets it registers; agent.prepare, the patches a dsh process the Iridium starts here
 * runs with: this dsh's models (llm: {file, scope}), a preset of its profile's bundles bound as
 * Desktop binds it (compose.js; o.dir where it is written), and this plugin's part for the session
 * (session.js, connected to o.bridge; o.sessionFile).
 */
export function dshMethods(ctx, kept = () => ({ unsynced: [], ungrouped: true }), llm = {}, o = {}) {
  const presets = async () => {
    const reg = ctx.get('agentPresets')
    if (!reg) throw new RemoteError('unsupported', 'no agent preset registry in this dsh')
    try {
      const out = []
      for (const p of await reg.list()) {
        const doc = await reg.readDocument(p.id)
        out.push({
          id: p.id,
          ...(p.name ? { name: p.name } : {}),
          ...(p.description ? { description: p.description } : {}),
          plugins: doc?.content ?? '',
          ...(p.broken ? { broken: String(p.broken) } : {}),
        })
      }
      return { presets: out, default: reg.defaultId ?? '' }
    } catch (e) {
      throw new RemoteError('failed', e.message)
    }
  }
  const write = (file, text) => {
    fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 })
    fs.writeFileSync(file, text, { mode: 0o600 })
    return file
  }
  const agentOf = (p, what) => {
    if (p?.agent !== AGENT) throw new RemoteError('unsupported', `${what}: no agent ${p?.agent} here`)
  }
  return {
    ...heldMethods(ctx, kept, o.follow),
    // The agent presets this dsh registers (its agentPresets registry: the shipped ones, a
    // bundle's, the profile's own edits), each with its child plugin rows as YAML; and the
    // default, the one set in its settings.
    'agent.presets': async (p) => {
      agentOf(p, 'agent.presets')
      return presets()
    },
    // The patches for a dsh process the Iridium starts here, in their order: this dsh's models;
    // with a session, this plugin's part in it (and the session parts of the profile's other
    // bundles), then its preset bound over the profile's bundles it names, if it names any
    // (preset true: the Iridium adds no preset of its own; system, its system prompt, follows the
    // preset's persona). Without a session: the models only (the Iridium's look at them).
    'agent.prepare': async (p) => {
      agentOf(p, 'agent.prepare')
      const patches = []
      if (llm.file && llm.scope && ctx.get('loader')?.entries) {
        try {
          patches.push(write(llm.file, llmPatch(ctx.get('loader'), baseIDs(llm.scope))))
        } catch (e) {
          throw new RemoteError('failed', `models: ${e.message}`)
        }
      }
      if (typeof p.session !== 'string' || !p.session) return { patches, preset: false }
      if (!/^[A-Za-z0-9_-]{1,128}$/.test(p.session)) throw new RemoteError('bad_request', 'agent.prepare: a session id')
      const dir = ctx.get('profileContext')?.dir
      let bundles = []
      try {
        bundles = dir ? profileBundles(dir) : []
      } catch (e) {
        throw new RemoteError('failed', `bundles: ${e.message}`)
      }
      if (o.bridge && o.sessionFile) patches.push(write(o.sessionFile, sessionPatch(o.bridge, bundles)))
      if (!ctx.get('agentPresets') || !llm.scope || !o.dir) return { patches, preset: false }
      const { presets: list, default: def } = await presets()
      const id = typeof p.preset === 'string' && p.preset ? p.preset : def
      const preset = list.find((x) => x.id === id)
      const binds = preset ? bound(preset, bundles) : []
      if (!binds.length) return { patches, preset: false }
      try {
        const text = boundPatch(llm.scope, preset, typeof p.system === 'string' ? p.system : '', moduleURL('modules.mjs'), moduleURL('preset.mjs'), binds)
        patches.push(write(path.join(o.dir, `${p.session}.patch.yml`), text))
      } catch (e) {
        throw new RemoteError('failed', `preset ${preset.id}: ${e.message}`)
      }
      return { patches, preset: true }
    },
  }
}

/** The file: URL of one of this plugin's modules for a bound preset (prepare/). */
const moduleURL = (name) => new URL(`./prepare/${name}`, import.meta.url).href

/**
 * Files a session that began in a dsh process the Iridium started here under the workspace of its
 * folder cwd in this dsh (DSH Desktop's sidebar), made if there is none.
 */
export async function fileSession(ctx, session, cwd) {
  if (!path.isAbsolute(cwd)) throw new Error(`no absolute folder for ${session}`)
  const reg = ctx.get('workspaceRegistry')
  if (!reg) return
  const w = (await reg.resolveByPath(cwd)) ?? (await reg.create(cwd))
  await w.attachSession(session)
}

/**
 * The patch that loads session.js in a session's dsh process, connected to bridge, and the
 * session part each of bundles names (its package.json's iridium.session: a module of it the
 * plugin in that process is).
 */
export function sessionPatch(bridge, bundles = []) {
  const q = (v) => JSON.stringify(v)
  const out = [
    "# dsh-remote-iridium: its part for the Iridium's sessions here, and the profile's bundles'.",
    '# Generated; do not edit.',
    '- insert:',
    '    - id: remote-iridium-session',
    `      name: ${q(new URL('./session.js', import.meta.url).href)}`,
    '      config:',
    `        endpoint: ${q(bridge.endpoint)}`,
    `        secret: ${q(bridge.secret)}`,
  ]
  for (const b of bundles) {
    if (!b.session) continue
    out.push(`    - id: ${q(`remote-iridium-session-${b.name.replace(/[^A-Za-z0-9_-]+/g, '-')}`)}`, `      name: ${q(b.session)}`)
  }
  return out.join('\n') + '\n'
}

/** This plugin's package name. */
const own = JSON.parse(fs.readFileSync(new URL('./package.json', import.meta.url), 'utf8')).name

/**
 * The bundles of the profile at dir besides dsh's own (@deepseek-ai/) and this plugin, by its
 * order: {name, version, patches, session?}, patches the text of the files its dsh.bundle.patch
 * lists, session the file: URL of its part for a dsh process the Iridium starts (package.json's
 * iridium.session). A bundle not installed in the profile's node_modules is left out.
 */
export function profileBundles(dir) {
  const json = (f) => JSON.parse(fs.readFileSync(f, 'utf8'))
  const out = []
  for (const name of json(path.join(dir, 'package.json')).dsh?.profile?.bundles ?? []) {
    if (typeof name !== 'string' || name.startsWith('@deepseek-ai/') || name === own) continue
    const pkg = path.join(dir, 'node_modules', ...name.split('/'))
    if (!fs.existsSync(path.join(pkg, 'package.json'))) continue
    const m = json(path.join(pkg, 'package.json'))
    const files = [m.dsh?.bundle?.patch ?? []].flat().filter((f) => typeof f === 'string')
    const patches = files.map((f) => {
      const file = path.resolve(pkg, f)
      if (path.relative(pkg, file).startsWith('..')) throw new Error(`${name}: patch ${f} is outside the package`)
      return fs.readFileSync(file, 'utf8')
    })
    let session
    if (typeof m.iridium?.session === 'string') {
      const file = path.resolve(pkg, m.iridium.session)
      if (path.relative(pkg, file).startsWith('..')) throw new Error(`${name}: iridium.session is outside the package`)
      session = pathToFileURL(file).href
    }
    out.push({ name, ...(m.version ? { version: m.version } : {}), patches, ...(session ? { session } : {}) })
  }
  return out
}

/** The ids of the rows dsh's base bundle has, which an sdk profile has too. */
function baseIDs(scope) {
  const text = fs.readFileSync(path.join(scope, 'dsh-base', 'cordis.patch.yml'), 'utf8')
  return new Set([...text.matchAll(/^\s*-? ?id: ([A-Za-z0-9_-]+)\s*$/gm)].map((m) => m[1]))
}

/** Whether a row carries what models a dsh offers: an llm adapter, or the default model. */
const isLLM = (name) => typeof name === 'string' && (name.startsWith('@deepseek-ai/dsh-llm') || name === '@deepseek-ai/dsh-agent-default-model')

/**
 * The patch that gives an sdk profile this dsh's models: each llm row of the base, configured (and
 * on or off) as here. Values in YAML's flow form, a `!!js` expression kept as one.
 */
export function llmPatch(loader, base) {
  const out = ['# dsh-remote-iridium: the models and providers of this dsh, for the Iridium\'s sessions here.', '# Generated; do not edit.']
  for (const e of loader.entries()) {
    const o = e.options ?? {}
    if (!isLLM(o.name) || !base.has(o.id) || (o.config === undefined && (o.disabled === undefined || o.disabled === null))) continue
    out.push(`- id: ${o.id}`)
    if (o.disabled !== undefined && o.disabled !== null) out.push(`  disabled: ${flow(o.disabled)}`)
    if (o.config !== undefined) out.push(`  config: ${flow(o.config)}`)
  }
  return out.length > 2 ? out.join('\n') + '\n' : out.join('\n') + '\n[]\n'
}

/** v in YAML's flow form: JSON, but for the loader's `!!js` expressions ({__jsExpr}). */
function flow(v) {
  if (v && typeof v === 'object' && typeof v.__jsExpr === 'string') return `!!js ${JSON.stringify(v.__jsExpr)}`
  if (Array.isArray(v)) return `[${v.map(flow).join(', ')}]`
  if (v && typeof v === 'object') return `{${Object.entries(v).filter(([, x]) => x !== undefined && typeof x !== 'function').map(([k, x]) => `${JSON.stringify(k)}: ${flow(x)}`).join(', ')}}`
  return v === undefined ? 'null' : JSON.stringify(v)
}

