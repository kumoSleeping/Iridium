// Requests about the dsh this plugin runs in, beside the executor's own. Apart from index.js so
// that it loads without dsh's packages (the tests' run.mjs).
import fs from 'node:fs'
import path from 'node:path'
import { RemoteError } from './executor.js'
import { heldMethods } from './held.js'

/**
 * What the dsh this plugin runs in knows, for the Iridium (its docs/exec.md "远端 DSH"): the
 * sessions it holds (held.js: held.list, held.send, held.get, held.set, and dsh.open, dsh.prompt
 * by their older names), with kept() what this plugin's settings keep from it (unsynced, the
 * workspaces, by folder or name; ungrouped false, the sessions in none of them) and o.features,
 * o.follow the feature registry and followHeld's (none: no features); dsh.attach {session, cwd},
 * the session filed under the workspace of its folder, made if there is none (a session the
 * Iridium started here, in DSH Desktop's sidebar); dsh.presets, the agent presets it registers;
 * dsh.bundles, its profile's bundles of other packages (profileBundles); dsh.llm, its models for
 * the sessions the Iridium starts here; dsh.session, this plugin's part in them (session.js).
 */
export function dshMethods(ctx, kept = () => ({ unsynced: [], ungrouped: true }), llm = {}, o = {}) {
  return {
    ...heldMethods(ctx, o.features, kept, o.follow, o.bridge),
    // The patch that puts this plugin's part for a session (session.js) into the dsh processes the
    // Iridium starts here, connected to the bridge (o.bridge, at o.sessionFile): their features
    // (a plugin's in that process, OMD's) answer the Iridium as Desktop's do. The secret stays here.
    'dsh.session': async () => {
      if (!o.bridge || !o.sessionFile) throw new RemoteError('unsupported', 'dsh.session')
      try {
        fs.mkdirSync(path.dirname(o.sessionFile), { recursive: true, mode: 0o700 })
        fs.writeFileSync(o.sessionFile, sessionPatch(o.bridge), { mode: 0o600 })
        return { patch: o.sessionFile }
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
    },
    // The agent presets this dsh registers (its agentPresets registry: the shipped ones, a
    // bundle's, the profile's own edits), each with its child plugin rows as YAML, which the
    // Iridium runs process-wide through a patch; and the default, the one set in its settings.
    'dsh.presets': async () => {
      const reg = ctx.get('agentPresets')
      if (!reg) throw new RemoteError('unsupported', 'no agent preset registry in this dsh')
      try {
        const presets = []
        for (const p of await reg.list()) {
          const doc = await reg.readDocument(p.id)
          presets.push({
            id: p.id,
            ...(p.name ? { name: p.name } : {}),
            ...(p.description ? { description: p.description } : {}),
            plugins: doc?.content ?? '',
            ...(p.broken ? { broken: String(p.broken) } : {}),
          })
        }
        return { presets, default: reg.defaultId ?? '' }
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
    },
    // The bundles of the profile this dsh runs (its package.json's dsh.profile.bundles) besides
    // dsh's own and this plugin, each with its patch files' text: a preset of theirs (OMD's) needs
    // their layer in the Iridium's sessions, whose sdk profile has none of this one's bundles.
    'dsh.bundles': async () => {
      const dir = ctx.get('profileContext')?.dir
      if (!dir) throw new RemoteError('unsupported', 'no profile in this dsh')
      try {
        return { bundles: profileBundles(dir) }
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
    },
    // This dsh's models and providers as its settings have them (the llm adapters' rows and the
    // default model), for the Iridium's sessions here, whose sdk profile has none of this
    // profile's settings: written as a patch beside this plugin's state, which they boot with. The
    // Iridium gets its path only: keys in it stay here (the executor serves it to no one).
    'dsh.llm': async () => {
      if (!llm.file || !llm.scope) throw new RemoteError('unsupported', 'dsh.llm')
      const loader = ctx.get('loader')
      if (!loader?.entries) throw new RemoteError('unsupported', 'no loader in this dsh')
      try {
        fs.mkdirSync(path.dirname(llm.file), { recursive: true, mode: 0o700 })
        fs.writeFileSync(llm.file, llmPatch(loader, baseIDs(llm.scope)), { mode: 0o600 })
        return { patch: llm.file }
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
    },
    'dsh.attach': async (p) => {
      if (typeof p.session !== 'string' || !p.session || typeof p.cwd !== 'string' || !path.isAbsolute(p.cwd))
        throw new RemoteError('bad_request', 'dsh.attach: session and an absolute cwd')
      const reg = ctx.get('workspaceRegistry')
      if (!reg) throw new RemoteError('unsupported', 'no workspace registry in this dsh')
      try {
        const w = (await reg.resolveByPath(p.cwd)) ?? (await reg.create(p.cwd))
        await w.attachSession(p.session)
        return { workspace: w.id }
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
    },
  }
}

/** The patch that loads session.js in a session's dsh process, connected to bridge. */
export function sessionPatch(bridge) {
  const q = (v) => JSON.stringify(v)
  return [
    "# dsh-remote-iridium: its part for the Iridium's sessions here (their features).",
    '# Generated; do not edit.',
    '- insert:',
    '    - id: remote-iridium-session',
    `      name: ${q(new URL('./session.js', import.meta.url).href)}`,
    '      config:',
    `        endpoint: ${q(bridge.endpoint)}`,
    `        secret: ${q(bridge.secret)}`,
  ].join('\n') + '\n'
}

/** This plugin's package name. */
const own = JSON.parse(fs.readFileSync(new URL('./package.json', import.meta.url), 'utf8')).name

/**
 * The bundles of the profile at dir besides dsh's own (@deepseek-ai/) and this plugin, by its
 * order: {name, version, patches}, patches the text of the files its dsh.bundle.patch lists. A
 * bundle not installed in the profile's node_modules (dsh's installation has it) is left out.
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
    out.push({ name, ...(m.version ? { version: m.version } : {}), patches })
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

