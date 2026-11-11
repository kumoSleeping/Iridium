// The bridge between the plugin in DSH Desktop and its part in the dsh processes the Iridium starts
// (bridge.js, session.js): node --test test/
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { bridgeEndpoint, createBridge } from '../bridge.js'
import { createAttach } from '../attach.js'
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

test("a session's process: its sessions, what it attaches, a button back, gone with it", async () => {
  const started = []
  const sent = []
  const attach = createAttach({ send: (p) => sent.push(p), toProcess: (s, id, user) => bridge.button(s, id, user) })
  const bridge = createBridge({
    endpoint: bridgeEndpoint(tmp()),
    started: (s, cwd) => started.push([s, cwd]),
    attach: (s, place, content, owner) => attach.put(s, place, content, owner),
    gone: (owner) => attach.drop(owner),
  })
  const services = { workingDirectory: { get: () => '/w' } }
  const on = {}
  const effects = []
  const ctx = {
    provide: (name, v) => (services[name] = v),
    get: (name) => services[name],
    on: (name, fn) => (on[name] = fn),
    effect: (fn) => effects.push(fn()),
    plugin: () => {}, // OMD's Ultra (omd.js): no OMD here
  }
  session.apply(ctx, { endpoint: bridge.endpoint, secret: bridge.secret })
  const pressed = []
  services.iridium.onButton((s, id, user) => pressed.push([s, id, user?.id]))
  on['agent/created']({ agent: { id: 's1', session: {} } })
  await until(() => bridge.has('s1'))
  await until(() => started.length === 1)
  assert.deepEqual(started, [['s1', '/w']])
  services.iridium.attach('s1', 'effort', { zh: '<button id="a">甲</button>', en: '<button id="a">A</button>' })
  await until(() => sent.length === 1)
  assert.deepEqual(sent[0], { session: 's1', place: 'effort', content: { zh: '<button id="a">甲</button>', en: '<button id="a">A</button>' } })
  // The same again sends nothing; resend sends all.
  services.iridium.attach('s1', 'effort', { zh: '<button id="a">甲</button>', en: '<button id="a">A</button>' })
  attach.resend()
  await until(() => sent.length === 2)
  attach.button('s1', 'a', { id: 'u', name: 'U' })
  await until(() => pressed.length === 1)
  assert.deepEqual(pressed, [['s1', 'a', 'u']])
  // The process gone: what it attached is taken away.
  for (const stop of effects) stop?.()
  await until(() => sent.length === 3)
  assert.deepEqual(sent[2], { session: 's1', place: 'effort', content: null })
  assert.deepEqual(attach.attached(), [])
  bridge.close()
})

test('attach: places and contents checked, its own listeners told of a button', () => {
  const sent = []
  const a = createAttach({ send: (p) => sent.push(p) })
  assert.throws(() => a.service.attach('s', 'Bad Place', 'x'))
  assert.throws(() => a.service.attach('s', 'effort', 'x'.repeat(5000)))
  assert.throws(() => a.service.attach('s', 'effort', 3))
  a.service.attach('s', 'effort', '')
  assert.deepEqual(sent, [])
  a.service.attach('s', 'effort', { zh: '', en: 'x' })
  assert.deepEqual(sent, [{ session: 's', place: 'effort', content: { en: 'x' } }])
  const got = []
  const off = a.service.onButton((s, id) => got.push(id))
  a.button('s', 'b1', null)
  off()
  a.button('s', 'b2', null)
  assert.deepEqual(got, ['b1'])
  a.service.attach('s', 'effort', null)
  assert.deepEqual(sent.at(-1), { session: 's', place: 'effort', content: null })
})

test("the session controller of a process without dsh's: a model selected on the session's agent", async () => {
  const picked = []
  const agent = { model: { select: (s) => picked.push(s) } }
  const c = session.sessionController({ get: (n) => ({ agents: { get: (id) => (id === 's1' ? agent : undefined) } })[n] })
  assert.deepEqual(await c.selectModel({ sessionId: 's1', provider: 'p', model: 'm', reasoningEffort: 'xhigh' }), { selected: { provider: 'p', model: 'm', reasoningEffort: 'xhigh' } })
  assert.deepEqual(picked, [{ provider: 'p', model: 'm', reasoningEffort: 'xhigh' }])
  await assert.rejects(c.selectModel({ sessionId: 's2', provider: 'p', model: 'm' }), { code: 'not_found' })
})
