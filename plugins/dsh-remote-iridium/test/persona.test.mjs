// The personas of dsh's persona plugin for the Iridium (persona.js), over a fake of that plugin's
// modules and service: node --test test/
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { dshMethods } from '../dsh.js'
import { ownPersona, personaLib, personasOf, personaText } from '../persona.js'

/** A fake of the plugin's modules (OMD's plugins/persona/src/shared), as files in a profile. */
const PERSONALITY = `
export const PERSONALITY_FIELDS = [{ key: 'identity', label: '身份与人格' }, { key: 'style', label: '语言风格' }, { key: 'communication', label: '沟通方式' }, { key: 'presentation', label: '回答呈现' }]
const builtin = { overclock: { name: '超频', description: '冷静', modules: { identity: '你是超频。', style: '直接' } } }
export function personalityOptions(config) {
  return [{ value: 'custom', label: '自定义' }, ...Object.entries(builtin).map(([value, b]) => ({ value, label: b.name })), { value: 'off', label: '关闭人格' }]
}
export function personalityProfile(config, preset) {
  if (preset === 'custom') return { id: preset, name: '自定义', description: '', modules: { identity: config.identityPrompt } }
  return { id: preset, ...builtin[preset] }
}
export function resolvedPersonalityModules(config, preset) {
  return { communication: 'Keep updates brief.', presentation: '', ...personalityProfile(config, preset).modules }
}
`
const PROMPT = `
export function applyPersonality(assembly, context, service) {
  const { modules } = service.forSession(context.agent.session)
  if (!modules.identity) return assembly
  return { ...assembly, sections: [...assembly.sections, { name: 'dsh-persona:identity', text: modules.identity + '\\n\\nScope.', interpolate: false }] }
}
`
function profile(place = 'oh-my-dsh/plugins/persona/src/shared') {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'persona-'))
  const at = path.join(dir, 'node_modules', place)
  fs.mkdirSync(at, { recursive: true })
  fs.writeFileSync(path.join(at, 'personality.mjs'), PERSONALITY)
  fs.writeFileSync(path.join(at, 'prompt.mjs'), PROMPT)
  return dir
}

/** Desktop's persona service: its config; per session, its choice (none: the global one). */
const service = (config, chosen = {}) => ({
  config: () => config,
  forSession: (s) => ({ modules: chosen[s.id] ?? { identity: config.identityPrompt } }),
})
const ctxOf = (services) => ({ get: (n) => services[n] })

test('the personas, each whole, off left out', async () => {
  const lib = await personaLib(profile())
  assert.equal(lib.from, 'OMD')
  const got = personasOf(ctxOf({ dshPersona: service({ identityPrompt: 'You are kind.' }) }), lib)
  assert.deepEqual(got, [
    { id: 'custom', name: '自定义', text: 'You are kind.\n\n## 沟通方式\nKeep updates brief.', from: 'OMD' },
    { id: 'overclock', name: '超频', description: '冷静', text: '你是超频。\n\n## 语言风格\n直接\n\n## 沟通方式\nKeep updates brief.', from: 'OMD' },
  ])
  // The plugin on its own, not OMD's.
  assert.equal((await personaLib(profile('@gulagala001/dsh-persona-plugin/src/shared'))).from, 'Persona')
})

test('no persona plugin: nothing', async () => {
  assert.equal(await personaLib(fs.mkdtempSync(path.join(os.tmpdir(), 'none-'))), null)
  assert.equal(await personaLib(undefined), null)
  const lib = await personaLib(profile())
  assert.deepEqual(personasOf(ctxOf({}), lib), [])
  assert.deepEqual(personasOf(ctxOf({ dshPersona: service({}) }), null), [])
})

test("in Desktop's process: a session whose persona is the Iridium's gets none of the plugin's; the rest theirs", async () => {
  // As OMD 1.9 has it: forPrompt (its presets' main prompt) asks forSession, as its sections do.
  const service = { forSession: (s) => ({ preset: 'custom', modules: { identity: `own ${s.id}`, style: 'S' } }) }
  service.forPrompt = (s) => service.forSession(s)
  let effect
  const ctx = { inject: (deps, fn) => (assert.deepEqual(deps, ['dshPersona']), fn({ get: () => service, effect: (f) => (effect = f()) })) }
  const p = ownPersona(ctx)
  const off = { identity: '', style: '', communication: '', presentation: '' }
  const out = p.own('x')
  const again = p.own('x') // two holds of it
  assert.deepEqual(service.forPrompt({ id: 'x' }), { preset: 'custom', modules: off })
  assert.equal(service.forPrompt({ id: 'y' }).modules.identity, 'own y')
  out()
  assert.deepEqual(service.forSession({ id: 'x' }).modules, off)
  again()
  assert.equal(service.forSession({ id: 'x' }).modules.identity, 'own x')
  p.own('x')
  effect() // the plugin gone: its service as it was
  assert.equal(service.forSession({ id: 'x' }).modules.identity, 'own x')
})

test('agent.personas through the methods', async () => {
  const lib = await personaLib(profile())
  const ctx = ctxOf({ dshPersona: service({ identityPrompt: 'G.' }) })
  const m = dshMethods(ctx, { personaLib: async () => lib })
  assert.equal((await m['agent.personas']({ agent: 'dsh' })).personas.length, 2)
  const none = dshMethods(ctxOf({}), { personaLib: async () => null })
  assert.deepEqual(await none['agent.personas']({ agent: 'dsh' }), { personas: [] })
})

test('personaText', () => {
  assert.equal(personaText({ identity: ' A ', style: ' ', presentation: 'P' }, []), 'A\n\n## presentation\nP')
})
