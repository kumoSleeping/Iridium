// The personas of dsh's persona plugin for the Iridium (persona.js), over a fake of that plugin's
// modules and service: node --test test/
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { dshMethods } from '../dsh.js'
import { apply, personaFor, personaLib, personaPatch, personasOf, personaText, withPersona } from '../persona.js'

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
  assert.equal(personaFor(ctxOf({}), lib, 's', true), null)
  assert.equal(personaFor(ctxOf({ dshPersona: service({}) }), null, 's', false), null)
})

test("a session's persona: the Iridium's, else Desktop's for it", async () => {
  const lib = await personaLib(profile())
  const ctx = ctxOf({ dshPersona: service({ identityPrompt: 'Global.' }, { chosen: { identity: 'Mine.' } }) })
  assert.deepEqual(personaFor(ctx, lib, 's', true), { own: true })
  assert.deepEqual(personaFor(ctx, lib, 's', false), { sections: [{ name: 'dsh-persona:identity', text: 'Global.\n\nScope.' }] })
  assert.deepEqual(personaFor(ctx, lib, 'chosen', false), { sections: [{ name: 'dsh-persona:identity', text: 'Mine.\n\nScope.' }] })
  assert.match(personaPatch({ own: true }), /id: remote-iridium-persona\n {6}name: "file:.*persona\.js"\n {6}config: \{"own":true\}\n$/)
})

test("in the session's process: the plugin's sections out, its mark in, whichever runs first", async () => {
  const base = { sections: [{ name: 'harness', text: 'H' }] }
  const omd = { name: 'dsh-persona:identity', text: 'sdk store' }
  const mark = { name: 'dsh-persona:applied', text: '', interpolate: false }
  // Run inside the plugin's (it adds after): its mark makes it add nothing.
  assert.deepEqual(withPersona(base, { own: true }), { sections: [base.sections[0], mark] })
  // Run outside it (it has added): its sections taken out, Desktop's put in.
  const desk = { name: 'dsh-persona:identity', text: 'Desktop' }
  assert.deepEqual(withPersona({ sections: [...base.sections, omd] }, { sections: [desk] }), { sections: [base.sections[0], { ...desk, interpolate: false }, mark] })
  let hook
  apply({ on: (name, fn, o) => (assert.equal(name, 'system-prompt/assemble'), assert.ok(o.global), (hook = fn)) }, { own: true })
  assert.deepEqual(await hook(null, {}, async () => ({ sections: [omd] })), { sections: [mark] })
})

test('agent.personas and agent.prepare through the methods', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'prep-'))
  const lib = await personaLib(profile())
  const ctx = ctxOf({ dshPersona: service({ identityPrompt: 'G.' }) })
  const m = dshMethods(ctx, undefined, {}, { dir, personaLib: async () => lib })
  assert.equal((await m['agent.personas']({ agent: 'dsh' })).personas.length, 2)
  const own = await m['agent.prepare']({ agent: 'dsh', session: 'abc', persona: 'own' })
  assert.deepEqual(own.patches, [path.join(dir, 'abc.persona.patch.yml')])
  assert.match(fs.readFileSync(own.patches[0], 'utf8'), /config: \{"own":true\}/)
  const desk = await m['agent.prepare']({ agent: 'dsh', session: 'abc' })
  assert.match(fs.readFileSync(desk.patches[0], 'utf8'), /"sections":\[\{"name":"dsh-persona:identity","text":"G\.\\n\\nScope\."\}\]/)
  // No persona plugin: no patch, no personas.
  const none = dshMethods(ctxOf({}), undefined, {}, { dir, personaLib: async () => null })
  assert.deepEqual((await none['agent.prepare']({ agent: 'dsh', session: 'abc', persona: 'own' })).patches, [])
  assert.deepEqual(await none['agent.personas']({ agent: 'dsh' }), { personas: [] })
})

test('personaText', () => {
  assert.equal(personaText({ identity: ' A ', style: ' ', presentation: 'P' }, []), 'A\n\n## presentation\nP')
})
