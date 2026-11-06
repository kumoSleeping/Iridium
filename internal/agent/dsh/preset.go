package dsh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Agent presets. dsh-web-app ships them (presets/<id>.patch.yml), and only its web profile binds
// one per session: the sdk profile never does. So a preset runs process-wide, through a --patch
// file made from the installed package at every spawn, so it follows dsh's upgrades: the base rows
// the web surface moves behind presets are disabled, and so are base rows a preset row shares an
// id or a package with; the preset's persona folds into the system-prompt row; PTC's executor
// replaces the native one; then the preset's plugin rows go in, their ids prefixed so none meets a
// base row of the same name. (Iridium's /tmp/dsh-tools/gen-preset.mjs, verified against dsh
// 0.2.2-rc.1: standard 32 tools, ptc 1, minimal 9.)

// Presets are the agent presets Iridium offers (not dsh-web-app's cordis).
var Presets = []string{"standard", "ptc", "minimal"}

const DefaultPreset = "standard"

// PatchFile is the generated patch's name, written beside the session's system prompt.
const PatchFile = "dsh-patch.yml"

// rowPrefix starts the ids of the preset's rows.
const rowPrefix = "iridium-"

// Package is an installed dsh package: its root (where package.json says @deepseek-ai/dsh) and
// the directory of its @deepseek-ai packages.
type Package struct {
	Root, Scope, Version string
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
	rowStart    = regexp.MustCompile(`^ {10}- id: `)
	rowEnd      = regexp.MustCompile(`^ {10}- |^ {0,9}\S`)
	topDisabled = regexp.MustCompile(`^- id: ([A-Za-z0-9_-]+)\s*$`)
	isDisabled  = regexp.MustCompile(`^\s+disabled: true\s*$`)
)

// Patch is the --patch file that runs preset process-wide, with suffix (Iridium's system prompt)
// after the preset's own persona suffix.
func (p Package) Patch(preset, suffix string) ([]byte, error) {
	if !slices.Contains(Presets, preset) {
		return nil, fmt.Errorf("dsh: no agent preset %q (%s)", preset, strings.Join(Presets, ", "))
	}
	read := func(parts ...string) ([]string, error) {
		b, err := os.ReadFile(filepath.Join(append([]string{p.Scope}, parts...)...))
		if err != nil {
			return nil, fmt.Errorf("dsh: preset %s: %w", preset, err)
		}
		return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), nil
	}
	lines, err := read("dsh-web-app", "presets", preset+".patch.yml")
	if err != nil {
		return nil, err
	}
	start := slices.Index(lines, "        plugins:")
	if start < 0 {
		return nil, fmt.Errorf("dsh: preset %s: no plugins list", preset)
	}
	body := slices.Clone(lines[start+1:])

	// The persona row registers the prompt's persona sections, which unscoped are the
	// system-prompt row's own: it becomes that row's config. complete (the persona is the whole
	// prompt) comes as near as it can: no harness identity line.
	var persona []string
	presetSuffix := ""
	at := -1
	for i, l := range body {
		if rowStart.MatchString(l) && i+1 < len(body) && strings.Contains(body[i+1], "'@deepseek-ai/dsh-persona'") {
			at = i
			break
		}
	}
	if at >= 0 {
		end := at + 1
		for end < len(body) && !rowEnd.MatchString(body[end]) {
			end++
		}
		rows := slices.Clone(body[at:end])
		body = slices.Delete(body, at, end)
		block := func(key string) []string {
			re := regexp.MustCompile(`^ {14}` + key + `:`)
			i := slices.IndexFunc(rows, re.MatchString)
			if i < 0 {
				return nil
			}
			out := []string{re.ReplaceAllString(rows[i], "")}
			for j := i + 1; j < len(rows) && strings.HasPrefix(rows[j], strings.Repeat(" ", 16)); j++ {
				out = append(out, rows[j])
			}
			return out
		}
		if b := block("prefix"); b != nil {
			persona = append(persona, "    personaPrefix:"+b[0])
			for _, l := range b[1:] {
				persona = append(persona, strings.TrimPrefix(l, strings.Repeat(" ", 10)))
			}
		}
		if b := block("suffix"); b != nil {
			if presetSuffix, err = scalar(b); err != nil {
				return nil, fmt.Errorf("dsh: preset %s: persona suffix: %w", preset, err)
			}
		}
		cfg := strings.Join(rows, "\n")
		if regexp.MustCompile(`(?m)^ {14}includeRuntimeContext: false`).MatchString(cfg) {
			persona = append(persona, "    includeRuntimeContext: false")
		}
		if regexp.MustCompile(`(?m)^ {14}complete: true`).MatchString(cfg) {
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
		hoisted = append(hoisted, strings.TrimPrefix(l, "      "))
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
	out := []string{fmt.Sprintf("# Iridium: dsh-web-app's %q Agent preset, run process-wide by the sdk profile.", preset), "# Generated; do not edit."}
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
