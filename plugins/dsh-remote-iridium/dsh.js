// Requests about the dsh this plugin runs in, beside the executor's own: its agent presets and the
// personas of its persona plugin. Apart from index.js so that it loads without dsh's packages (the
// tests' run.mjs).
import { RemoteError } from './executor.js'
import { personaLib, personasOf } from './persona.js'

/** The agents it tells of. */
const AGENT = 'dsh'

/**
 * agent.presets, the agent presets this dsh registers; agent.personas, the personas of its persona
 * plugin (o.personaLib: that plugin's modules; else found in the profile it runs).
 */
export function dshMethods(ctx, o = {}) {
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
  const lib = () => (o.personaLib ? o.personaLib() : personaLib(ctx.get('profileContext')?.dir))
  const agentOf = (p, what) => {
    if (p?.agent !== AGENT) throw new RemoteError('unsupported', `${what}: no agent ${p?.agent} here`)
  }
  return {
    // The agent presets this dsh registers (its agentPresets registry: the shipped ones, a
    // bundle's, the profile's own edits), each with its child plugin rows as YAML; and the
    // default, the one set in its settings.
    'agent.presets': async (p) => {
      agentOf(p, 'agent.presets')
      return presets()
    },
    // The personas its persona plugin has (none: none), each whole, to take as a persona of the
    // Iridium's (persona.js).
    'agent.personas': async (p) => {
      agentOf(p, 'agent.personas')
      try {
        return { personas: personasOf(ctx, await lib()) }
      } catch (e) {
        throw new RemoteError('failed', `personas: ${e.message}`)
      }
    },
  }
}
