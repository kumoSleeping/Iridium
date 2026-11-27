// Runs the executor for Iridium's tests (internal/remote, internal/discover, internal/session): node
// run.mjs <url> <token> <state>; IRIDIUM_TEST_AGENTS, if set, is the hello's agents (JSON);
// IRIDIUM_TEST_CAPS, if set, its caps, comma-separated ("": none, as an older plugin; unset:
// session.attach and agent.personas with IRIDIUM_TEST_DSH, and dsh.inproc with
// IRIDIUM_TEST_INPROC; else none); IRIDIUM_TEST_HEADLESS=1: its hello says headless.
// IRIDIUM_TEST_INPROC, if set, a command line (JSON array) standing in for a hold run in DSH
// Desktop's process (argv[0] @dsh-desktop, inproc.js): that program is started instead, given the
// hold's argv after @dsh-desktop, its env, and IRIDIUM_TEST_INPROC_ARGV (the hold's argv, JSON).
// IRIDIUM_TEST_DSH, if set, a directory standing in for the dsh it runs in (dsh.js, desktop.js,
// attach.js), files read at each use:
//   open.json       the ids of the sessions open in it (desktop.sessions, desktop.sessions.changed);
//   running.json    those whose agent runs (none: all idle);
//   sessions.json   its sessions open or not, for desktop.sessions ([{id, cwd?, title?, last?}]);
//   models.json     desktop.models ({models, default});
//   workspaces.json its workspace registry ({workspaces: [{id, title, path, sessions}], archived});
//   presets.json    its agent preset registry ({presets: [{id, name?, description?, plugins,
//                   broken?}], default}; none: no registry: agent.presets unsupported);
//   attach.json     what a plugin here hangs on sessions ({session: {place: content}}: the service
//                   iridium's attach, synced every 200ms: one gone from it is taken away);
//   buttons.jsonl   each button pressed for it (session.button: {session, id, user});
//   sync.json       its "sync new sessions" switch (true/false);
//   keys.json       the keys a new session may start with (desktop.config's keys: [{place, from,
//                   content, presets?}]; none: not told).
// SIGUSR1 drops the connection (it connects again); SIGUSR2 tells all again as dsh's events would
// (desktop.*.changed); SIGTERM stops it and its processes.
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { createAttach } from '../attach.js'
import { createDesktop } from '../desktop.js'
import { dshMethods } from '../dsh.js'
import { Executor } from '../executor.js'

const [url, token, state] = process.argv.slice(2)
const dir = process.env.IRIDIUM_TEST_DSH
const agents = process.env.IRIDIUM_TEST_AGENTS ? JSON.parse(process.env.IRIDIUM_TEST_AGENTS) : undefined
const inprocCmd = process.env.IRIDIUM_TEST_INPROC ? JSON.parse(process.env.IRIDIUM_TEST_INPROC) : null
const caps =
  process.env.IRIDIUM_TEST_CAPS !== undefined
    ? process.env.IRIDIUM_TEST_CAPS.split(',').filter(Boolean)
    : [...(dir ? ['session.attach', 'agent.personas'] : []), ...(inprocCmd ? ['dsh.inproc'] : [])]
let ex = null
let methods
let attach = null
let desktop = null
if (dir) {
  const ctx = fakeDsh(dir)
  attach = createAttach({ send: (p) => ex?.notify('session.attach', p) ?? false })
  plugin(dir, attach.service)
  desktop = createDesktop(ctx, (e, p) => ex?.notify(e, p), {
    sync: () => read(dir, 'sync.json', false) === true,
    setSync: (on) => fs.writeFileSync(path.join(dir, 'sync.json'), JSON.stringify(!!on)),
    ...(fs.existsSync(path.join(dir, 'keys.json')) ? { keys: async () => read(dir, 'keys.json', []) } : {}),
    gather: 0,
  })
  methods = { ...dshMethods(ctx), ...desktop.methods }
}
ex = new Executor({
  url, token, state, agents, methods, caps,
  headless: process.env.IRIDIUM_TEST_HEADLESS === '1',
  inproc: inprocCmd ? fakeInproc(inprocCmd) : undefined,
  connected: () => {
    attach?.resend()
    desktop?.tellAll()
  },
  events: { 'session.button': (p) => attach?.button(p?.session, p?.id, p?.user) },
  log: (m) => console.error(`executor: ${m}`),
})
process.on('SIGUSR1', () => ex.drop())
process.on('SIGUSR2', () => desktop?.tellAll())
process.on('SIGTERM', () => {
  ex.stop()
  setTimeout(() => process.exit(0), 100)
})
ex.run()

/** A hold run "in this process": cmd started for it, its stdio as the hold's streams. */
function fakeInproc(cmd) {
  return {
    available: () => true,
    async start({ argv, env, input, out, err, exited }) {
      const child = spawn(cmd[0], [...cmd.slice(1), ...argv.slice(1)], {
        env: { ...process.env, ...env, IRIDIUM_TEST_INPROC_ARGV: JSON.stringify(argv) },
        stdio: ['pipe', out, err],
      })
      input.pipe(child.stdin)
      child.stdin.on('error', () => {})
      child.on('exit', (code, signal) => exited(signal ? { from: 'agent', code: null, signal: signal.replace(/^SIG/, '') } : { from: 'agent', code, signal: null }))
      return { stop: (hard) => (hard ? child.kill('SIGKILL') : input.end()) }
    },
  }
}

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

/** name in dir as JSON; none if it is not there. */
function read(dir, name, none) {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, name), 'utf8'))
  } catch {
    return none
  }
}

/** A cordis context with dsh's agents, session controller, workspace registry, models, kept in dir. */
function fakeDsh(dir) {
  const open = () => read(dir, 'open.json', [])
  const sessions = () => read(dir, 'sessions.json', [])
  const item = (x) => ({ sessionId: x.id, ...(x.cwd ? { cwd: x.cwd } : {}), ...(x.last ? { updatedAt: x.last } : {}), projections: { values: { ...(x.title ? { title: x.title } : {}) } } })
  const session = (id) => {
    const s = sessions().find((x) => x.id === id) ?? { id }
    return { id, header: { id, ...(s.cwd ? { cwd: s.cwd } : {}) } }
  }
  const services = {
    agents: {
      get: (id) => (open().includes(id) ? { session: session(id), status: read(dir, 'running.json', []).includes(id) ? 'running' : 'idle' } : undefined),
      list: () => open().map((id) => services.agents.get(id)),
    },
    sessionController: {
      listState: { summaryFor: (s) => item(sessions().find((y) => y.id === s.id) ?? { id: s.id }) },
      list: async () => ({ items: sessions().map(item) }),
      search: async ({ query }) => ({ items: sessions().filter((x) => `${x.title ?? ''} ${x.cwd ?? ''}`.toLowerCase().includes(query.toLowerCase())).map(item) }),
    },
    workspaceRegistry: {
      list: () => (read(dir, 'workspaces.json', {}).workspaces ?? []).map((w) => ({ ...w, sessionIds: w.sessions ?? [] })),
      get archivedSessionIds() {
        return read(dir, 'workspaces.json', {}).archived ?? []
      },
    },
  }
  if (fs.existsSync(path.join(dir, 'models.json'))) {
    const m = () => read(dir, 'models.json', { models: [] })
    services.llm = {
      listProviders: () => [...new Set(m().models.map((x) => x.value.split('/')[0]))].map((id) => ({ id, name: id })),
      listModels: async (p) => m().models.filter((x) => x.value.startsWith(p + '/')).map((x) => ({ id: x.value.slice(p.length + 1), ...(x.name ? { name: x.name } : {}) })),
      resolveModelInfo: async (p, id) => ({ reasoning: { efforts: (m().models.find((y) => y.value === `${p}/${id}`)?.efforts ?? []).map((e) => ({ id: e, name: e })) } }),
    }
    services.agentDefaultModel = {
      currentSelection: () => {
        const d = m().default
        if (!d) return undefined
        const i = d.indexOf('/')
        return { provider: d.slice(0, i), model: d.slice(i + 1) }
      },
    }
  }
  if (fs.existsSync(path.join(dir, 'presets.json')))
    services.agentPresets = {
      list: async () => read(dir, 'presets.json').presets.map(({ plugins, ...p }) => p),
      readDocument: async (id) => ({ agentPreset: id, content: read(dir, 'presets.json').presets.find((p) => p.id === id).plugins }),
      get defaultId() {
        return read(dir, 'presets.json').default
      },
    }
  return { get: (name) => services[name], on: () => () => {}, effect: () => {} }
}
