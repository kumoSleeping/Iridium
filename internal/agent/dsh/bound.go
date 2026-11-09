package dsh

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// A preset of a bundle in DSH Desktop's profile (OMD's omd-ptc, oh-my-dsh) needs more than its
// rows: the bundle's own layer (OMD's root plugin, the omd service its rows inject, and the base
// rows it replaces), and a preset scope, which OMD's PTC executor requires and only a bound preset
// has. The dsh plugin gives the bundles' patches (dsh.bundles: Iridium cannot read the profile);
// then the sdk profile composes as Desktop does: the preset registry, the web surface's disables,
// the bundle's layer, the preset's declaration, and PresetModule binding every session to it.
// Names from packages outside dsh's installation go through ModulesModule. (Verified against dsh
// 0.2.2-rc.1 with OMD 1.3.9: omd-ptc's agent has 63 tools under OMD's PTC executor.)

// PresetFile is PresetModule's name, written beside the patch.
const PresetFile = "dsh-preset.mjs"

// PresetModule binds each session the process runs, as it is created, to the preset of its
// config, as DSH Desktop binds a session's (a subagent joins its parent's on its own).
const PresetModule = `// Iridium: binds each session of this process to the agent preset of its config.
export const name = 'iridium-preset'
export const inject = ['agents', 'agentPresets']
export function apply(ctx, config) {
  ctx.on('agent/created', async ({ agent }) => {
    if (ctx.agentPresets.composedPreset(agent.ctx) !== undefined) return
    await ctx.agentPresets.recompose(agent.ctx, config.preset)
  })
}
`

// ownBundle is the dsh plugin's package, never composed into a session (it would connect again).
const ownBundle = "dsh-remote-iridium"

var insertLine = regexp.MustCompile(`^- insert:\s*$`)

// Bound are the bundles among bundles whose packages preset's rows name; none: it runs
// process-wide (Patch).
func Bound(preset agent.Preset, bundles []agent.Bundle) []agent.Bundle {
	pkgs := externalPackages(preset)
	var out []agent.Bundle
	for _, b := range bundles {
		if b.Name != ownBundle && slices.Contains(pkgs, b.Name) {
			out = append(out, b)
		}
	}
	return out
}

// externalPackages are the packages outside dsh's installation preset's rows name.
func externalPackages(preset agent.Preset) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(preset.Plugins, "\r\n", "\n"), "\n") {
		if m := rowName.FindStringSubmatch(l); m != nil && !installed(m[3]) && !slices.Contains(out, packageOf(m[3])) {
			out = append(out, packageOf(m[3]))
		}
	}
	return out
}

// BoundPatch is the --patch file that binds every session to preset over bundles (Bound's), with
// suffix (Iridium's system prompt) after the preset's own persona suffix; modules and binder are
// ModulesFile's and PresetFile's file: URLs where the agent runs.
func (p Package) BoundPatch(preset agent.Preset, suffix, modules, binder string, bundles []agent.Bundle) ([]byte, error) {
	// One session a process: the persona folds into the system-prompt row as process-wide.
	body, persona, err := personaOf(preset, suffix)
	if err != nil {
		return nil, err
	}
	off, err := p.webOff(preset.ID)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(bundles))
	for i, b := range bundles {
		names[i] = b.Name
	}
	id := quote(preset.ID)
	out := []string{
		fmt.Sprintf("# Iridium: dsh's %q Agent preset, bound to each session by the sdk profile over DSH Desktop's bundles (%s).", preset.ID, strings.Join(names, ", ")),
		"# Generated; do not edit.",
		"- insert:",
		"    - id: agent-preset-registry",
		"      name: '@deepseek-ai/dsh-agent-preset-registry'",
		"      config:",
		"        default: " + id,
	}
	offs := make([]string, 0, len(off))
	for id := range off {
		offs = append(offs, id)
	}
	slices.Sort(offs)
	for _, id := range offs {
		out = append(out, "- id: "+id, "  disabled: true")
	}
	for _, b := range bundles {
		for i, text := range b.Patches {
			out = append(out, fmt.Sprintf("# %s, patch %d", b.Name, i+1))
			out = append(out, bundleLayer(text, modules)...)
		}
	}
	if len(persona) > 0 {
		out = append(append(out, "- id: system-prompt", "  config:"), persona...)
	}
	out = append(out,
		"- insert:",
		"    - id: iridium-preset",
		"      name: '@deepseek-ai/dsh-agent-preset'",
		"      config:",
		"        id: "+id,
	)
	if len(body) == 0 {
		out = append(out, "        plugins: []")
	} else {
		out = append(out, "        plugins:")
		for _, l := range rename(body, modules) {
			if !blank(l) {
				l = "          " + l
			}
			out = append(out, l)
		}
	}
	out = append(out,
		"    - id: iridium-preset-binder",
		"      name: "+moduleQuote(binder),
		"      config:",
		"        preset: "+id,
	)
	return []byte(strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"), nil
}

// rename is lines with the names of packages outside dsh's installation through modules.
func rename(lines []string, modules string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if m := rowName.FindStringSubmatch(l); m != nil && !installed(m[3]) && modules != "" {
			l = m[1] + m[2] + "name: " + moduleName(modules, m[3])
		}
		out[i] = l
	}
	return out
}

// bundleLayer is a bundle's patch file for the sdk profile: less its preset declarations (the
// session's preset comes from the registry dsh.presets reads, as it is now) and an insert left
// empty by that, its names through modules.
func bundleLayer(text, modules string) []string {
	ls := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	var kept []string
	for i := 0; i < len(ls); i++ {
		m := rowLine.FindStringSubmatch(ls[i])
		if m == nil {
			kept = append(kept, ls[i])
			continue
		}
		ind := len(m[1]) + 2
		end := i + 1
		for end < len(ls) && (blank(ls[end]) || indent(ls[end]) >= ind) {
			end++
		}
		if !slices.ContainsFunc(ls[i+1:end], func(l string) bool { return indent(l) == ind && presetName.MatchString(l) }) {
			kept = append(kept, ls[i])
			continue
		}
		i = end - 1
	}
	var out []string
	for i, l := range kept {
		if insertLine.MatchString(l) {
			j := i + 1
			for j < len(kept) && (blank(kept[j]) || strings.HasPrefix(strings.TrimSpace(kept[j]), "#")) {
				j++
			}
			if j == len(kept) || indent(kept[j]) == 0 {
				continue
			}
		}
		out = append(out, l)
	}
	return rename(out, modules)
}

// moduleQuote is a file: URL as a YAML single-quoted scalar.
func moduleQuote(u string) string {
	return "'" + strings.ReplaceAll(u, "'", "''") + "'"
}
