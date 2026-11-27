import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { readLocal, stateDir, watchLocal } from '../local.js'

test("Iridium's state directory, as Iridium has it", () => {
  assert.equal(stateDir({ IRIDIUM_STATE: '/x' }, 'linux', '/h'), '/x')
  assert.equal(stateDir({ XDG_STATE_HOME: '/s' }, 'linux', '/h'), path.join('/s', 'iridium'))
  assert.equal(stateDir({}, 'linux', '/h'), path.join('/h', '.local', 'state', 'iridium'))
  assert.equal(stateDir({ LOCALAPPDATA: 'C:\\L' }, 'win32', '/h'), path.join('C:\\L', 'Iridium', 'state'))
  assert.equal(stateDir({ XDG_STATE_HOME: '/s', LOCALAPPDATA: 'C:\\L' }, 'win32', '/h'), path.join('/s', 'iridium'))
})

test('a local Iridium only on loopback, with a local TOKEN, while it runs', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'local-'))
  const put = (v) => fs.writeFileSync(path.join(dir, 'local-exec.json'), JSON.stringify(v))
  assert.equal(readLocal(dir), null)
  put({ ws: 'ws://127.0.0.1:7810/v1/internal/exec', token: 'irl_x', pid: process.pid })
  assert.deepEqual(readLocal(dir), { ws: 'ws://127.0.0.1:7810/v1/internal/exec', token: 'irl_x', pid: process.pid })
  put({ ws: 'ws://10.0.0.2:7810/v1/internal/exec', token: 'irl_x', pid: process.pid })
  assert.equal(readLocal(dir), null)
  put({ ws: 'ws://127.0.0.1:7810/v1/internal/exec', token: 'ir_x', pid: process.pid })
  assert.equal(readLocal(dir), null)
  put({ ws: 'ws://[::1]:7810/v1/internal/exec', token: 'irl_x', pid: 2 ** 31 - 2 })
  assert.equal(readLocal(dir), null) // its process gone
})

test('followed: on as one appears or another takes its place, off as it goes', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'local-'))
  const file = path.join(dir, 'local-exec.json')
  const seen = []
  const stop = watchLocal(dir, (v) => seen.push(v.token), () => seen.push('off'), { every: 50 })
  const wait = (n) => new Promise((ok, no) => { const end = Date.now() + 3000; const t = setInterval(() => (seen.length >= n ? (clearInterval(t), ok()) : Date.now() > end && (clearInterval(t), no(new Error(`seen ${seen}`)))), 10) })
  // None at first: nothing said.
  fs.writeFileSync(file, JSON.stringify({ ws: 'ws://127.0.0.1:1/v1/internal/exec', token: 'irl_a', pid: process.pid }))
  await wait(1)
  fs.writeFileSync(file, JSON.stringify({ ws: 'ws://127.0.0.1:1/v1/internal/exec', token: 'irl_b', pid: process.pid }))
  await wait(2)
  fs.rmSync(file)
  await wait(3)
  stop()
  assert.deepEqual(seen, ['irl_a', 'irl_b', 'off'])
})
