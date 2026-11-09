package dsh

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// A preset of DSH Desktop's bundles: the bundles its rows name (never the dsh plugin), and a patch
// that composes as Desktop does and binds each session to it.
func TestBoundPatch(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "pkg"))
	p, err := FindPackage(filepath.Join(root, "lib", "bin.js"))
	if err != nil {
		t.Fatal(err)
	}
	preset := agent.Preset{ID: "omd-ptc", Plugins: `- id: tool-executor
  name: oh-my-dsh/ptc
- id: persona
  name: '@deepseek-ai/dsh-persona'
  config:
    prefix: You are OMD.
- id: tool-fs
  name: '@deepseek-ai/dsh-tool-fs'
`}
	bundles := []agent.Bundle{
		{Name: "dsh-remote-iridium", Patches: []string{"- insert:\n    - id: remote-iridium\n      name: dsh-remote-iridium\n"}},
		{Name: "other", Patches: []string{"- id: x\n  disabled: true\n"}},
		{Name: "oh-my-dsh", Patches: []string{
			"- insert:\n    - id: oh-my-dsh\n      name: oh-my-dsh\n    - id: omd-x\n      name: '@deepseek-ai/dsh-tool-web'\n- id: jobs\n  disabled: true\n",
			"# presets\n- insert:\n    - id: preset-omd\n      name: '@deepseek-ai/dsh-agent-preset'\n      config:\n        id: omd\n        plugins:\n          - id: a\n            name: oh-my-dsh/agent\n\n# more\n- insert:\n    - id: preset-b\n      name: '@deepseek-ai/dsh-agent-preset'\n      config:\n        id: b\n        plugins: []\n    - id: kept\n      name: oh-my-dsh/kept\n",
		}},
	}
	if Bound(agent.Preset{ID: "standard", Plugins: "- id: tool-fs\n  name: '@deepseek-ai/dsh-tool-fs'\n"}, bundles) != nil {
		t.Error("a preset of dsh's own is bound")
	}
	bound := Bound(preset, bundles)
	if len(bound) != 1 || bound[0].Name != "oh-my-dsh" {
		t.Fatalf("bound %+v", bound)
	}
	got, err := p.BoundPatch(preset, "Use ir.", "file:///s/dsh-modules.mjs", "file:///s/dsh-preset.mjs", bound)
	if err != nil {
		t.Fatal(err)
	}
	want := `# Iridium: dsh's "omd-ptc" Agent preset, bound to each session by the sdk profile over DSH Desktop's bundles (oh-my-dsh).
# Generated; do not edit.
- insert:
    - id: agent-preset-registry
      name: '@deepseek-ai/dsh-agent-preset-registry'
      config:
        default: "omd-ptc"
`
	if !strings.HasPrefix(string(got), want) {
		t.Errorf("head:\n%s", got)
	}
	s := string(got)
	for _, w := range []string{
		// The web surface's disables.
		"\n- id: tool-bash\n  disabled: true\n",
		// The bundle's layer, its names through the modules file, its preset declarations left out.
		"# oh-my-dsh, patch 1\n- insert:\n    - id: oh-my-dsh\n      name: 'file:///s/dsh-modules.mjs?oh-my-dsh'\n    - id: omd-x\n      name: '@deepseek-ai/dsh-tool-web'\n- id: jobs\n  disabled: true\n",
		"# oh-my-dsh, patch 2\n# presets\n# more\n- insert:\n    - id: kept\n      name: 'file:///s/dsh-modules.mjs?oh-my-dsh%2Fkept'\n",
		// The persona as the system-prompt row's, Iridium's after it.
		"- id: system-prompt\n  config:\n    personaPrefix: You are OMD.\n    personaSuffix: |-\n      Use ir.\n",
		// The preset, as declared, and its binder.
		"- insert:\n    - id: iridium-preset\n      name: '@deepseek-ai/dsh-agent-preset'\n      config:\n        id: \"omd-ptc\"\n        plugins:\n          - id: tool-executor\n            name: 'file:///s/dsh-modules.mjs?oh-my-dsh%2Fptc'\n          - id: tool-fs\n            name: '@deepseek-ai/dsh-tool-fs'\n    - id: iridium-preset-binder\n      name: 'file:///s/dsh-preset.mjs'\n      config:\n        preset: \"omd-ptc\"\n",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("no %q in\n%s", w, s)
		}
	}
	for _, no := range []string{"remote-iridium", "id: x\n", "id: preset-omd", "id: preset-b", "oh-my-dsh/agent", "iridium-tool"} {
		if strings.Contains(s, no) {
			t.Errorf("%q in\n%s", no, s)
		}
	}
}
