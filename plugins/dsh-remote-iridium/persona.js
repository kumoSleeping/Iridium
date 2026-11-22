// Personas for the Iridium (its docs/exec.md "dsh 插件" 人格, docs/channels.md "角色"): the only
// file here that knows a persona plugin of dsh's (OMD's plugins/persona, or the same plugin on its
// own: @gulagala001/dsh-persona-plugin), through its own service (dshPersona) and its own modules.
// With none in this dsh, it gives nothing and changes nothing.
//
// In DSH Desktop's process: agent.personas, the personas Desktop has (each whole, its parts put
// together here: the Iridium does not know them); and, for a session the Iridium starts here, a
// patch (personaPatch) that loads this file in that process with what its persona is to be:
//   own: the session has a persona of the Iridium's (in its system prompt): the plugin's give way;
//   sections: Desktop's for this session as the plugin itself would add them (Desktop's choice for
//     it, else Desktop's global one), in place of the ones the plugin there reads from the sdk
//     profile's own store (none: no persona).
// In that process (apply): on each assembly of the system prompt, the plugin's sections out, these
// in, and its own mark that it applied, so it adds none (a section named dsh-persona:applied).

import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

export const name = 'remote-iridium-persona'

/** Where its modules are under a profile's node_modules, and what the page calls each. */
const PLACES = [
  { dir: 'oh-my-dsh/plugins/persona/src/shared', from: 'OMD' },
  { dir: '@gulagala001/dsh-persona-plugin/src/shared', from: 'Persona' },
]

/** The prefix of the plugin's sections, and its mark that it applied. */
const PREFIX = 'dsh-persona:'
const APPLIED = 'dsh-persona:applied'

/** The parts after the first, in their order: each under its own heading. */
const PARTS = ['style', 'communication', 'presentation']

/**
 * The persona plugin's modules in the profile at dir: {personality, prompt, from}; null if it has
 * none.
 */
export async function personaLib(dir) {
  if (!dir) return null
  for (const p of PLACES) {
    const at = path.join(dir, 'node_modules', p.dir)
    const personality = path.join(at, 'personality.mjs')
    const prompt = path.join(at, 'prompt.mjs')
    if (!fs.existsSync(personality) || !fs.existsSync(prompt)) continue
    try {
      return { personality: await import(pathToFileURL(personality).href), prompt: await import(pathToFileURL(prompt).href), from: p.from }
    } catch {
      return null
    }
  }
  return null
}

/** The whole of one persona: its identity, then each other part under its heading. */
export function personaText(modules, fields) {
  const label = (k) => fields?.find?.((f) => f.key === k)?.label ?? k
  const out = []
  if (modules?.identity?.trim()) out.push(modules.identity.trim())
  for (const k of PARTS) {
    const v = modules?.[k]?.trim()
    if (v) out.push(`## ${label(k)}\n${v}`)
  }
  return out.join('\n\n')
}

/**
 * The personas in ctx's dsh (Desktop's process), by the plugin's order, "off" left out; [] if it
 * has no persona plugin. lib: personaLib's, for the profile ctx runs.
 */
export function personasOf(ctx, lib) {
  const service = ctx.get('dshPersona')
  if (!service || !lib) return []
  const { personalityOptions, personalityProfile, resolvedPersonalityModules, PERSONALITY_FIELDS } = lib.personality
  const config = service.config()
  const out = []
  for (const o of personalityOptions(config)) {
    if (o.value === 'off') continue
    try {
      const p = personalityProfile(config, o.value)
      const text = personaText(resolvedPersonalityModules(config, o.value), PERSONALITY_FIELDS)
      if (!text) continue
      out.push({ id: o.value, name: p.name || o.label || o.value, ...(p.description ? { description: p.description } : {}), text, from: lib.from })
    } catch {}
  }
  return out
}

/**
 * What a session the Iridium starts here takes (agent.prepare): own, its persona is the Iridium's;
 * else Desktop's sections for it (its choice for session, else the global one), as the plugin
 * adds them. null: no persona plugin here, nothing to change.
 */
export function personaFor(ctx, lib, session, own) {
  const service = ctx.get('dshPersona')
  if (!service || !lib) return null
  if (own) return { own: true }
  const assembly = lib.prompt.applyPersonality({ sections: [] }, { agent: { session: { id: session, header: {} } } }, service)
  return { sections: (assembly?.sections ?? []).filter((s) => typeof s?.name === 'string' && typeof s.text === 'string').map((s) => ({ name: s.name, text: s.text })) }
}

/** The patch that loads this file in a session's dsh process with config (personaFor's). */
export function personaPatch(config) {
  const out = [
    "# dsh-remote-iridium: the persona of the Iridium's session here.",
    '# Generated; do not edit.',
    '- insert:',
    '    - id: remote-iridium-persona',
    `      name: ${JSON.stringify(new URL('./persona.js', import.meta.url).href)}`,
    `      config: ${JSON.stringify(config)}`,
  ]
  return out.join('\n') + '\n'
}

/** The system prompt's sections with config's persona in place of the plugin's. */
export function withPersona(assembly, config) {
  if (!assembly || !Array.isArray(assembly.sections)) return assembly
  const kept = assembly.sections.filter((s) => !(typeof s?.name === 'string' && s.name.startsWith(PREFIX)))
  const ours = config?.own ? [] : (config?.sections ?? []).map((s) => ({ name: s.name, text: s.text, interpolate: false }))
  return { ...assembly, sections: [...kept, ...ours, { name: APPLIED, text: '', interpolate: false }] }
}

/** In a session's dsh process: its persona as config says, on each assembly of its prompt. */
export function apply(ctx, config) {
  ctx.on('system-prompt/assemble', async (_initial, _context, next) => withPersona(await next(), config), { global: true })
}
