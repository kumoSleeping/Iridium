// Runs the executor for Iridium's tests (internal/remote, internal/discover): node run.mjs <url>
// <token> <state>; IRIDIUM_TEST_AGENTS, if set, is the hello's agents (JSON); IRIDIUM_TEST_DSH, if
// set, a directory standing in for the dsh it runs in (dsh.js): open.json the ids of the sessions
// open in it, prompts.jsonl each message given to one of their agents, attached.jsonl each session
// filed under a workspace, presets.json its agent preset registry ({presets: [{id, name?,
// description?, plugins, broken?}], default}; none: no registry), entries.json its loader's rows
// ([{id, name, config?, disabled?}]; dsh.llm writes llm.patch.yml there), unsynced.json the workspaces
// its settings keep from the Iridium, ungrouped.json false if they keep its 未分组 too. SIGUSR1 drops the connection
// (it connects again); SIGTERM stops it and its processes.
import fs from 'node:fs'
import path from 'node:path'
import { dshMethods } from '../dsh.js'
import { Executor } from '../executor.js'

const [url, token, state] = process.argv.slice(2)
const agents = process.env.IRIDIUM_TEST_AGENTS ? JSON.parse(process.env.IRIDIUM_TEST_AGENTS) : undefined
const llm = process.env.IRIDIUM_TEST_DSH && agents?.[0]?.scope ? { file: path.join(process.env.IRIDIUM_TEST_DSH, 'llm.patch.yml'), scope: agents[0].scope } : {}
const methods = process.env.IRIDIUM_TEST_DSH ? dshMethods(fakeDsh(process.env.IRIDIUM_TEST_DSH), () => kept(process.env.IRIDIUM_TEST_DSH), llm) : undefined
const ex = new Executor({ url, token, state, agents, methods, denied: llm.file ? [llm.file] : [], log: (m) => console.error(`executor: ${m}`) })
process.on('SIGUSR1', () => ex.drop())
process.on('SIGTERM', () => {
  ex.stop()
  setTimeout(() => process.exit(0), 100)
})
ex.run()

/** A cordis context with dsh's sessions and workspace registry, kept in dir. */
function fakeDsh(dir) {
  const services = {
    sessions: { list: () => JSON.parse(fs.readFileSync(path.join(dir, 'open.json'), 'utf8')).map((id) => ({ id })) },
    // The agents of the sessions open in it: a message given one is noted in prompts.jsonl.
    agents: {
      get: (id) => {
        const open = JSON.parse(fs.readFileSync(path.join(dir, 'open.json'), 'utf8'))
        if (!open.includes(id)) return undefined
        const note = (how) => async (input) => fs.appendFileSync(path.join(dir, 'prompts.jsonl'), JSON.stringify({ session: id, how, text: input.content[0].text, source: input.source.kind }) + '\n')
        return { followup: note('followup'), steer: note('steer') }
      },
    },
    workspaceRegistry: {
      resolveByPath: async () => undefined,
      create: async (cwd) => ({
        id: `w:${cwd}`,
        attachSession: async (session) => fs.appendFileSync(path.join(dir, 'attached.jsonl'), JSON.stringify({ session, cwd }) + '\n'),
      }),
    },
  }
  const presets = () => JSON.parse(fs.readFileSync(path.join(dir, 'presets.json'), 'utf8'))
  if (fs.existsSync(path.join(dir, 'presets.json')))
    services.agentPresets = {
      list: async () => presets().presets.map(({ plugins, ...p }) => p),
      readDocument: async (id) => ({ agentPreset: id, content: presets().presets.find((p) => p.id === id).plugins }),
      get defaultId() {
        return presets().default
      },
    }
  if (fs.existsSync(path.join(dir, 'entries.json')))
    services.loader = {
      *entries() {
        for (const options of JSON.parse(fs.readFileSync(path.join(dir, 'entries.json'), 'utf8'))) yield { options }
      },
    }
  return { get: (name) => services[name] }
}

/** What the plugin's settings keep from the Iridium, as dir has them (nothing if not set). */
function kept(dir) {
  const read = (name, none) => {
    try {
      return JSON.parse(fs.readFileSync(path.join(dir, name), 'utf8'))
    } catch {
      return none
    }
  }
  return { unsynced: read('unsynced.json', []), ungrouped: read('ungrouped.json', true) }
}
