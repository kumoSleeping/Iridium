import assert from 'node:assert/strict'
import test from 'node:test'
import { inprocArgs, race } from '../inproc.js'

const later = (ms, v, fail) => new Promise((ok, no) => setTimeout(() => (fail ? no(v) : ok(v)), ms))

test("a hold's argv", () => {
  assert.deepEqual(inprocArgs(['@dsh-desktop', '--preset', 'omd', '--system', '/s.md', '--persona', 'own', '--keep-model']), { preset: 'omd', system: '/s.md', persona: 'own', keepModel: true })
  assert.deepEqual(inprocArgs(['@dsh-desktop']), {})
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
