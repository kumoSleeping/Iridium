// The bridge between the plugin in DSH Desktop and its part in the dsh processes the Iridium starts
// (bridge.js, session.js): node --test test/
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { bridgeEndpoint, createBridge } from '../bridge.js'
import { createFeatures } from '../features.js'
import * as session from '../session.js'

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), 'bridge-'))
const until = async (ok) => {
  for (let i = 0; i < 200 && !ok(); i++) await new Promise((r) => setTimeout(r, 10))
  assert.ok(ok())
}

test('each plugin its own socket: one that closes leaves the next one\'s, a dead one is swept', { skip: process.platform === 'win32' }, async () => {
  const dir = tmp()
  const dead = path.join(dir, 'bridge-0123.sock')
  fs.writeFileSync(dead, '')
  const a = createBridge({ endpoint: bridgeEndpoint(dir) })
  await until(() => fs.existsSync(a.endpoint) && !fs.existsSync(dead))
  const b = createBridge({ endpoint: bridgeEndpoint(dir) })
  await until(() => fs.existsSync(b.endpoint))
  assert.notEqual(a.endpoint, b.endpoint)
  a.close()
  await until(() => !fs.existsSync(a.endpoint))
  assert.ok(fs.existsSync(b.endpoint))
  b.close()
})

test("a session's process: its sessions, its features answered through the bridge", async () => {
  const changed = []
  const bridge = createBridge({ endpoint: bridgeEndpoint(tmp()), changed: (s) => changed.push(s) })
  const services = {}
  const on = {}
  const effects = []
  const ctx = {
    provide: (name, v) => (services[name] = v),
    get: (name) => services[name],
    on: (name, fn) => (on[name] = fn),
    effect: (fn) => effects.push(fn()),
    plugin: () => {},
  }
  session.apply(ctx, { endpoint: bridge.endpoint, secret: bridge.secret })
  let mode = 'off'
  services.iridiumFeatures.provide({
    id: 'p.mode',
    get: async () => ({ name: 'Mode', on: mode === 'on', available: true }),
    set: async (_, { on }) => void (mode = on ? 'on' : 'off'),
    ownsEffort: async () => mode === 'on',
    effort: async () => 'max',
    setEffort: async () => {},
  })
  on['agent/created']({ agent: { id: 's1' } })
  await until(() => bridge.has('s1'))
  assert.ok(changed.includes('s1'))
  assert.deepEqual((await bridge.call('s1', 'live', { lang: 'en' })).features.map((f) => [f.id, f.on, f.available]), [['p.mode', false, true]])
  await assert.rejects(bridge.call('s1', 'setEffort', { level: 'high' }), { code: 'unsupported' })
  await bridge.call('s1', 'set', { id: 'p.mode', on: true })
  const live = await bridge.call('s1', 'live', {})
  assert.equal(live.owner, 'p.mode')
  assert.equal(live.effort, 'max')
  await bridge.call('s1', 'setEffort', { level: 'high' })
  on['agent/disposed']({ agent: { id: 's1' } })
  await until(() => !bridge.has('s1'))
  for (const stop of effects) stop?.()
  bridge.close()
})

test("the session controller of a process without dsh's: a model selected on the session's agent", async () => {
  const picked = []
  const agent = { model: { select: (s) => picked.push(s) } }
  const c = session.sessionController({ get: (n) => ({ agents: { get: (id) => (id === 's1' ? agent : undefined) } })[n] })
  assert.deepEqual(await c.selectModel({ sessionId: 's1', provider: 'p', model: 'm', reasoningEffort: 'xhigh' }), { selected: { provider: 'p', model: 'm', reasoningEffort: 'xhigh' } })
  assert.deepEqual(picked, [{ provider: 'p', model: 'm', reasoningEffort: 'xhigh' }])
  await assert.rejects(c.selectModel({ sessionId: 's2', provider: 'p', model: 'm' }), { code: 'not_found' })
})

test('createFeatures is what the process provides', () => {
  assert.equal(typeof createFeatures().offered, 'function')
})
