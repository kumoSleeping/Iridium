package dsh

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// A preset may name plugins from packages outside dsh's installation: those of a bundle or plugin
// DSH Desktop has in its web profile (OMD's oh-my-dsh/ptc, oh-my-dsh/host/tool-pwsh). The sdk
// profile resolves bare names from the installation and its own directory only, so such a row
// fails there ("Cannot find package"). Its name goes through ModulesModule instead, which finds
// the package in dsh's profiles where the agent runs; and a base row it replaces stays on unless
// the package is there (Iridium cannot read the profiles: the dsh plugin keeps them from it).

// ModulesFile is ModulesModule's name, written beside the patch.
const ModulesFile = "dsh-modules.mjs"

// ModulesModule loads the plugin module its URL's query names from the profiles of dsh's home
// (DSH_HOME, else ~/.dsh, as dsh has it): the web profile's (DSH Desktop's) first, then the rest
// by name; its default export is the plugin as dsh's loader would unwrap the module itself.
const ModulesModule = `// Iridium: a plugin module from dsh's profiles, which the sdk profile cannot resolve.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const spec = decodeURIComponent(new URL(import.meta.url).search.slice(1))
const parts = spec.split('/')
const pkg = parts.slice(0, spec.startsWith('@') ? 2 : 1).join('/')
const sub = '.' + spec.slice(pkg.length)
const home = process.env.DSH_HOME?.trim() ? process.env.DSH_HOME : path.join(os.homedir(), '.dsh')
const profiles = path.resolve(home, 'profiles')

// A package.json exports target under Node's import conditions.
function pick(t) {
  if (typeof t === 'string') return t
  if (Array.isArray(t)) {
    for (const x of t) {
      const r = pick(x)
      if (r !== undefined) return r
    }
    return
  }
  if (t && typeof t === 'object') {
    for (const [k, v] of Object.entries(t)) {
      if (k === 'node' || k === 'import' || k === 'default') {
        const r = pick(v)
        if (r !== undefined) return r
      }
    }
  }
}

// The file of subpath sub in the package at dir, relative to it.
function entry(dir, sub) {
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
  let ex = manifest.exports
  if (ex === undefined || ex === null) {
    if (sub === '.') return manifest.main ?? 'index.js'
    return ['', '.js', '.mjs'].map((e) => sub + e).find((f) => fs.existsSync(path.join(dir, f)))
  }
  if (typeof ex === 'string' || Array.isArray(ex) || !Object.keys(ex).some((k) => k.startsWith('.'))) ex = { '.': ex }
  if (Object.hasOwn(ex, sub)) return pick(ex[sub])
  let best
  for (const k of Object.keys(ex)) {
    const i = k.indexOf('*')
    if (i < 0) continue
    const pre = k.slice(0, i), post = k.slice(i + 1)
    if (sub.length >= pre.length + post.length && sub.startsWith(pre) && sub.endsWith(post) && (!best || pre.length > best.pre.length)) best = { k, pre, post }
  }
  return best && pick(ex[best.k])?.replaceAll('*', sub.slice(best.pre.length, sub.length - best.post.length))
}

let names = []
try {
  names = fs.readdirSync(profiles).sort()
} catch {}
let file
for (const p of ['web', ...names.filter((n) => n !== 'web')]) {
  const dir = path.join(profiles, p, 'node_modules', ...pkg.split('/'))
  if (!fs.existsSync(path.join(dir, 'package.json'))) continue
  const f = entry(dir, sub)
  if (f) {
    file = path.resolve(dir, f)
    break
  }
}
if (!file) throw new Error('iridium: no ' + spec + ' in the profiles at ' + profiles)
const m = await import(pathToFileURL(file).href)
let plugin = m.default ?? m
if (plugin?.__esModule) plugin = plugin.default ?? plugin
export default plugin
`

// installed reports whether dsh's installation resolves a plugin name itself (or it is no package
// name at all: a cordis: builtin, a path, a URL). Its own packages are @deepseek-ai/'s.
func installed(name string) bool {
	return strings.HasPrefix(name, "@deepseek-ai/") || strings.ContainsAny(name, ":\\") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/")
}

// packageOf is the package a bare plugin name is in.
func packageOf(name string) string {
	n := 1
	if strings.HasPrefix(name, "@") {
		n = 2
	}
	parts := strings.SplitN(name, "/", n+1)
	return strings.Join(parts[:min(n, len(parts))], "/")
}

// moduleName is name's row name through the modules file at modules (a file: URL), quoted.
func moduleName(modules, name string) string {
	return "'" + strings.ReplaceAll(modules+"?"+url.PathEscape(name), "'", "''") + "'"
}

// foundExpr is a loader expression: whether every package in pkgs is in one of dsh's profiles
// where the agent runs, as ModulesModule looks; or else what the row's own disabled (a YAML value
// as written, "" for none) says.
func foundExpr(pkgs []string, own string) string {
	switch own = strings.TrimSpace(own); {
	case own == "true":
		return "true"
	case strings.HasPrefix(own, "!!js "):
		if e, err := scalar([]string{" " + strings.TrimSpace(own[len("!!js "):])}); err == nil {
			own = e
		} else {
			own = ""
		}
	default:
		own = ""
	}
	slices.Sort(pkgs)
	list := quote(slices.Compact(pkgs))
	js := fmt.Sprintf(`((fs, path) => { const home = process.env.DSH_HOME?.trim() ? process.env.DSH_HOME : path.join(process.getBuiltinModule('node:os').homedir(), '.dsh'); const root = path.resolve(home, 'profiles'); let ds = []; try { ds = fs.readdirSync(root) } catch {} return %s.every((p) => ds.some((d) => fs.existsSync(path.join(root, d, 'node_modules', ...p.split('/'), 'package.json')))) })(process.getBuiltinModule('node:fs'), process.getBuiltinModule('node:path'))`, list)
	if own != "" {
		js = "(" + own + ") || " + js
	}
	return "!!js " + quote(js)
}

// quote is v as JSON (a YAML flow value) with <, > and & as they are.
func quote(v any) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}
