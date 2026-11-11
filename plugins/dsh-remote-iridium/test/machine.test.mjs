// This computer's ID in the hello (machine.js): node --test test/
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { Executor } from '../executor.js'
import { hashOf } from '../machine.js'

test('hashOf is Iridium machineid.Of', () => {
  assert.equal(hashOf('abc'), '607b29cab35d839d437a9a485b71b2fb')
  assert.equal(hashOf('  abc  '), '607b29cab35d839d437a9a485b71b2fb')
  assert.equal(hashOf(''), '')
  assert.equal(hashOf('none'), '')
  assert.equal(hashOf(undefined), '')
})

test('the hello says machineId when it has one', () => {
  const dirs = []
  const state = () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'machine-'))
    dirs.push(dir)
    return dir
  }
  try {
    assert.equal(new Executor({ state: state(), machineId: 'abc' }).hello().machineId, 'abc')
    assert.equal('machineId' in new Executor({ state: state() }).hello(), false)
  } finally {
    for (const dir of dirs) fs.rmSync(dir, { recursive: true, force: true })
  }
})
