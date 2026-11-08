// Requests about the dsh this plugin runs in, beside the executor's own. Apart from index.js so
// that it loads without dsh's packages (the tests' run.mjs).
import fs from 'node:fs'
import path from 'node:path'
import { RemoteError } from './executor.js'

/**
 * What the dsh this plugin runs in knows, for the Iridium (its docs/exec.md "远端 DSH"):
 * dsh.open, the sessions open in this process now (DSH Desktop's: one the Iridium resumes is refused
 * while it is), and what this plugin's settings keep from it (kept(): unsynced, the workspaces,
 * by folder or name; ungrouped false, the sessions in none of them); dsh.attach {session, cwd}, the session filed under the workspace of its folder, made
 * if there is none (a session the Iridium started here, in DSH Desktop's sidebar); dsh.prompt
 * {session, text, steer}, a message for a session this dsh holds, given to its agent here;
 * dsh.presets, the agent presets it registers.
 */
export function dshMethods(ctx, kept = () => ({ unsynced: [], ungrouped: true }), llm = {}) {
  return {
    'dsh.open': async () => {
      const k = kept()
      return { sessions: (ctx.get('sessions')?.list() ?? []).map((s) => s.id), unsynced: k.unsynced, ...(k.ungrouped === false ? { unsyncedUngrouped: true } : {}) }
    },
    // A message for a session this dsh holds (open in DSH Desktop, its writer lock here): the
    // Iridium cannot resume it, so its agent here takes the message, as a next turn or (steer)
    // into the running one; not_found if no agent of it lives here.
    'dsh.prompt': async (p) => {
      if (typeof p.session !== 'string' || !p.session || typeof p.text !== 'string' || !p.text.trim())
        throw new RemoteError('bad_request', 'dsh.prompt: session and text')
      const a = ctx.get('agents')?.get(p.session)
      if (!a) throw new RemoteError('not_found', `no agent of ${p.session} here`)
      const input = { content: [{ type: 'text', text: p.text }], source: { kind: 'user' } } // yours, through the Iridium (its tag before the words)
      try {
        await (p.steer ? a.steer(input) : a.followup(input))
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
      return {}
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
