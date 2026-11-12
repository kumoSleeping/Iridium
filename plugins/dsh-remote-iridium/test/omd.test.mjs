// OMD's Ultra on the Iridium's page (omd.js), over a fake OMD and a fake service iridium:
// node --test test/
import assert from 'node:assert/strict'
import test from 'node:test'
import * as omdIridium from '../omd.js'

const until = async (ok) => {
  for (let i = 0; i < 200 && !ok(); i++) await new Promise((r) => setTimeout(r, 5))
  assert.ok(ok())
}

/** A fake dsh with OMD: sessions by id ({eligible, mode, submode, teamAvailable, selected}). */
function fake(views) {
  const selects = []
  const attached = new Map()
  const buttons = new Set()
  const handlers = {}
  const effects = []
  const omd = {
    ultracode: {
      inspect: async (id) => {
        if (!views[id]) throw new Error(`no session ${id}`)
        return { ...views[id], revision: 1 }
      },
      select: async (id, input) => {
        selects.push({ session: id, ...input })
        views[id] = { ...views[id], mode: input.mode, submode: input.submode ?? views[id].submode, selected: { provider: input.provider, model: input.model, reasoningEffort: input.reasoningEffort ?? 'xhigh' } }
        handlers['session/event']?.({ id }, { type: 'model/selection', data: views[id].selected })
      },
    },
  }
  const iridium = {
    attach: (s, place, content) => (content === null ? attached.delete(`${s} ${place}`) : attached.set(`${s} ${place}`, content)),
    onButton: (fn) => (buttons.add(fn), () => buttons.delete(fn)),
  }
  const services = { omd, iridium, agents: { get: (id) => (views[id] ? {} : undefined) }, sessions: { list: () => Object.keys(views).map((id) => ({ id })) } }
  const ctx = { get: (n) => services[n], on: (n, fn) => (handlers[n] = fn), effect: (fn) => effects.push(fn()) }
  const press = (s, id) => Promise.all([...buttons].map((fn) => fn(s, id, { id: 'u' })))
  return { ctx, selects, attached, press, handlers, stop: () => effects.forEach((f) => f?.()) }
}

const sel = { provider: 'deepseek', model: 'v4', reasoningEffort: 'high' }

test('the key and its sub-modes with their marks, switched by a press; Ultra kept on a level picked elsewhere', async () => {
  const views = {
    s1: { eligible: true, mode: 'off', submode: 'free', teamAvailable: false, selected: sel },
    s2: { eligible: false, mode: 'off', reason: 'not on an OMD preset', selected: sel },
  }
  const f = fake(views)
  omdIridium.apply(f.ctx, { poll: 60_000 })
  await until(() => f.attached.size === 2)
  const { MARKS } = omdIridium
  const subs = (on) => [['workflow', 'Workflow'], ['free', 'Free']].map(([id, t]) => `<button id="omd.ultra.${id}" type="action"${id === on ? ' pressed' : ''}><img src="${MARKS[id]}"/>${t}</button>`).join('')
  assert.equal(
    f.attached.get('s1 effort').zh,
    `<button id="omd.ultra" type="action" title="OMD 的协作模式">Ultra</button><button id="omd.ultra.workflow" type="action"><img src="${MARKS.workflow}"/>Workflow</button><button id="omd.ultra.free" type="action"><img src="${MARKS.free}"/>自由</button>`,
  )
  assert.equal(f.attached.get('s2 effort').en, '<button id="omd.ultra" type="action" disabled title="not on an OMD preset">Ultra</button>')

  await f.press('s1', 'omd.ultra')
  assert.deepEqual(f.selects.at(-1), { session: 's1', provider: 'deepseek', model: 'v4', mode: 'ultra', expectedRevision: 1 })
  await until(() => f.attached.get('s1 effort').en.includes('pressed'))
  assert.equal(f.attached.get('s1 effort').en, `<button id="omd.ultra" type="action" pressed title="OMD's collaboration mode">Ultra</button>${subs('free')}`)
  for (const m of Object.values(MARKS)) assert.match(atob(m.split(',')[1]), /^<svg [^>]*viewBox="0 0 24 24"[^>]*>.*<\/svg>$/)

  await f.press('s1', 'omd.ultra.workflow')
  assert.equal(f.selects.at(-1).submode, 'workflow')
  assert.equal(f.selects.at(-1).reasoningEffort, 'xhigh') // on already: its level kept

  // A level picked in the Iridium: a plain selection, which OMD takes for leaving Ultra.
  const n = f.selects.length
  views.s1 = { ...views.s1, mode: 'off', selected: { ...sel, reasoningEffort: 'max' } }
  f.handlers['session/event']({ id: 's1' }, { type: 'model/selection', data: { ...sel, reasoningEffort: 'max' } })
  await until(() => f.selects.length === n + 1)
  assert.deepEqual(f.selects.at(-1), { session: 's1', provider: 'deepseek', model: 'v4', reasoningEffort: 'max', mode: 'ultra', submode: 'workflow', expectedRevision: 1 })

  await f.press('s1', 'omd.ultra')
  assert.equal(f.selects.at(-1).mode, 'off')
  await until(() => !f.attached.get('s1 effort').en.includes('pressed'))
  assert.equal(f.attached.get('s1 effort').en, `<button id="omd.ultra" type="action" title="OMD's collaboration mode">Ultra</button>${subs('')}`)
  await f.press('s1', 'other.button')
  assert.equal(f.selects.at(-1).mode, 'off')

  f.handlers['agent/disposed']({ agent: { id: 's2' } })
  assert.ok(!f.attached.has('s2 effort'))
  f.stop()
  assert.equal(f.attached.size, 0)
})

test('attribute values escaped', () => {
  const c = omdIridium.contentOf({ eligible: false, mode: 'off', reason: 'a "b" <c> & d' })
  assert.match(c.en, /title="a &quot;b&quot; &lt;c&gt; &amp; d"/)
})
