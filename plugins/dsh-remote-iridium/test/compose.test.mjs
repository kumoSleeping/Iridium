// A preset of DSH Desktop's bundles bound for a dsh process the Iridium starts (compose.js), and the
// patches agent.prepare gives (dsh.js): node --test test/. The expectations are those the Iridium
// had for the same patch (its internal/agent/dsh bound_test.go, modules_test.go).
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { pathToFileURL } from 'node:url'
import { bound, boundPatch, externalPackages, moduleName } from '../compose.js'
import { dshMethods } from '../dsh.js'

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), 'compose-'))
const write = (dir, rel, data) => {
  const f = path.join(dir, rel)
  fs.mkdirSync(path.dirname(f), { recursive: true })
  fs.writeFileSync(f, data)
}

/** A dsh @deepseek-ai directory: dsh-web-app's patch with the web surface's disables, dsh-base's rows. */
function scope() {
  const dir = tmp()
  write(dir, 'dsh-web-app/cordis.patch.yml', '- id: system-prompt\n  config:\n    personaPrefix: x\n\n- id: tool-bash\n  disabled: true\n\n- id: tool-fs\n  disabled: true\n\n- insert:\n    - id: agent-preset-registry\n      name: \'@deepseek-ai/dsh-agent-preset-registry\'\n')
  write(dir, 'dsh-base/cordis.patch.yml', '- insert:\n    - id: llm-deepseek\n      name: \'@deepseek-ai/dsh-llm-deepseek\'\n    - id: agent-default-model\n      name: \'@deepseek-ai/dsh-agent-default-model\'\n')
  return dir
}

const PRESET = {
  id: 'omd-ptc',
  plugins: `- id: tool-executor
  name: oh-my-dsh/ptc
- id: persona
  name: '@deepseek-ai/dsh-persona'
  config:
    prefix: You are OMD.
- id: tool-fs
  name: '@deepseek-ai/dsh-tool-fs'
`,
}

const BUNDLES = [
  { name: 'dsh-remote-iridium', patches: ['- insert:\n    - id: remote-iridium\n      name: dsh-remote-iridium\n'] },
  { name: 'other', patches: ['- id: x\n  disabled: true\n'] },
  {
    name: 'oh-my-dsh',
    patches: [
      "- insert:\n    - id: oh-my-dsh\n      name: oh-my-dsh\n    - id: omd-x\n      name: '@deepseek-ai/dsh-tool-web'\n- id: jobs\n  disabled: true\n",
      "# presets\n- insert:\n    - id: preset-omd\n      name: '@deepseek-ai/dsh-agent-preset'\n      config:\n        id: omd\n        plugins:\n          - id: a\n            name: oh-my-dsh/agent\n\n# more\n- insert:\n    - id: preset-b\n      name: '@deepseek-ai/dsh-agent-preset'\n      config:\n        id: b\n        plugins: []\n    - id: kept\n      name: oh-my-dsh/kept\n",
    ],
  },
]

test("a preset of Desktop's bundles: the bundles its rows name, a patch that binds each session to it", () => {
  assert.deepEqual(bound({ id: 'standard', plugins: "- id: tool-fs\n  name: '@deepseek-ai/dsh-tool-fs'\n" }, BUNDLES), [])
  const b = bound(PRESET, BUNDLES)
  assert.deepEqual(b.map((x) => x.name), ['oh-my-dsh'])
  const s = boundPatch(scope(), PRESET, 'Use ir.', 'file:///s/dsh-modules.mjs', 'file:///s/dsh-preset.mjs', b)
  assert.ok(
    s.startsWith(`# Iridium: dsh's "omd-ptc" Agent preset, bound to each session by the sdk profile over DSH Desktop's bundles (oh-my-dsh).
# Generated; do not edit.
- insert:
    - id: agent-preset-registry
      name: '@deepseek-ai/dsh-agent-preset-registry'
      config:
        default: "omd-ptc"
`),
    s,
  )
  for (const w of [
    '\n- id: tool-bash\n  disabled: true\n',
    "# oh-my-dsh, patch 1\n- insert:\n    - id: oh-my-dsh\n      name: 'file:///s/dsh-modules.mjs?oh-my-dsh'\n    - id: omd-x\n      name: '@deepseek-ai/dsh-tool-web'\n- id: jobs\n  disabled: true\n",
    "# oh-my-dsh, patch 2\n# presets\n# more\n- insert:\n    - id: kept\n      name: 'file:///s/dsh-modules.mjs?oh-my-dsh%2Fkept'\n",
    '- id: system-prompt\n  config:\n    personaPrefix: You are OMD.\n    personaSuffix: |-\n      Use ir.\n',
    "- insert:\n    - id: iridium-preset\n      name: '@deepseek-ai/dsh-agent-preset'\n      config:\n        id: \"omd-ptc\"\n        plugins:\n          - id: tool-executor\n            name: 'file:///s/dsh-modules.mjs?oh-my-dsh%2Fptc'\n          - id: tool-fs\n            name: '@deepseek-ai/dsh-tool-fs'\n    - id: iridium-preset-binder\n      name: 'file:///s/dsh-preset.mjs'\n      config:\n        preset: \"omd-ptc\"\n",
  ])
    assert.ok(s.includes(w), `no ${JSON.stringify(w)} in\n${s}`)
  for (const no of ['remote-iridium', 'id: x\n', 'id: preset-omd', 'id: preset-b', 'oh-my-dsh/agent', 'iridium-tool']) assert.ok(!s.includes(no), `${no} in\n${s}`)
})

test("names through the modules file as the Iridium's were, {{ in its prompt apart", () => {
  assert.equal(moduleName('file:///s/m.mjs', 'oh-my-dsh/host/tool-bash'), "'file:///s/m.mjs?oh-my-dsh%2Fhost%2Ftool-bash'")
  assert.equal(moduleName('file:///s/m.mjs', '@scope/pkg/x'), "'file:///s/m.mjs?@scope%2Fpkg%2Fx'")
  assert.deepEqual(externalPackages({ plugins: '- id: a\n  name: oh-my-dsh/ptc\n- id: b\n  name: "@s/p/x"\n- id: c\n  name: cordis:group\n- id: d\n  name: \'@deepseek-ai/dsh-tool-fs\'\n' }), ['oh-my-dsh', '@s/p'])
  const s = boundPatch(scope(), { id: 'p', plugins: "- id: persona\n  name: '@deepseek-ai/dsh-persona'\n  config:\n    suffix: |-\n      Own.\n    complete: true\n- id: t\n  name: o/x\n" }, 'Say {{x}}.', 'file:///m', 'file:///b', [{ name: 'o', patches: [] }])
  assert.ok(s.includes('    includeHarnessIdentity: false\n    personaSuffix: |-\n      Own.\n\n      Say { {x}}.\n'), s)
})

test('the binder and modules files as the Iridium shipped them load in node', () => {
  const home = tmp()
  write(home, 'profiles/b/node_modules/omd/package.json', '{"name":"omd","exports":{".":"./i.mjs","./host/*":{"types":"./t","import":"./lib/*.mjs"}}}')
  write(home, 'profiles/b/node_modules/omd/lib/tool-x.mjs', "export const name = 'x'\nexport function apply() {}\n")
  const url = pathToFileURL(new URL('../prepare/modules.mjs', import.meta.url).pathname).href
  const out = execFileSync(process.execPath, ['--input-type=module', '-e', `console.log((await import(process.argv[1] + '?' + encodeURIComponent('omd/host/tool-x'))).default.name)`, url], { env: { ...process.env, DSH_HOME: home } })
  assert.equal(String(out).trim(), 'x')
  const preset = fs.readFileSync(new URL('../prepare/preset.mjs', import.meta.url), 'utf8')
  assert.match(preset, /export const name = 'iridium-preset'/)
})

test('agent.prepare: the models, the session parts, a preset of the bundles bound; presets as registered', async () => {
  const sc = scope()
  const state = tmp()
  const profile = tmp()
  write(profile, 'package.json', JSON.stringify({ dsh: { profile: { bundles: ['@deepseek-ai/dsh-web-app', 'oh-my-dsh', 'omd-iridium', 'dsh-remote-iridium'] } } }))
  write(profile, 'node_modules/oh-my-dsh/package.json', JSON.stringify({ name: 'oh-my-dsh', version: '1.3.9', dsh: { bundle: { patch: './p.yml' } } }))
  write(profile, 'node_modules/oh-my-dsh/p.yml', '- insert:\n    - id: oh-my-dsh\n      name: oh-my-dsh\n')
  write(profile, 'node_modules/omd-iridium/package.json', JSON.stringify({ name: 'omd-iridium', iridium: { session: './index.js' } }))
  const reg = {
    list: async () => [{ id: 'standard' }, { id: 'omd-ptc', name: 'OMD PTC' }],
    readDocument: async (id) => ({ content: id === 'omd-ptc' ? PRESET.plugins : "- id: tool-fs\n  name: '@deepseek-ai/dsh-tool-fs'\n" }),
    defaultId: 'standard',
  }
  const loader = { *entries() { yield { options: { id: 'llm-deepseek', name: '@deepseek-ai/dsh-llm-deepseek', config: { apiKey: 'k' } } } } }
  const services = { agentPresets: reg, profileContext: { dir: profile }, loader }
  const ctx = { get: (n) => services[n] }
  const bridge = { endpoint: '/tmp/b.sock', secret: 's' }
  const m = dshMethods(ctx, undefined, { file: path.join(state, 'llm.patch.yml'), scope: sc }, { bridge, sessionFile: path.join(state, 'session.patch.yml'), dir: path.join(state, 'prepared') })
  await assert.rejects(m['agent.prepare']({ agent: 'claude' }), { code: 'unsupported' })
  assert.equal((await m['agent.presets']({ agent: 'dsh' })).presets[1].plugins, PRESET.plugins)
  // The models alone, without a session.
  assert.deepEqual(await m['agent.prepare']({ agent: 'dsh' }), { patches: [path.join(state, 'llm.patch.yml')], preset: false })
  assert.match(fs.readFileSync(path.join(state, 'llm.patch.yml'), 'utf8'), /- id: llm-deepseek\n  config: \{"apiKey": "k"\}/)
  // A preset of dsh's own: the Iridium makes it.
  let r = await m['agent.prepare']({ agent: 'dsh', session: 's1', cwd: '/w' })
  assert.deepEqual(r, { patches: [path.join(state, 'llm.patch.yml'), path.join(state, 'session.patch.yml')], preset: false })
  const sp = fs.readFileSync(path.join(state, 'session.patch.yml'), 'utf8')
  assert.ok(sp.includes('id: "remote-iridium-session-omd-iridium"') && sp.includes(pathToFileURL(path.join(profile, 'node_modules/omd-iridium/index.js')).href), sp)
  // OMD's: bound here, the Iridium's prompt after its persona.
  r = await m['agent.prepare']({ agent: 'dsh', session: 's2', preset: 'omd-ptc', system: 'Use ir.' })
  assert.equal(r.preset, true)
  const file = r.patches.at(-1)
  assert.equal(file, path.join(state, 'prepared', 's2.patch.yml'))
  const text = fs.readFileSync(file, 'utf8')
  assert.ok(text.includes('over DSH Desktop\'s bundles (oh-my-dsh)') && text.includes('      Use ir.\n') && text.includes('prepare/modules.mjs?oh-my-dsh%2Fptc'), text)
  await assert.rejects(m['agent.prepare']({ agent: 'dsh', session: '../x' }), { code: 'bad_request' })
})
