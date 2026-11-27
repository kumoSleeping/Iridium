import assert from 'node:assert/strict'
import test from 'node:test'
import { createDesktop, createFreeze, modelsOf, workspacesOf } from '../desktop.js'

function fake() {
  const handlers = {}
  const agents = new Map()
  const ws = [{ id: 'w1', title: 'proj', path: '/p', sessionIds: ['s1'], updatedAt: '2026-11-27T00:00:00.000Z' }]
  const services = {
    llm: {
      listProviders: () => [{ id: 'deepseek', name: 'DeepSeek' }],
      listModels: async () => [{ id: 'v4', name: 'V4' }, { id: 'chat' }],
      resolveModelInfo: async (_p, m) => (m === 'v4' ? { reasoning: { efforts: [{ id: 'high', name: 'High' }, { id: 'max', name: 'max' }] } } : {}),
    },
    agentDefaultModel: { currentSelection: () => ({ provider: 'deepseek', model: 'v4' }) },
    workspaceRegistry: { list: () => ws, archivedSessionIds: ['s9'] },
    agents: { get: (id) => agents.get(id), list: () => [...agents.values()] },
    sessionController: {
      listState: { summaryFor: (s) => ({ sessionId: s.id, updatedAt: 5, projections: { values: { title: `t ${s.id}` } } }) },
      list: async () => ({ items: [{ sessionId: 'old', cwd: '/o', updatedAt: 1, projections: { values: { title: 'Old one' } } }] }),
      search: async ({ query }) => ({ items: query === 'old' ? [{ sessionId: 'old', cwd: '/o', updatedAt: 1 }] : [] }),
    },
  }
  const ctx = { get: (n) => services[n], on: (n, fn) => ((handlers[n] ??= []).push(fn), () => {}), effect: () => {} }
  const fire = (n, ...a) => (handlers[n] ?? []).forEach((fn) => fn(...a))
  const agent = (id, status = 'idle', header = {}) => agents.set(id, { status, session: { id, header: { id, cwd: '/p', ...header } } })
  return { ctx, services, fire, agent, agents }
}

test('models, as the Iridium has them', async () => {
  const { ctx } = fake()
  assert.deepEqual(await modelsOf(ctx), {
    default: 'deepseek/v4',
    models: [
      { value: 'deepseek/v4', group: 'DeepSeek', name: 'V4', efforts: ['high', 'max'], effortNames: { high: 'High' }, default: true },
      { value: 'deepseek/chat', group: 'DeepSeek', noEffort: true },
    ],
  })
})

test('workspaces, and the sessions open told of as dsh says they change', async () => {
  const f = fake()
  assert.deepEqual(workspacesOf(f.ctx), { workspaces: [{ id: 'w1', title: 'proj', path: '/p', sessions: ['s1'], updatedAt: Date.parse('2026-11-27T00:00:00.000Z') }], archived: ['s9'] })
  const sent = []
  let sync = true
  const d = createDesktop(f.ctx, (e, p) => sent.push([e, p]), { sync: () => sync, setSync: (v) => (sync = !!v), gather: 0 })
  f.agent('s1', 'running')
  f.agent('sub', 'idle', { origin: 'subagent', parentSession: 's1' })
  f.fire('agent/status', {})
  f.fire('agent/created', {}) // gathered: one event
  await new Promise((r) => setTimeout(r, 20))
  const ev = sent.filter(([e]) => e === 'desktop.sessions.changed')
  assert.equal(ev.length, 1)
  assert.deepEqual(ev[0][1], {
    full: true,
    sessions: [
      { id: 's1', cwd: '/p', title: 't s1', last: 5, updatedAt: 5, open: true, running: true, workspaceId: 'w1' },
      { id: 'sub', cwd: '/p', title: 't sub', last: 5, updatedAt: 5, sub: true, parent: 's1', open: true, ended: true },
    ],
  })
  await d.tellAll()
  assert.deepEqual(sent.slice(-3).map(([e]) => e), ['desktop.config', 'desktop.workspaces.changed', 'desktop.sessions.changed'])
  assert.deepEqual(sent.find(([e]) => e === 'desktop.config')[1], { syncNewSessions: true })
  // On demand: a search, or all.
  assert.deepEqual((await d.methods['desktop.sessions']({ query: 'old' })).sessions, [{ id: 'old', cwd: '/o', last: 1, updatedAt: 1 }])
  assert.equal((await d.methods['desktop.sessions']({})).sessions[0].title, 'Old one')
  assert.deepEqual(await d.methods['desktop.config.set']({ syncNewSessions: false }), { syncNewSessions: false })
  assert.equal(sync, false)
  assert.deepEqual(sent.at(-1), ['desktop.config', { syncNewSessions: false }])
  await assert.rejects(d.methods['desktop.freeze'](), /not a windowless/)
})

test("the hand-over: stores written no more, no session made, the process ends once no turn runs", async () => {
  const f = fake()
  const unit = { publish: async () => 'wrote', writeDocument: async () => 'wrote' }
  f.services['storage.backend.json'] = { open: new Map([['workspace', unit]]) }
  f.agent('s1', 'running')
  let refused = false
  let exited = null
  const freeze = createFreeze(f.ctx, { refuse: () => (refused = true), exit: (c) => (exited = c) })
  assert.deepEqual(await freeze(), { frozen: true, running: 1 })
  assert.equal(refused, true)
  assert.equal(await unit.publish(), undefined)
  assert.equal(await unit.writeDocument(), undefined)
  await new Promise((r) => setImmediate(r))
  assert.equal(exited, null) // a turn runs
  f.agents.get('s1').status = 'idle'
  f.fire('agent/status', {})
  await new Promise((r) => setImmediate(() => setImmediate(r)))
  assert.equal(exited, 0)
})

test('the keys a new session may start with, in desktop.config, told again as plugins change', async () => {
  const f = fake()
  const sent = []
  let keys = [{ place: 'effort', from: '', content: { en: '<button id="omd.ultra">Ultra</button>' }, presets: ['omd'] }]
  const d = createDesktop(f.ctx, (e, p) => sent.push([e, p]), { sync: () => false, keys: async () => keys, gather: 0 })
  await d.tellAll()
  assert.deepEqual(sent[0], ['desktop.config', { syncNewSessions: false, keys }])
  assert.deepEqual(sent.slice(0, 3).map(([e]) => e), ['desktop.config', 'desktop.workspaces.changed', 'desktop.sessions.changed'])
  keys = []
  f.fire('loader/config-update')
  f.fire('plugin-manager/changed', {}) // gathered: one event
  await new Promise((r) => setTimeout(r, 20))
  const configs = sent.filter(([e]) => e === 'desktop.config')
  assert.equal(configs.length, 2)
  assert.deepEqual(configs[1][1], { syncNewSessions: false, keys: [] })
  // One that fails: none.
  const d2 = createDesktop(f.ctx, () => {}, { keys: async () => { throw new Error('no') } })
  assert.deepEqual(await d2.methods['desktop.config.set']({}), { syncNewSessions: false, keys: [] })
})
