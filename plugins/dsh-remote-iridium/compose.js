// A preset of DSH Desktop's bundles (OMD's omd-ptc, oh-my-dsh) for a dsh process the Iridium
// starts here (`dsh --profile sdk`, whose profile has none of this one's bundles): the bundle's own
// layer, and a preset scope, which OMD's PTC executor requires and only a bound preset has. The
// patch composes as Desktop does: the preset registry, the web surface's disables, each bound
// bundle's layer, the preset's declaration with the Iridium's system prompt after its persona,
// and prepare/preset.mjs binding every session to it. Names from packages outside dsh's
// installation go through prepare/modules.mjs, which finds them in dsh's profiles. (Moved here
// from the Iridium, its internal/agent/dsh bound.go; verified there against dsh 0.2.2-rc.1 with
// OMD 1.3.9: omd-ptc's agent has 63 tools under OMD's PTC executor.) Apart from index.js so that
// it loads without dsh's packages (the tests).
import fs from 'node:fs'
import path from 'node:path'

const nameLine = /^\s*name: '?([^'\s]+)'?\s*$/
const rowName = /^(\s*)(- )?name: ["']?([^"'\s]+)["']?\s*$/
const rowLine = /^( *)- id: ([A-Za-z0-9_-]+)\s*$/
const presetName = /^\s*name: ["']?@deepseek-ai\/dsh-agent-preset["']?\s*$/
const topDisabled = /^- id: ([A-Za-z0-9_-]+)\s*$/
const isDisabled = /^\s+disabled: true\s*$/
const insertLine = /^- insert:\s*$/

/** This plugin's own package, never composed into a session (it would connect again). */
const OWN = 'dsh-remote-iridium'

const blank = (l) => l.trim() === ''
const indent = (l) => l.length - l.replace(/^ +/, '').length
const lf = (s) => String(s ?? '').replaceAll('\r\n', '\n')

/** Whether dsh's installation resolves a plugin name itself (or it is no package name at all). */
export const installed = (name) => name.startsWith('@deepseek-ai/') || /[:\\]/.test(name) || name.startsWith('.') || name.startsWith('/')

/** The package a bare plugin name is in. */
export function packageOf(name) {
  const n = name.startsWith('@') ? 2 : 1
  return name.split('/').slice(0, n).join('/')
}

/** Go's url.PathEscape: a path segment, "/", ";", "," and "?" escaped too. */
export function pathEscape(s) {
  let out = ''
  for (const b of Buffer.from(s, 'utf8')) {
    const c = String.fromCharCode(b)
    if (/[A-Za-z0-9\-_.~$&+:=@]/.test(c)) out += c
    else out += '%' + b.toString(16).toUpperCase().padStart(2, '0')
  }
  return out
}

/** name's row name through the modules file at modules (a file: URL), quoted. */
export const moduleName = (modules, name) => "'" + (modules + '?' + pathEscape(name)).replaceAll("'", "''") + "'"

/** v as JSON (a YAML flow value), <, > and & as they are. */
export const quote = (v) => JSON.stringify(v)

/** A file: URL as a YAML single-quoted scalar. */
const moduleQuote = (u) => "'" + u.replaceAll("'", "''") + "'"

/** The packages outside dsh's installation preset's rows name. */
export function externalPackages(preset) {
  const out = []
  for (const l of lf(preset.plugins).split('\n')) {
    const m = l.match(rowName)
    if (m && !installed(m[3]) && !out.includes(packageOf(m[3]))) out.push(packageOf(m[3]))
  }
  return out
}

/** The bundles among bundles ({name, patches}) whose packages preset's rows name. */
export function bound(preset, bundles) {
  const pkgs = externalPackages(preset)
  return bundles.filter((b) => b.name !== OWN && pkgs.includes(b.name))
}

/** The keys at indent ind in a block's lines, each with its lines: its own line's rest first. */
function keys(lines, ind) {
  const out = new Map()
  let key = ''
  for (const l of lines) {
    if (!blank(l) && indent(l) < ind) break
    if (!blank(l) && indent(l) === ind && !l.trim().startsWith('#')) {
      const t = l.trim()
      const i = t.indexOf(':')
      if (i < 0) {
        key = ''
        continue
      }
      key = t.slice(0, i)
      out.set(key, [t.slice(i + 1)])
      continue
    }
    if (key !== '') out.get(key).push(l)
  }
  return out
}

const tail = (b) => (b?.length ? b.slice(1) : [])

/** A YAML scalar starting after "key:" (b[0]) and going on in b[1:]: plain, quoted or a literal block. */
export function scalar(b) {
  const head = b[0].trim()
  if (head === '|' || head === '|-' || head === '|+') {
    let ind = -1
    const out = []
    for (const l of b.slice(1)) {
      if (l.trim() === '') {
        out.push('')
        continue
      }
      if (ind < 0) ind = indent(l)
      out.push(l.slice(Math.min(ind, indent(l))))
    }
    return out.join('\n').replace(/\n+$/, '')
  }
  if (b.length > 1) throw new Error('a scalar this generator cannot read')
  if (head.length >= 2 && head[0] === "'" && head.at(-1) === "'") return head.slice(1, -1).replaceAll("''", "'")
  if (head.length >= 2 && head[0] === '"' && head.at(-1) === '"') return JSON.parse(head)
  return head
}

function scalarText(b) {
  if (!b?.length) return ''
  const h = b[0].trim()
  if (h.startsWith('>')) return b.slice(1).map((l) => l.trim()).join(' ').trim()
  try {
    return scalar(b)
  } catch {
    return ''
  }
}

/** Two persona texts joined by a blank line; "{{" in the second goes apart (dsh reads it as a variable). */
export function joinText(a, b) {
  b = lf(b).trim()
  while (b.includes('{{')) b = b.replaceAll('{{', '{ {')
  if (a === '') return b
  if (b === '') return a
  return a + '\n\n' + b
}

/**
 * preset's rows less its persona row, and that row as the system-prompt row's config lines, with
 * suffix (the Iridium's system prompt) after the persona's own.
 */
export function personaOf(preset, suffix) {
  let body = lf(preset.plugins).replace(/\n+$/, '').split('\n')
  if (!lf(preset.plugins).trim()) body = []
  const persona = []
  let presetSuffix = ''
  let at = -1
  for (let i = 0; i < body.length && at < 0; i++) {
    if (!body[i].startsWith('- ')) continue
    for (let j = i; j < body.length && (j === i || blank(body[j]) || indent(body[j]) > 0); j++) {
      const l = j === i ? '  ' + body[j].slice(2) : body[j]
      const n = l.match(nameLine)
      if (n && indent(l) === 2 && n[1] === '@deepseek-ai/dsh-persona') {
        at = i
        break
      }
    }
  }
  if (at >= 0) {
    let end = at + 1
    while (end < body.length && (blank(body[end]) || indent(body[end]) > 0)) end++
    const rows = body.slice(at, end)
    body = [...body.slice(0, at), ...body.slice(end)]
    const cfg = keys(tail(keys(rows.slice(1), 2).get('config')), 4)
    const prefix = cfg.get('prefix')
    if (prefix) persona.push('    personaPrefix:' + prefix[0], ...prefix.slice(1))
    const suf = cfg.get('suffix')
    if (suf) {
      try {
        presetSuffix = scalar(suf)
      } catch (e) {
        throw new Error(`preset ${preset.id}: persona suffix: ${e.message}`)
      }
    }
    if (scalarText(cfg.get('includeRuntimeContext')).trim() === 'false') persona.push('    includeRuntimeContext: false')
    if (scalarText(cfg.get('complete')).trim() === 'true') persona.push('    includeHarnessIdentity: false')
  }
  const s = joinText(presetSuffix, suffix ?? '')
  if (s !== '') {
    persona.push('    personaSuffix: |-')
    for (const l of s.split('\n')) persona.push(l === '' ? '' : '      ' + l)
  }
  return { body, persona }
}

/** The rows the web surface turns off (its per-agent rows move behind presets), sorted. */
export function webOff(scope) {
  const web = lf(fs.readFileSync(path.join(scope, 'dsh-web-app', 'cordis.patch.yml'), 'utf8')).split('\n')
  const off = new Set()
  for (let i = 0; i < web.length - 1; i++) {
    const m = web[i].match(topDisabled)
    if (m && isDisabled.test(web[i + 1])) off.add(m[1])
  }
  return [...off].sort()
}

/** lines with the names of packages outside dsh's installation through modules. */
export function rename(lines, modules) {
  return lines.map((l) => {
    const m = l.match(rowName)
    return m && !installed(m[3]) && modules ? m[1] + (m[2] ?? '') + 'name: ' + moduleName(modules, m[3]) : l
  })
}

/**
 * A bundle's patch file for the sdk profile: less its preset declarations (the session's preset
 * comes from the registry, as it is now) and an insert left empty by that, its names through modules.
 */
export function bundleLayer(text, modules) {
  const ls = lf(text).replace(/\n+$/, '').split('\n')
  const kept = []
  for (let i = 0; i < ls.length; i++) {
    const m = ls[i].match(rowLine)
    if (!m) {
      kept.push(ls[i])
      continue
    }
    const ind = m[1].length + 2
    let end = i + 1
    while (end < ls.length && (blank(ls[end]) || indent(ls[end]) >= ind)) end++
    if (!ls.slice(i + 1, end).some((l) => indent(l) === ind && presetName.test(l))) {
      kept.push(ls[i])
      continue
    }
    i = end - 1
  }
  const out = []
  for (let i = 0; i < kept.length; i++) {
    if (insertLine.test(kept[i])) {
      let j = i + 1
      while (j < kept.length && (blank(kept[j]) || kept[j].trim().startsWith('#'))) j++
      if (j === kept.length || indent(kept[j]) === 0) continue
    }
    out.push(kept[i])
  }
  return rename(out, modules)
}

/**
 * The --patch file that binds every session to preset over bundles (bound's), with suffix (the
 * Iridium's system prompt) after the preset's own persona suffix; scope is dsh's @deepseek-ai
 * directory, modules and binder prepare/modules.mjs's and prepare/preset.mjs's file: URLs.
 */
export function boundPatch(scope, preset, suffix, modules, binder, bundles) {
  const { body, persona } = personaOf(preset, suffix)
  const off = webOff(scope)
  const id = quote(preset.id)
  const out = [
    `# Iridium: dsh's ${quote(preset.id)} Agent preset, bound to each session by the sdk profile over DSH Desktop's bundles (${bundles.map((b) => b.name).join(', ')}).`,
    '# Generated; do not edit.',
    '- insert:',
    '    - id: agent-preset-registry',
    "      name: '@deepseek-ai/dsh-agent-preset-registry'",
    '      config:',
    '        default: ' + id,
  ]
  for (const x of off) out.push('- id: ' + x, '  disabled: true')
  for (const b of bundles) {
    b.patches.forEach((text, i) => {
      out.push(`# ${b.name}, patch ${i + 1}`, ...bundleLayer(text, modules))
    })
  }
  if (persona.length) out.push('- id: system-prompt', '  config:', ...persona)
  out.push('- insert:', '    - id: iridium-preset', "      name: '@deepseek-ai/dsh-agent-preset'", '      config:', '        id: ' + id)
  if (!body.length) out.push('        plugins: []')
  else {
    out.push('        plugins:')
    for (const l of rename(body, modules)) out.push(blank(l) ? l : '          ' + l)
  }
  out.push('    - id: iridium-preset-binder', '      name: ' + moduleQuote(binder), '      config:', '        preset: ' + id)
  return out.join('\n').replace(/\n+$/, '') + '\n'
}
