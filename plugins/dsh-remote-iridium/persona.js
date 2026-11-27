// Personas for the Iridium (its docs/exec.md "dsh 插件" 人格, docs/channels.md "角色"): the only
// file here that knows a persona plugin of dsh's (OMD's plugins/persona, or the same plugin on its
// own: @gulagala001/dsh-persona-plugin), through its own service (dshPersona) and its own modules.
// With none in this dsh, it gives nothing and changes nothing.
//
// In DSH Desktop's process: agent.personas, the personas Desktop has (each whole, its parts put
// together here: the Iridium does not know them); and the sessions the Iridium runs here with a
// persona of its own (in its system prompt), for which the plugin's gives way (ownPersona: its
// service answers for them with none, as its "关闭人格"). Every way the plugin puts a persona in a
// prompt asks it so: its sections at the prompt's end, and OMD's own presets, which write it into
// their head (1.9: oh-my-dsh:persona, through forPrompt, which asks forSession).

import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

/** Where its modules are under a profile's node_modules, and what the page calls each. */
const PLACES = [
  { dir: 'oh-my-dsh/plugins/persona/src/shared', from: 'OMD' },
  { dir: '@gulagala001/dsh-persona-plugin/src/shared', from: 'Persona' },
]

/** A persona of none: every part empty (the plugin's own "off"). */
const OFF = Object.freeze({ identity: '', style: '', communication: '', presentation: '' })

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
 * In DSH Desktop's process, the sessions whose persona is the Iridium's (inproc.js: --persona own):
 * the plugin's service answers for them with none, for every other session as it would.
 * own(session) puts one in; it returns the function that takes it out.
 */
export function ownPersona(ctx) {
  const ids = new Map() // session → how many holds said so
  ctx.inject(['dshPersona'], (ctx) => {
    const service = ctx.get('dshPersona')
    if (typeof service?.forSession !== 'function') return
    const was = service.forSession
    service.forSession = function (session) {
      const r = was.call(this, session)
      return ids.has(String(session?.id)) ? { ...r, modules: { ...OFF } } : r
    }
    ctx.effect(() => () => {
      service.forSession = was
    }, 'remote-iridium: the Iridium\'s personas')
  })
  return {
    own(session) {
      ids.set(session, (ids.get(session) ?? 0) + 1)
      return () => {
        const n = (ids.get(session) ?? 1) - 1
        if (n > 0) ids.set(session, n)
        else ids.delete(session)
      }
    },
  }
}
