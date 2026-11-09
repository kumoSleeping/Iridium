// Runs the executor for Iridium's tests (internal/remote, internal/discover, internal/session): node
// run.mjs <url> <token> <state>; IRIDIUM_TEST_AGENTS, if set, is the hello's agents (JSON);
// IRIDIUM_TEST_CAPS, if set, its caps, comma-separated ("": none, as an older plugin; unset: held
// and held.features with IRIDIUM_TEST_DSH, else none). IRIDIUM_TEST_DSH, if set, a directory
// standing in for the dsh it runs in (dsh.js, held.js), files read at each use:
//   open.json       the ids of the sessions open in it (each one's agent: held there);
//   running.json    those whose agent runs (none: all idle);
//   prompts.jsonl   each message given to one of their agents (held.send);
//   models.json     the model selected for each by session ({provider, model, reasoningEffort?}:
//                   its agent's model.current(); none: OMD's `selected`, else none);
//   selects.jsonl   each model selection made through dsh's session controller (held.set model or
//                   effort: {sessionId, provider, model, reasoningEffort?}), written to models.json;
//   efforts.json    the levels each model has ({"<provider>/<model>": ["low", …]}: llm's
//                   resolveModelInfo; none: it cannot tell);
//   attached.jsonl  each session filed under a workspace (dsh.attach);
//   presets.json    its agent preset registry ({presets: [{id, name?, description?, plugins,
//                   broken?}], default}; none: no registry);
//   entries.json    its loader's rows ([{id, name, config?, disabled?}]; dsh.llm writes
//                   llm.patch.yml there);
//   omd.json        OMD's Ultra by session ({eligible, mode, submode, teamAvailable, selected,
//                   reason?}): while it is there, OMD is (its feature omd.ultra registered, taken
//                   away once it is gone); modes.jsonl each select asked of it. Ultra on without a
//                   level runs at xhigh, as OMD picks; a selection not made through OMD turns it
//                   off, as OMD's tie to the selection does;
//   unsynced.json   the workspaces its settings keep from the Iridium; ungrouped.json false if they
//                   keep its 未分组 too; unfeatured.json the feature ids switched off.
// A held session the Iridium asked about is looked at every 3s: a change in these files reaches it
// as held.changed. SIGUSR1 drops the connection (it connects again); SIGTERM stops it and its
// processes.
import fs from 'node:fs'
import path from 'node:path'
import { registerOmd } from '../adapters/omd.js'
import { dshMethods } from '../dsh.js'
import { Executor } from '../executor.js'
import { createFeatures } from '../features.js'
import { followHeld } from '../held.js'

const [url, token, state] = process.argv.slice(2)
const dir = process.env.IRIDIUM_TEST_DSH
const agents = process.env.IRIDIUM_TEST_AGENTS ? JSON.parse(process.env.IRIDIUM_TEST_AGENTS) : undefined
const llm = dir && agents?.[0]?.scope ? { file: path.join(dir, 'llm.patch.yml'), scope: agents[0].scope } : {}
const caps = process.env.IRIDIUM_TEST_CAPS !== undefined ? process.env.IRIDIUM_TEST_CAPS.split(',').filter(Boolean) : dir ? ['held', 'held.features'] : []
let ex = null
let methods
if (dir) {
  const ctx = fakeDsh(dir)
  const features = createFeatures({ unfeatured: () => read(dir, 'unfeatured.json', []) })
  const follow = followHeld(ctx, features, (event, params) => ex?.notify(event, params))
  // OMD comes and goes with omd.json, as cordis starts and stops the adapter with OMD's service.
  let omd = null
  const syncOmd = () => {
    const has = fs.existsSync(path.join(dir, 'omd.json'))
    if (has && !omd) omd = registerOmd(features, ctx.get('omd'))
    else if (!has && omd) {
      omd()
      omd = null
    }
  }
  syncOmd()
  setInterval(syncOmd, 500).unref()
  methods = Object.fromEntries(
    Object.entries(dshMethods(ctx, () => kept(dir), llm, { features, follow })).map(([k, fn]) => [
      k,
      (p) => {
        syncOmd()
        return fn(p)
      },
    ]),
  )
}
ex = new Executor({ url, token, state, agents, methods, caps, denied: llm.file ? [llm.file] : [], log: (m) => console.error(`executor: ${m}`) })
process.on('SIGUSR1', () => ex.drop())
process.on('SIGTERM', () => {
  ex.stop()
  setTimeout(() => process.exit(0), 100)
})
ex.run()

/** name in dir as JSON; none if it is not there. */
function read(dir, name, none) {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, name), 'utf8'))
  } catch {
    return none
  }
}

/** A cordis context with dsh's sessions, agents, session controller, workspace registry, kept in dir. */
function fakeDsh(dir) {
  const file = (name) => path.join(dir, name)
  const write = (name, v) => fs.writeFileSync(file(name), JSON.stringify(v))
  // The selection a session's agent runs with: models.json's, else the one OMD's file gives.
  const selection = (id) => read(dir, 'models.json', {})[id] ?? read(dir, 'omd.json', {})[id]?.selected
  const select = (id, sel, byOmd) => {
    write('models.json', { ...read(dir, 'models.json', {}), [id]: sel })
    const omd = read(dir, 'omd.json', null)
    if (omd?.[id]) write('omd.json', { ...omd, [id]: { ...omd[id], selected: sel, ...(byOmd ? {} : { mode: 'off' }) } })
  }
  const open = () => JSON.parse(fs.readFileSync(file('open.json'), 'utf8')) // none: an error, as dsh without sessions
  const services = {
    sessions: { list: () => open().map((id) => ({ id })) },
    // The agents of the sessions open in it: a message given one is noted in prompts.jsonl.
    agents: {
      get: (id) => {
        if (!open().includes(id)) return undefined
        const note = (how) => async (input) => fs.appendFileSync(file('prompts.jsonl'), JSON.stringify({ session: id, how, text: input.content[0].text, source: input.source.kind }) + '\n')
        const running = read(dir, 'running.json', []).includes(id)
        return { followup: note('followup'), steer: note('steer'), status: running ? 'running' : 'idle', model: { current: () => selection(id) } }
      },
    },
    // dsh's session controller: a model selected on a loaded session, noted in selects.jsonl.
    sessionController: {
      selectModel: async (req) => {
        if (!services.agents.get(req.sessionId)) throw new Error(`session ${req.sessionId} is not loaded`)
        fs.appendFileSync(file('selects.jsonl'), JSON.stringify(req) + '\n')
        const { sessionId, ...sel } = req
        select(sessionId, sel, false)
        return { selected: sel }
      },
    },
    workspaceRegistry: {
      resolveByPath: async () => undefined,
      create: async (cwd) => ({
        id: `w:${cwd}`,
        attachSession: async (session) => fs.appendFileSync(file('attached.jsonl'), JSON.stringify({ session, cwd }) + '\n'),
      }),
    },
    // OMD's Ultra, as omd.json keeps it by session; each select noted in modes.jsonl.
    omd: {
      ultracode: {
        inspect: async (id) => {
          const v = read(dir, 'omd.json', {})[id]
          if (!v) throw new Error(`no session ${id}`)
          const sel = selection(id)
          return { ...v, ...(sel ? { selected: sel } : {}), revision: 1 }
        },
        select: async (id, input) => {
          fs.appendFileSync(file('modes.jsonl'), JSON.stringify({ session: id, ...input }) + '\n')
          const a = read(dir, 'omd.json', {})
          const effort = input.reasoningEffort ?? (input.mode === 'ultra' ? 'xhigh' : undefined)
          write('omd.json', { ...a, [id]: { ...a[id], mode: input.mode, submode: input.submode ?? a[id].submode } })
          select(id, { provider: input.provider, model: input.model, ...(effort ? { reasoningEffort: effort } : {}) }, true)
          return {}
        },
      },
    },
  }
  if (fs.existsSync(file('efforts.json')))
    services.llm = {
      resolveModelInfo: async (provider, model) => {
        const levels = read(dir, 'efforts.json', {})[`${provider}/${model}`]
        if (!levels) throw new Error(`no model ${provider}/${model}`)
        return { provider, id: model, name: model, reasoning: { efforts: levels.map((id) => ({ id, name: id })) } }
      },
    }
  if (fs.existsSync(file('presets.json')))
    services.agentPresets = {
      list: async () => read(dir, 'presets.json').presets.map(({ plugins, ...p }) => p),
      readDocument: async (id) => ({ agentPreset: id, content: read(dir, 'presets.json').presets.find((p) => p.id === id).plugins }),
      get defaultId() {
        return read(dir, 'presets.json').default
      },
    }
  if (fs.existsSync(file('entries.json')))
    services.loader = {
      *entries() {
        for (const options of read(dir, 'entries.json')) yield { options }
      },
    }
  // OMD's service is there while omd.json is.
  return { get: (name) => (name === 'omd' && !fs.existsSync(file('omd.json')) ? undefined : services[name]) }
}

/** What the plugin's settings keep from the Iridium, as dir has them (nothing if not set). */
function kept(dir) {
  return { unsynced: read(dir, 'unsynced.json', []), ungrouped: read(dir, 'ungrouped.json', true) }
}
