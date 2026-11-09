// The feature registry, held.* and the OMD adapter, without dsh: node --test (in this folder).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { omdFeature, registerOmd } from '../adapters/omd.js'
import { createFeatures } from '../features.js'
import { followHeld, heldMethods } from '../held.js'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const S = 'ssss'

/** A provider of id that answers view v (a function: called each time). */
const provider = (id, v = {}, more = {}) => ({
  id,
  get: async () => (typeof v === 'function' ? v() : { name: id, on: false, available: true, place: 'effort', ...v }),
  set: async () => {},
  ...more,
})

test('a feature id is taken once: the first, then the next once it goes', async () => {
  const f = createFeatures()
  assert.throws(() => f.provide(provider('Bad id')), /<plugin>\.<name>/)
  assert.throws(() => f.provide(provider('nodot')), /<plugin>\.<name>/)
  const a = f.provide(provider('x.a', { name: 'first' }))
  const b = f.provide(provider('x.a', { name: 'second' }))
  assert.deepEqual((await f.views(S)).map((v) => v.name), ['first'])
  b() // the later one going changes nothing
  assert.deepEqual((await f.views(S)).map((v) => v.name), ['first'])
  const c = f.provide(provider('x.a', { name: 'third' }))
  a()
  assert.deepEqual((await f.views(S)).map((v) => v.name), ['third'])
  c()
  assert.deepEqual(await f.views(S), [])
  f.dispose()
})

test('an error or a timeout makes only its own feature unavailable', async () => {
  const f = createFeatures({ timeouts: { get: 50, set: 50 } })
  f.provide(provider('x.ok'))
  f.provide({ id: 'x.err', name: 'Err', get: async () => { throw new Error('broken ' + 'x'.repeat(200)) }, set: async () => {} })
  f.provide({ id: 'x.slow', get: () => new Promise(() => {}), set: () => new Promise(() => {}) })
  const vs = await f.views(S)
  assert.equal(vs.length, 3)
  const by = Object.fromEntries(vs.map((v) => [v.id, v]))
  assert.equal(by['x.ok'].available, true)
  assert.equal(by['x.ok'].v, 1)
  assert.equal(by['x.err'].available, false)
  assert.equal(by['x.err'].name, 'Err')
  assert.ok(by['x.err'].reason.startsWith('broken '))
  assert.equal(by['x.err'].reason.length, 120)
  assert.equal(by['x.slow'].available, false)
  assert.match(by['x.slow'].reason, /no answer/)
  await assert.rejects(f.set(S, 'x.slow', { on: true }), (e) => e.code === 'failed')
  await assert.rejects(f.set(S, 'x.none', { on: true }), (e) => e.code === 'not_found')
  f.dispose()
})

test('a feature switched off in the settings is neither shown nor set', async () => {
  let off = ['x.b']
  const f = createFeatures({ unfeatured: () => off })
  const set = []
  f.provide(provider('x.a'))
  f.provide(provider('x.b', {}, { set: async (s, v) => set.push(v) }))
  assert.deepEqual((await f.views(S)).map((v) => v.id), ['x.a'])
  await assert.rejects(f.set(S, 'x.b', { on: true }), (e) => e.code === 'not_found')
  off = []
  await f.set(S, 'x.b', { on: true, option: 'o' })
  assert.deepEqual(set, [{ on: true, option: 'o' }])
  assert.deepEqual((await f.views(S)).map((v) => v.id), ['x.a', 'x.b'])
  f.dispose()
})

test('changes are coalesced per session: at most one in each gap, the last at its end', async () => {
  const f = createFeatures({ coalesce: 100 })
  const heard = []
  f.listen((s) => heard.push(s))
  for (let i = 0; i < 5; i++) f.changed('a')
  f.changed('b')
  await sleep(20)
  assert.deepEqual(heard.sort(), ['a', 'b'])
  for (let i = 0; i < 5; i++) f.changed('a')
  await sleep(40)
  assert.equal(heard.filter((s) => s === 'a').length, 1)
  await sleep(100)
  assert.equal(heard.filter((s) => s === 'a').length, 2)
  f.dispose()
})

/** A dsh with sessions held (agents), dsh's session controller, its llm, and OMD, in memory. */
function fakeDsh({ held = [S], omd = {}, selected = {}, efforts = {} } = {}) {
  const st = { held: new Set(held), omd, selected, selects: [], modes: [] }
  const select = (id, sel, byOmd) => {
    st.selected[id] = sel
    if (st.omd[id] && !byOmd) st.omd[id].mode = 'off'
  }
  const services = {
    sessions: { list: () => [...st.held].map((id) => ({ id })) },
    agents: { get: (id) => (st.held.has(id) ? { status: 'idle', model: { current: () => st.selected[id] }, followup: async () => {}, steer: async () => {} } : undefined) },
    sessionController: {
      selectModel: async ({ sessionId, ...sel }) => {
        st.selects.push({ sessionId, ...sel })
        select(sessionId, sel, false)
        return { selected: sel }
      },
    },
    llm: {
      resolveModelInfo: async (p, m) => {
        const e = efforts[`${p}/${m}`]
        if (!e) throw new Error('no such model')
        return { reasoning: { efforts: e.map((id) => ({ id, name: id })) } }
      },
    },
    omd: {
      ultracode: {
        inspect: async (id) => {
          if (!st.omd[id]) throw new Error(`no session ${id}`)
          return { ...st.omd[id], selected: st.selected[id], revision: 7 }
        },
        select: async (id, input) => {
          st.modes.push(input)
          const effort = input.reasoningEffort ?? (input.mode === 'ultra' ? 'xhigh' : undefined)
          st.omd[id] = { ...st.omd[id], mode: input.mode, submode: input.submode ?? st.omd[id].submode }
          select(id, { provider: input.provider, model: input.model, ...(effort ? { reasoningEffort: effort } : {}) }, true)
        },
      },
    },
  }
  return { st, ctx: { get: (n) => services[n] } }
}

const omdSession = (more = {}) => ({ [S]: { eligible: true, mode: 'off', submode: 'free', teamAvailable: false, ...more } })

test('OMD Ultra: as it is, held or not, in either language', async () => {
  const { ctx } = fakeDsh({ omd: omdSession(), selected: { [S]: { provider: 'p', model: 'm', reasoningEffort: 'high' } } })
  const u = omdFeature(ctx.get('omd'))
  let v = await u.get(S, { lang: 'zh', held: true })
  assert.equal(v.available, true)
  assert.equal(v.on, false)
  assert.equal(v.tone, 'warm')
  assert.equal(v.place, 'effort')
  assert.deepEqual(v.options, [{ id: 'workflow', name: 'Workflow' }, { id: 'free', name: '自由' }])
  assert.equal(v.option, 'free')
  assert.match(v.hint, /OMD 的协作模式/)
  v = await u.get(S, { lang: 'en', held: false })
  assert.equal(v.available, false)
  assert.equal(v.reason, 'Not open in DSH Desktop, or not on an OMD preset')
  assert.deepEqual(v.options.map((o) => o.name), ['Workflow', 'Free'])
  // One OMD never saw, not held: off, not an error.
  v = await u.get('other', { lang: 'zh', held: false })
  assert.equal(v.on, false)
  assert.equal(v.reason, '会话不在 DSH Desktop 里打开着，或没用 OMD 的预设')
  // Held but not eligible: OMD's own reason.
  const n = fakeDsh({ omd: omdSession({ eligible: false, reason: '不是 OMD 预设', teamAvailable: true }) })
  v = await omdFeature(n.ctx.get('omd')).get(S, { lang: 'zh', held: true })
  assert.equal(v.available, false)
  assert.equal(v.reason, '不是 OMD 预设')
  assert.deepEqual(v.options.map((o) => o.id), ['workflow', 'team', 'free'])
})

test('OMD Ultra through held.*: on at its own level, a sub-mode keeps it, the track sets it, off at the track', async () => {
  const { st, ctx } = fakeDsh({ omd: omdSession(), selected: { [S]: { provider: 'p', model: 'm', reasoningEffort: 'high' } }, efforts: { 'p/m2': ['low', 'high'], 'p/m3': ['low'] } })
  const f = createFeatures()
  registerOmd(f, ctx.get('omd'))
  const h = heldMethods(ctx, f)
  let live = await h['held.get']({ session: S, lang: 'en' })
  assert.deepEqual({ held: live.held, model: live.model, effort: live.effort }, { held: true, model: 'p/m', effort: 'high' })
  assert.equal(live.features[0].id, 'omd.ultra')
  assert.equal(live.features[0].on, false)

  // On: no level asked (OMD picks: xhigh here).
  live = await h['held.set']({ session: S, feature: { id: 'omd.ultra', on: true } })
  assert.deepEqual(st.modes.at(-1), { provider: 'p', model: 'm', mode: 'ultra', expectedRevision: 7 })
  assert.equal(live.features[0].on, true)
  assert.equal(live.features[0].active, true)
  assert.equal(live.effort, 'xhigh')
  // A sub-mode alone: Ultra's level kept.
  live = await h['held.set']({ session: S, feature: { id: 'omd.ultra', on: true, option: 'workflow' } })
  assert.deepEqual(st.modes.at(-1), { provider: 'p', model: 'm', reasoningEffort: 'xhigh', mode: 'ultra', submode: 'workflow', expectedRevision: 7 })
  assert.equal(live.features[0].option, 'workflow')
  await assert.rejects(h['held.set']({ session: S, feature: { id: 'omd.ultra', on: true, option: 'nope' } }), (e) => e.code === 'bad_request')
  // The track while on: through OMD, Ultra kept.
  live = await h['held.set']({ session: S, effort: 'max' })
  assert.deepEqual(st.modes.at(-1), { provider: 'p', model: 'm', reasoningEffort: 'max', mode: 'ultra', expectedRevision: 7 })
  assert.equal(live.effort, 'max')
  assert.equal(st.selects.length, 0)
  // Off: at the track's level.
  live = await h['held.set']({ session: S, feature: { id: 'omd.ultra', on: false } })
  assert.deepEqual(st.modes.at(-1), { provider: 'p', model: 'm', reasoningEffort: 'max', mode: 'off', expectedRevision: 7 })
  assert.equal(live.features[0].on, false)
  // The track while off: dsh's own selection.
  live = await h['held.set']({ session: S, effort: 'low' })
  assert.deepEqual(st.selects.at(-1), { sessionId: S, provider: 'p', model: 'm', reasoningEffort: 'low' })
  assert.equal(live.effort, 'low')
  // A model: the level kept if it has it, else none.
  live = await h['held.set']({ session: S, model: 'p/m2' })
  assert.deepEqual(st.selects.at(-1), { sessionId: S, provider: 'p', model: 'm2', reasoningEffort: 'low' })
  assert.equal(live.model, 'p/m2')
  st.selected[S] = { provider: 'p', model: 'm2', reasoningEffort: 'high' }
  await h['held.set']({ session: S, model: 'p/m3' })
  assert.deepEqual(st.selects.at(-1), { sessionId: S, provider: 'p', model: 'm3' })
  // Not held: not_found; nothing or two things asked: bad_request.
  await assert.rejects(h['held.set']({ session: 'other', effort: 'low' }), (e) => e.code === 'not_found')
  await assert.rejects(h['held.set']({ session: S }), (e) => e.code === 'bad_request')
  await assert.rejects(h['held.set']({ session: S, effort: 'low', model: 'p/m' }), (e) => e.code === 'bad_request')
  await assert.rejects(h['held.set']({ session: S, model: 'nomodel' }), (e) => e.code === 'bad_request')
  // A session not held: its features shown, not available; no model or effort.
  live = await h['held.get']({ session: 'other' })
  assert.equal(live.held, false)
  assert.equal(live.model, undefined)
  assert.equal(live.features[0].available, false)
  f.dispose()
})

test('held.changed: a watched session, once per change, in the language asked', async () => {
  const { st, ctx } = fakeDsh({ omd: omdSession(), selected: { [S]: { provider: 'p', model: 'm', reasoningEffort: 'high' } } })
  const f = createFeatures({ coalesce: 30 })
  registerOmd(f, ctx.get('omd'))
  const sent = []
  const follow = followHeld(ctx, f, (event, params) => sent.push({ event, ...params }))
  const h = heldMethods(ctx, f, undefined, follow)
  f.changed(S) // not watched yet: nothing
  await sleep(60)
  assert.equal(sent.length, 0)
  await h['held.get']({ session: S, lang: 'en' })
  f.changed(S) // watched, but as it was answered: nothing
  await sleep(60)
  assert.equal(sent.length, 0)
  st.omd[S].mode = 'ultra' // switched in Desktop
  st.selected[S] = { ...st.selected[S], reasoningEffort: 'xhigh' }
  f.changed(S)
  f.changed(S)
  await sleep(80)
  assert.equal(sent.length, 1)
  assert.equal(sent[0].event, 'held.changed')
  assert.equal(sent[0].session, S)
  assert.equal(sent[0].live.effort, 'xhigh')
  assert.equal(sent[0].live.features[0].on, true)
  assert.equal(sent[0].live.features[0].options.at(-1).name, 'Free')
  // A feature gone (OMD unloaded): every watched session looked at again.
  f.provide(provider('x.new'))
  await sleep(80)
  assert.equal(sent.length, 2)
  assert.deepEqual(sent[1].live.features.map((x) => x.id), ['omd.ultra', 'x.new'])
  follow.stop()
  f.dispose()
})

test('a hung provider: held.get still answers within the registry\'s timeout, the others\' features and the level in it', async () => {
  const { ctx } = fakeDsh({ selected: { [S]: { provider: 'p', model: 'm', reasoningEffort: 'high' } } })
  const f = createFeatures({ timeouts: { get: 100, set: 100 } })
  const hang = () => new Promise(() => {})
  f.provide({ id: 'x.hung', get: hang, set: hang, ownsEffort: hang, effort: hang })
  f.provide({ id: 'x.slow', get: hang, set: hang, ownsEffort: hang })
  f.provide(provider('x.ok'))
  const h = heldMethods(ctx, f)
  const t0 = Date.now()
  const live = await h['held.get']({ session: S })
  assert.ok(Date.now() - t0 < 180, `took ${Date.now() - t0}ms`)
  assert.deepEqual(live.features.map((x) => [x.id, x.available]), [['x.hung', false], ['x.slow', false], ['x.ok', true]])
  assert.equal(live.effort, 'high')
  // An owner that says so in time, its level slow: the selection's level.
  const g = createFeatures({ timeouts: { get: 100, set: 100 } })
  g.provide(provider('x.own', {}, { ownsEffort: async () => true, effort: hang }))
  const t1 = Date.now()
  assert.equal((await heldMethods(ctx, g)['held.get']({ session: S })).effort, 'high')
  assert.ok(Date.now() - t1 < 180)
  f.dispose()
  g.dispose()
})

test('OMD Ultra on, the session not held: on and in effect, not switchable here', async () => {
  const { ctx } = fakeDsh({ held: [], omd: omdSession({ mode: 'ultra' }) })
  const v = await omdFeature(ctx.get('omd')).get(S, { lang: 'en', held: false })
  assert.deepEqual({ on: v.on, active: v.active, available: v.available }, { on: true, active: true, available: false })
})

test('held.changed: a session held here stays watched past the watch, one no longer held does not', async () => {
  const { st, ctx } = fakeDsh({ held: [S, 'other'], omd: omdSession(), selected: { [S]: { provider: 'p', model: 'm', reasoningEffort: 'high' }, other: { provider: 'p', model: 'm' } } })
  const f = createFeatures({ coalesce: 10 })
  const sent = []
  const follow = followHeld(ctx, f, (event, params) => sent.push(params), { watch: 50, poll: 20 })
  const h = heldMethods(ctx, f, undefined, follow)
  await h['held.get']({ session: S })
  await h['held.get']({ session: 'other' })
  st.held.delete('other')
  await sleep(150) // past the watch
  st.selected[S] = { ...st.selected[S], reasoningEffort: 'low' } // changed in Desktop
  st.selected.other = { provider: 'p', model: 'm2' }
  await sleep(80)
  assert.ok(sent.some((x) => x.session === S && x.live.effort === 'low'))
  const others = sent.filter((x) => x.session === 'other').length
  st.held.add('other')
  await sleep(80)
  assert.equal(sent.filter((x) => x.session === 'other').length, others) // its watch lapsed: not looked at
  follow.stop()
  f.dispose()
})

test('held.list and held.send as dsh.open and dsh.prompt did', async () => {
  const { ctx } = fakeDsh()
  const h = heldMethods(ctx, undefined, () => ({ unsynced: ['/w'], ungrouped: false }))
  assert.deepEqual(await h['held.list']({}), { sessions: [S], running: [], unsynced: ['/w'], unsyncedUngrouped: true })
  assert.equal(h['dsh.open'], h['held.list'])
  assert.equal(h['dsh.prompt'], h['held.send'])
  assert.deepEqual(await h['held.send']({ session: S, text: 'hi' }), {})
  await assert.rejects(h['held.send']({ session: 'other', text: 'hi' }), (e) => e.code === 'not_found')
  // No registry: no features.
  assert.deepEqual(await h['held.get']({ session: S }), { held: true })
})
