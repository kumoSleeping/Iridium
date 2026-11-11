// Runs the executor for Iridium's tests (internal/remote, internal/discover, internal/session): node
// run.mjs <url> <token> <state>; IRIDIUM_TEST_AGENTS, if set, is the hello's agents (JSON);
// IRIDIUM_TEST_CAPS, if set, its caps, comma-separated ("": none, as an older plugin; unset: held,
// agent.prepare and session.attach with IRIDIUM_TEST_DSH, else none). IRIDIUM_TEST_DSH, if set, a
// directory standing in for the dsh it runs in (dsh.js, held.js, attach.js), files read at each use:
//   open.json       the ids of the sessions open in it (each one's agent: held there);
//   running.json    those whose agent runs (none: all idle);
//   prompts.jsonl   each message given to one of their agents (held.send);
//   models.json     the model selected for each by session ({provider, model, reasoningEffort?}:
//                   its agent's model.current());
//   selects.jsonl   each model selection made through dsh's session controller (held.set model or
//                   effort: {sessionId, provider, model, reasoningEffort?}), written to models.json;
//   efforts.json    the levels each model has ({"<provider>/<model>": ["low", …]}: llm's
//                   resolveModelInfo; none: it cannot tell);
//   attached.jsonl  each session filed under a workspace (a session process said it began);
//   presets.json    its agent preset registry ({presets: [{id, name?, description?, plugins,
//                   broken?}], default}; none: no registry: agent.presets unsupported);
//   profile/        the directory of the profile it runs (package.json, node_modules: the bundles
//                   agent.prepare binds a preset over);
//   entries.json    its loader's rows ([{id, name, config?, disabled?}]; agent.prepare writes
//                   llm.patch.yml there); prepared/ the bound presets' patches it writes;
//   attach.json     what a plugin here hangs on sessions ({session: {place: content}}: the service
//                   iridium's attach, synced every 200ms: one gone from it is taken away);
//   buttons.jsonl   each button pressed for it (session.button: {session, id, user});
//   session/        a dsh process the Iridium started here (session.js, over the bridge): a fake dsh
//                   of its own, as this one, session.json the sessions it runs; its attach.json and
//                   buttons.jsonl as above, for the plugins in that process;
//   unsynced.json   the workspaces its settings keep from the Iridium; ungrouped.json false if they
//                   keep its 未分组 too.
// A held session the Iridium asked about is looked at every 3s: a change in these files reaches it
// as held.changed. SIGUSR1 drops the connection (it connects again); SIGTERM stops it and its
// processes.
import fs from 'node:fs'
import path from 'node:path'
import { createAttach } from '../attach.js'
import { bridgeEndpoint, createBridge } from '../bridge.js'
import { dshMethods, fileSession } from '../dsh.js'
import { Executor } from '../executor.js'
import { followHeld } from '../held.js'
import * as session from '../session.js'

const [url, token, state] = process.argv.slice(2)
const dir = process.env.IRIDIUM_TEST_DSH
const agents = process.env.IRIDIUM_TEST_AGENTS ? JSON.parse(process.env.IRIDIUM_TEST_AGENTS) : undefined
const llm = dir && agents?.[0]?.scope ? { file: path.join(dir, 'llm.patch.yml'), scope: agents[0].scope } : {}
const caps = process.env.IRIDIUM_TEST_CAPS !== undefined ? process.env.IRIDIUM_TEST_CAPS.split(',').filter(Boolean) : dir ? ['held', 'agent.prepare', 'session.attach'] : []
let ex = null
let methods
let attach = null
if (dir) {
  const ctx = fakeDsh(dir)
  let bridge = null
  attach = createAttach({ send: (p) => ex?.notify('session.attach', p) ?? false, toProcess: (s, id, user) => bridge?.button(s, id, user) })
  bridge = createBridge({
    endpoint: bridgeEndpoint(dir),
    started: (s, cwd) => fileSession(ctx, s, cwd).catch((e) => console.error(`executor: filing ${s}: ${e.message}`)),
    attach: (s, place, content, owner) => attach.put(s, place, content, owner),
    gone: (owner) => attach.drop(owner),
  })
  plugin(dir, attach.service)
  const follow = followHeld(ctx, (event, params) => ex?.notify(event, params))
  if (fs.existsSync(path.join(dir, 'session', 'session.json'))) startSession(path.join(dir, 'session'), bridge)
  methods = dshMethods(ctx, () => kept(dir), llm, { follow, bridge, sessionFile: path.join(dir, 'session.patch.yml'), dir: path.join(dir, 'prepared') })
}
ex = new Executor({
  url, token, state, agents, methods, caps,
  denied: [...(llm.file ? [llm.file] : []), ...(dir ? [path.join(dir, 'session.patch.yml'), path.join(dir, 'prepared')] : [])],
  connected: () => attach?.resend(),
  events: { 'session.button': (p) => attach?.button(p?.session, p?.id, p?.user) },
  log: (m) => console.error(`executor: ${m}`),
})
process.on('SIGUSR1', () => ex.drop())
process.on('SIGTERM', () => {
  ex.stop()
  setTimeout(() => process.exit(0), 100)
})
ex.run()

/**
 * A plugin using the service iridium as dir's attach.json says, every 200ms; each button pressed
 * for it noted in dir's buttons.jsonl.
 */
function plugin(dir, iridium) {
  let last = {}
  const sync = () => {
    const now = read(dir, 'attach.json', {})
    for (const [s, places] of Object.entries(last)) for (const place of Object.keys(places)) if (now[s]?.[place] === undefined) iridium.attach(s, place, null)
    for (const [s, places] of Object.entries(now)) {
      for (const [place, content] of Object.entries(places)) {
        if (JSON.stringify(last[s]?.[place]) !== JSON.stringify(content)) {
          try {
            iridium.attach(s, place, content)
          } catch (e) {
            console.error(`executor: attach: ${e.message}`)
          }
        }
      }
    }
    last = now
  }
  sync()
  setInterval(sync, 200).unref()
  iridium.onButton((session, id, user) => fs.appendFileSync(path.join(dir, 'buttons.jsonl'), JSON.stringify({ session, id, ...(user ? { user } : {}) }) + '\n'))
}

/**
 * A dsh process the Iridium started (session.js, as the patch agent.prepare writes loads it), in
 * this one: its own fake dsh at dir; the sessions it runs are session.json's, their folder dir.
 */
function startSession(dir, bridge) {
  const there = fakeDsh(dir)
  const services = { workingDirectory: { get: () => dir } }
  const on = {}
  const ctx = {
    provide: (name, v) => (services[name] = v),
    get: (name) => services[name] ?? there.get(name),
    on: (name, fn) => (on[name] = fn),
    effect: (fn) => fn(),
    plugin: () => {}, // OMD's Ultra (omd.js): no OMD in these tests
  }
  session.apply(ctx, { endpoint: bridge.endpoint, secret: bridge.secret })
  plugin(dir, ctx.get('iridium'))
  for (const id of read(dir, 'session.json', [])) on['agent/created']({ agent: { id, session: { id } } })
}

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
  // The selection a session's agent runs with: models.json's.
  const selection = (id) => read(dir, 'models.json', {})[id]
  const select = (id, sel) => write('models.json', { ...read(dir, 'models.json', {}), [id]: sel })
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
        select(sessionId, sel)
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
  if (fs.existsSync(file('profile'))) services.profileContext = { dir: file('profile') }
  if (fs.existsSync(file('entries.json')))
    services.loader = {
      *entries() {
        for (const options of read(dir, 'entries.json')) yield { options }
      },
    }
  return { get: (name) => services[name] }
}

/** What the plugin's settings keep from the Iridium, as dir has them (nothing if not set). */
function kept(dir) {
  return { unsynced: read(dir, 'unsynced.json', []), ungrouped: read(dir, 'ungrouped.json', true) }
}
