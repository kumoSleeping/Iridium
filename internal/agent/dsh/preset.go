package dsh

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// Agent presets. dsh registers them as @deepseek-ai/dsh-agent-preset rows: dsh-web-app ships
// some (presets/<id>.patch.yml, listed in its package.json), a bundle or the profile may add more;
// and only its web profile binds one per session: the sdk profile never does. So a preset runs
// process-wide, through a --patch file made at every spawn from the preset's child rows as dsh
// has them now (the files, or DSH Desktop's own registry through the dsh plugin): the base rows
// the web surface moves behind presets are disabled, and so are base rows a preset row shares an
// id or a package with; the preset's persona folds into the system-prompt row; PTC's executor
// replaces the native one; then the preset's plugin rows go in, their ids prefixed so none meets a
// base row of the same name. (Iridium's /tmp/dsh-tools/gen-preset.mjs, verified against dsh
// 0.2.2-rc.1: standard 32 tools, ptc 1, minimal 9.)

// DefaultPreset is the preset of a session that names none, when dsh's registry says no default.
const DefaultPreset = "standard"

// PatchFile is the generated patch's name, written beside the session's system prompt.
const PatchFile = "dsh-patch.yml"

// rowPrefix starts the ids of the preset's rows.
const rowPrefix = "iridium-"

// Package is an installed dsh package: its root (where package.json says @deepseek-ai/dsh) and
// the directory of its @deepseek-ai packages.
type Package struct {
	Root, Scope, Version string
	// Read reads a file under Scope by its parts; nil: this machine's files.
	Read func(parts ...string) ([]byte, error)
}

// FindPackage finds the dsh package program runs: the program is <root>/lib/bin.js or a symlink
// to it (npm's bin), else a file under the root.
func FindPackage(program string) (Package, error) {
	real, err := filepath.EvalSymlinks(program)
	if err != nil {
		return Package{}, fmt.Errorf("dsh: %w", err)
	}
	for dir := filepath.Dir(real); ; dir = filepath.Dir(dir) {
		var pkg struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(b, &pkg) == nil && pkg.Name == "@deepseek-ai/dsh" {
			// Installed globally its packages are its own; in a project they may be hoisted beside it.
			for _, scope := range []string{filepath.Join(dir, "node_modules", "@deepseek-ai"), filepath.Dir(dir)} {
				if st, err := os.Stat(filepath.Join(scope, "dsh-web-app", "presets")); err == nil && st.IsDir() {
					return Package{Root: dir, Scope: scope, Version: pkg.Version}, nil
				}
			}
			return Package{}, fmt.Errorf("dsh: no dsh-web-app presets in the package at %s", dir)
		}
		if dir == filepath.Dir(dir) {
			return Package{}, fmt.Errorf("dsh: %s is in no @deepseek-ai/dsh package", real)
		}
	}
}

var (
	idLine      = regexp.MustCompile(`^(\s*)(- )?id: ([A-Za-z0-9_-]+)\s*$`)
	nameLine    = regexp.MustCompile(`^\s*name: '?([^'\s]+)'?\s*$`)
	topDisabled = regexp.MustCompile(`^- id: ([A-Za-z0-9_-]+)\s*$`)
	isDisabled  = regexp.MustCompile(`^\s+disabled: true\s*$`)
	rowLine     = regexp.MustCompile(`^( *)- id: ([A-Za-z0-9_-]+)\s*$`)
	presetName  = regexp.MustCompile(`^\s*name: ["']?@deepseek-ai/dsh-agent-preset["']?\s*$`)
	defaultLine = regexp.MustCompile(`^\s+default: ["']?([A-Za-z0-9_-]+)["']?\s*$`)
)

func (p Package) readFile(parts ...string) ([]byte, error) {
	if p.Read != nil {
		return p.Read(parts...)
	}
	return os.ReadFile(filepath.Join(append([]string{p.Scope}, parts...)...))
}

func (p Package) lines(parts ...string) ([]string, error) {
	b, err := p.readFile(parts...)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), nil
}

// Presets are the agent presets the package registers, as dsh's registry lists them (by order,
// then id), and the registry's default: the @deepseek-ai/dsh-agent-preset rows in the patches
// dsh-web-app's package.json lists (dsh.bundle.patch), less those a later one disables.
func (p Package) Presets() ([]agent.Preset, string, error) {
	b, err := p.readFile("dsh-web-app", "package.json")
	if err != nil {
		return nil, "", fmt.Errorf("dsh: agent presets: %w", err)
	}
	var pkg struct {
		Dsh struct {
			Bundle struct {
				Patch []string `json:"patch"`
			} `json:"bundle"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return nil, "", fmt.Errorf("dsh: agent presets: dsh-web-app's package.json: %w", err)
	}
	if len(pkg.Dsh.Bundle.Patch) == 0 {
		pkg.Dsh.Bundle.Patch = []string{"cordis.patch.yml"}
	}
	type row struct {
		agent.Preset
		key   string // the declaration row's id
		order float64
	}
	var rows []row
	def := ""
	off := map[string]bool{}
	for _, f := range pkg.Dsh.Bundle.Patch {
		f = path.Clean(strings.ReplaceAll(f, "\\", "/"))
		if path.IsAbs(f) || strings.HasPrefix(f, "../") {
			continue
		}
		ls, err := p.lines(append([]string{"dsh-web-app"}, strings.Split(f, "/")...)...)
		if err != nil {
			return nil, "", fmt.Errorf("dsh: agent presets: %w", err)
		}
		for i := 0; i+1 < len(ls); i++ {
			if m := topDisabled.FindStringSubmatch(ls[i]); m != nil && isDisabled.MatchString(ls[i+1]) {
				off[m[1]] = true
			}
			m := rowLine.FindStringSubmatch(ls[i])
			if m == nil {
				continue
			}
			ind := len(m[1]) + 2 // the row's keys
			end := i + 1
			for end < len(ls) && (blank(ls[end]) || indent(ls[end]) >= ind) {
				end++
			}
			r := ls[i+1 : end]
			if m[2] == "agent-preset-registry" {
				for _, l := range r {
					if d := defaultLine.FindStringSubmatch(l); d != nil {
						def = d[1]
					}
				}
				continue
			}
			if !presetName.MatchString(ls[i+1]) || indent(ls[i+1]) != ind {
				continue
			}
			cfg := keys(tail(keys(r, ind)["config"]), ind+2)
			pr := row{Preset: agent.Preset{ID: scalarText(cfg["id"]), Name: scalarText(cfg["name"]), Description: scalarText(cfg["description"])}, key: m[2], order: math.Inf(1)}
			if o, err := strconv.ParseFloat(scalarText(cfg["order"]), 64); err == nil {
				pr.order = o
			}
			if pr.ID == "" {
				continue
			}
			if pl := cfg["plugins"]; len(pl) > 1 {
				var out []string
				for _, l := range pl[1:] {
					out = append(out, l[min(ind+4, indent(l)):])
				}
				pr.Plugins = strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
			}
			rows = slices.DeleteFunc(rows, func(x row) bool { return x.ID == pr.ID })
			rows = append(rows, pr)
		}
	}
	rows = slices.DeleteFunc(rows, func(x row) bool { return off[x.key] })
	slices.SortStableFunc(rows, func(a, b row) int {
		if a.order != b.order {
			if a.order < b.order {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	out := make([]agent.Preset, len(rows))
	for i, r := range rows {
		out[i] = r.Preset
	}
	return out, def, nil
}

// FindPreset is the preset of id among those dsh registers.
func FindPreset(presets []agent.Preset, id string) (agent.Preset, error) {
	if i := slices.IndexFunc(presets, func(p agent.Preset) bool { return p.ID == id }); i >= 0 {
		return presets[i], nil
	}
	ids := make([]string, len(presets))
	for i, p := range presets {
		ids[i] = p.ID
	}
	return agent.Preset{}, fmt.Errorf("dsh: no agent preset %q (%s)", id, strings.Join(ids, ", "))
}

func blank(l string) bool { return strings.TrimSpace(l) == "" }

func indent(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

// keys are the keys at indent ind in a block's lines (a mapping's), each with its lines: the
// rest of its own line first, then those under it.
func keys(lines []string, ind int) map[string][]string {
	out := map[string][]string{}
	key := ""
	for _, l := range lines {
		if !blank(l) && indent(l) < ind {
			break
		}
		if !blank(l) && indent(l) == ind && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
			if !ok {
				key = ""
				continue
			}
			key = k
			out[key] = []string{v}
			continue
		}
		if key != "" {
			out[key] = append(out[key], l)
		}
	}
	return out
}

// tail is a key's lines under it.
func tail(b []string) []string {
	if len(b) == 0 {
		return nil
	}
	return b[1:]
}

// scalarText is a scalar's text, a folded one's lines joined; "" if there is none.
func scalarText(b []string) string {
	if len(b) == 0 {
		return ""
	}
	if h := strings.TrimSpace(b[0]); strings.HasPrefix(h, ">") {
		var ws []string
		for _, l := range b[1:] {
			ws = append(ws, strings.TrimSpace(l))
		}
		return strings.TrimSpace(strings.Join(ws, " "))
	}
	s, err := scalar(b)
	if err != nil {
		return ""
	}
	return s
}

// Patch is the --patch file that runs preset process-wide, with suffix (Iridium's system prompt)
// after the preset's own persona suffix.
func (p Package) Patch(preset agent.Preset, suffix string) ([]byte, error) {
	read := func(parts ...string) ([]string, error) {
		ls, err := p.lines(parts...)
		if err != nil {
			return nil, fmt.Errorf("dsh: preset %s: %w", preset.ID, err)
		}
		return ls, nil
	}
	body := strings.Split(strings.TrimRight(strings.ReplaceAll(preset.Plugins, "\r\n", "\n"), "\n"), "\n")
	if strings.TrimSpace(preset.Plugins) == "" {
		body = nil
	}

	// The persona row registers the prompt's persona sections, which unscoped are the
	// system-prompt row's own: it becomes that row's config. complete (the persona is the whole
	// prompt) comes as near as it can: no harness identity line.
	var persona []string
	presetSuffix := ""
	at := -1
	for i := 0; i < len(body) && at < 0; i++ {
		if !strings.HasPrefix(body[i], "- ") {
			continue
		}
		// A row's name: on its first line, or a key under it.
		for j := i; j < len(body) && (j == i || blank(body[j]) || indent(body[j]) > 0); j++ {
			l := body[j]
			if j == i {
				l = "  " + l[2:]
			}
			if n := nameLine.FindStringSubmatch(l); n != nil && indent(l) == 2 && n[1] == "@deepseek-ai/dsh-persona" {
				at = i
				break
			}
		}
	}
	if at >= 0 {
		end := at + 1
		for end < len(body) && (blank(body[end]) || indent(body[end]) > 0) {
			end++
		}
		rows := slices.Clone(body[at:end])
		body = slices.Delete(body, at, end)
		cfg := keys(tail(keys(rows[1:], 2)["config"]), 4)
		if b := cfg["prefix"]; b != nil {
			persona = append(persona, "    personaPrefix:"+b[0])
			persona = append(persona, b[1:]...)
		}
		if b := cfg["suffix"]; b != nil {
			var err error
			if presetSuffix, err = scalar(b); err != nil {
				return nil, fmt.Errorf("dsh: preset %s: persona suffix: %w", preset.ID, err)
			}
		}
		if strings.TrimSpace(scalarText(cfg["includeRuntimeContext"])) == "false" {
			persona = append(persona, "    includeRuntimeContext: false")
		}
		if strings.TrimSpace(scalarText(cfg["complete"])) == "true" {
			persona = append(persona, "    includeHarnessIdentity: false")
		}
	}
	if s := joinText(presetSuffix, suffix); s != "" {
		persona = append(persona, "    personaSuffix: |-")
		for _, l := range strings.Split(s, "\n") {
			if l == "" {
				persona = append(persona, "")
			} else {
				persona = append(persona, "      "+l)
			}
		}
	}

	ids := map[string]bool{}
	hoisted := make([]string, 0, len(body))
	for _, l := range body {
		if m := idLine.FindStringSubmatch(l); m != nil {
			ids[m[3]] = true
			l = m[1] + m[2] + "id: " + rowPrefix + m[3]
		}
		if !blank(l) {
			l = "    " + l
		}
		hoisted = append(hoisted, l)
	}
	// Every row in the bundles the sdk profile composes, by id and by the package it loads.
	baseIDs := map[string]bool{}
	baseByName := map[string][]string{}
	for _, bundle := range []string{"dsh-base", "dsh-sdk-app"} {
		ls, err := read(bundle, "cordis.patch.yml")
		if err != nil {
			return nil, err
		}
		for i, l := range ls {
			m := idLine.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			baseIDs[m[3]] = true
			if i+1 < len(ls) {
				if n := nameLine.FindStringSubmatch(ls[i+1]); n != nil {
					baseByName[n[1]] = append(baseByName[n[1]], m[3])
				}
			}
		}
	}
	names := map[string]bool{}
	for _, l := range body {
		if n := nameLine.FindStringSubmatch(l); n != nil && strings.HasPrefix(n[1], "@deepseek-ai/") {
			names[n[1]] = true
		}
	}
	// The web surface's own disables (its per-agent rows move behind presets), and any base row a
	// preset row shares an id or a package with.
	web, err := read("dsh-web-app", "cordis.patch.yml")
	if err != nil {
		return nil, err
	}
	off := map[string]bool{}
	for i := 0; i < len(web)-1; i++ {
		if m := topDisabled.FindStringSubmatch(web[i]); m != nil && isDisabled.MatchString(web[i+1]) {
			off[m[1]] = true
		}
	}
	for id := range ids {
		if baseIDs[id] {
			off[id] = true
		}
	}
	for n := range names {
		for _, id := range baseByName[n] {
			off[id] = true
		}
	}
	// One tool executor per scope: the preset's PTC executor replaces the native one.
	if names["@deepseek-ai/dsh-tool-executor-ptc"] {
		off["tool-executor-native"] = true
	}
	out := []string{fmt.Sprintf("# Iridium: dsh's %q Agent preset, run process-wide by the sdk profile.", preset.ID), "# Generated; do not edit."}
	offs := make([]string, 0, len(off))
	for id := range off {
		offs = append(offs, id)
	}
	slices.Sort(offs)
	for _, id := range offs {
		out = append(out, "- id: "+id, "  disabled: true")
	}
	if len(persona) > 0 {
		out = append(append(out, "- id: system-prompt", "  config:"), persona...)
	}
	out = append(append(out, "- insert:"), hoisted...)
	return []byte(strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"), nil
}

// scalar reads a YAML scalar that starts after "key:" (b[0]) and goes on in b[1:]: a plain or
// quoted line, or a literal block.
func scalar(b []string) (string, error) {
	head := strings.TrimSpace(b[0])
	switch {
	case head == "|" || head == "|-" || head == "|+":
		indent := -1
		var out []string
		for _, l := range b[1:] {
			if strings.TrimSpace(l) == "" {
				out = append(out, "")
				continue
			}
			if indent < 0 {
				indent = len(l) - len(strings.TrimLeft(l, " "))
			}
			out = append(out, l[min(indent, len(l)-len(strings.TrimLeft(l, " "))):])
		}
		return strings.TrimRight(strings.Join(out, "\n"), "\n"), nil
	case len(b) > 1:
		return "", errors.New("a scalar this generator cannot read")
	case len(head) >= 2 && head[0] == '\'' && head[len(head)-1] == '\'':
		return strings.ReplaceAll(head[1:len(head)-1], "''", "'"), nil
	case len(head) >= 2 && head[0] == '"' && head[len(head)-1] == '"':
		var s string
		if err := json.Unmarshal([]byte(head), &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return head, nil
}

// joinText joins two persona texts with a blank line. dsh reads {{name}} in them as a variable
// and fails the whole prompt on one it does not know; it has no escape, so "{{" goes apart.
func joinText(a, b string) string {
	b = strings.TrimSpace(strings.ReplaceAll(b, "\r\n", "\n"))
	for strings.Contains(b, "{{") {
		b = strings.ReplaceAll(b, "{{", "{ {")
	}
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n\n" + b
}
