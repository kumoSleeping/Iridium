import assert from 'node:assert/strict'
import test from 'node:test'
import { desktopServer, inprocArgs, race } from '../inproc.js'

const later = (ms, v, fail) => new Promise((ok, no) => setTimeout(() => (fail ? no(v) : ok(v)), ms))

test("a hold's argv", () => {
  assert.deepEqual(inprocArgs(['@dsh-desktop', '--preset', 'omd', '--system', '/s.md', '--persona', 'own', '--keep-model']), { preset: 'omd', system: '/s.md', persona: 'own', keepModel: true })
  assert.deepEqual(inprocArgs(['@dsh-desktop']), {})
  assert.deepEqual(inprocArgs(['@dsh-desktop', '--preset', 'omd', '--attach', 'omd.ultra.workflow']), { preset: 'omd', attach: 'omd.ultra.workflow' })
  assert.deepEqual(inprocArgs(['@dsh-desktop', '--attach']), {})
})

/** dsh's SDK server as far as a hold's createSession uses it, over a fake Desktop. */
function server(o) {
  const steps = []
  const agents = new Map()
  const agent = (id) => ({
    session: { id },
    status: 'idle',
    model: { current: () => ({ provider: 'deepseek', model: 'old' }), select: (s) => steps.push(['route', s.model]) },
  })
  const ctx = {
    agents,
    get: (n) =>
      n === 'sessionController'
        ? {
            create: async ({ sessionId }) => (steps.push(['create', sessionId]), agents.set(sessionId, agent(sessionId)), { sessionId }),
            resolveAgent: async (id) => (agents.set(id, agent(id)), { agent: agents.get(id) }),
          }
        : undefined,
  }
  class Base {
    constructor(c) {
      Object.assign(this, { ctx: c, disposers: [() => {}, () => {}], userQuestions: { unregister() {} }, sessions: new Map(), cwd: '/p', provider: 'deepseek', model: 'v4' })
    }
  }
  const S = desktopServer(Base)
  return { s: new S(ctx, { request() {}, notify() {} }, o), steps }
}

test('a new session made with its start key, after its route, before its first turn; never on resume', async () => {
  const logs = []
  const { s, steps } = server({ startKey: async (agent) => steps.push(['key', agent.session.id]), log: (m) => logs.push(m) })
  await s.createSession('n1', false)
  assert.deepEqual(steps, [['create', 'n1'], ['route', 'v4'], ['key', 'n1']])
  await s.createSession('r1', true)
  assert.deepEqual(steps.at(-1), ['route', 'v4'])
  // A key that fails: logged, the session made all the same.
  const f = server({ startKey: async () => Promise.reject(new Error('no OMD preset')), log: (m) => logs.push(m) })
  assert.ok((await f.s.createSession('n2', false)).handle.agent)
  assert.deepEqual(logs, ['start key: no OMD preset'])
})

test("the Iridium answers first: its answer, Desktop's card withdrawn", async () => {
  const req = { signal: new AbortController().signal }
  let deskSignal
  const r = await race(req, () => ((deskSignal = req.signal), new Promise(() => {})), () => later(10, 'allowed-once'))
  assert.equal(r, 'allowed-once')
  assert.equal(deskSignal.aborted, true)
})

test("Desktop answers first: its answer, the Iridium's ask cancelled", async () => {
  const req = {}
  let irSignal
  const r = await race(req, () => later(10, 'rejected'), (s) => ((irSignal = s), new Promise(() => {})))
  assert.equal(r, 'rejected')
  assert.equal(irSignal.aborted, true)
  assert.equal(req.signal.aborted, false)
})

test('no window of Desktopen (unavailable, NO_PROVIDER): the Iridium is waited for', async () => {
  assert.equal(await race({}, () => 'unavailable', () => later(20, 'allowed-once')), 'allowed-once')
  const noProvider = Object.assign(new Error('none'), { code: 'NO_PROVIDER' })
  assert.deepEqual(await race({}, () => Promise.reject(noProvider), () => later(20, { answers: [] })), { answers: [] })
})

test("neither answers: Desktop's own end; the Iridium gone: Desktop alone", async () => {
  assert.equal(await race({}, () => 'unavailable', () => Promise.reject(new Error('transport closed'))), 'unavailable')
  assert.equal(await race({}, () => later(20, 'allowed-once'), () => Promise.reject(new Error('transport closed'))), 'allowed-once')
})

test('the turn cancelled: both sides end with it', async () => {
  const ac = new AbortController()
  const req = { signal: ac.signal }
  const p = race(
    req,
    () => new Promise((_, no) => req.signal.addEventListener('abort', () => no(new Error('aborted')))),
    (s) => new Promise((_, no) => s.addEventListener('abort', () => no(new Error('cancelled')))),
  )
  setTimeout(() => ac.abort(), 10)
  await assert.rejects(p)
})
