package dsh

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// A preset of another package's rows (OMD's, in DSH Desktop's web profile): their names go
// through the modules file, and the base rows they bring again go off only where it is.
func TestPatchModules(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "pkg"))
	p, err := FindPackage(filepath.Join(root, "lib", "bin.js"))
	if err != nil {
		t.Fatal(err)
	}
	plugins := `- id: tool-executor
  name: oh-my-dsh/ptc
- id: tool-bash
  name: 'oh-my-dsh/host/tool-bash'
  disabled: !!js process.platform === 'win32'
- id: tool-fs
  name: '@deepseek-ai/dsh-tool-fs'
- id: delegation
  name: cordis:group
  config:
    - id: tool-workflow
      name: "@scope/pkg/x"
`
	got, err := p.Patch(agent.Preset{ID: "omd", Plugins: plugins}, "", "file:///s/dsh-modules.mjs")
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"\n- id: tool-bash\n  disabled: !!js \"(process.platform === 'win32') || ((fs, path) => {",
		`return [\"oh-my-dsh\"].every(`,
		"\n- id: tool-workflow\n  disabled: !!js \"((fs, path) => {",
		`return [\"@scope/pkg\"].every(`,
		"\n- id: tool-fs\n  disabled: true\n",
		"\n    - id: iridium-tool-executor\n      name: 'file:///s/dsh-modules.mjs?oh-my-dsh%2Fptc'\n",
		"\n      name: 'file:///s/dsh-modules.mjs?oh-my-dsh%2Fhost%2Ftool-bash'\n      disabled: !!js process.platform === 'win32'\n",
		"\n      name: '@deepseek-ai/dsh-tool-fs'\n",
		"\n      name: cordis:group\n",
		"\n          name: 'file:///s/dsh-modules.mjs?@scope%2Fpkg%2Fx'\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "- id: tool-executor\n") {
		t.Errorf("a row the base has not is off:\n%s", s)
	}
	// Without a modules file the names stay, and so do the plain disables.
	if got, _ := p.Patch(agent.Preset{ID: "omd", Plugins: plugins}, "", ""); !strings.Contains(string(got), "      name: oh-my-dsh/ptc\n") {
		t.Errorf("no modules:\n%s", got)
	}
}

// The modules file and the expression, run by node against a dsh home.
func TestModulesModule(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	home := t.TempDir()
	write := func(rel, data string) {
		f := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte(data), 0o600)
	}
	// A package in a profile other than web, by an exports pattern, and one with no exports.
	write("profiles/web/node_modules/other/package.json", `{"name":"other"}`)
	write("profiles/b/node_modules/omd/package.json", `{"name":"omd","exports":{".":"./i.mjs","./host/*":{"types":"./t","import":"./lib/*.mjs"}}}`)
	write("profiles/b/node_modules/omd/lib/tool-x.mjs", "export const name = 'x'\nexport function apply() {}\n")
	write("profiles/web/node_modules/@s/plain/package.json", `{"name":"@s/plain","main":"m.mjs"}`)
	write("profiles/web/node_modules/@s/plain/m.mjs", "export default { name: 'plain' }\n")
	mod := filepath.Join(t.TempDir(), ModulesFile)
	os.WriteFile(mod, []byte(ModulesModule), 0o600)
	url := FileURL(mod)
	expr := func(pkgs ...string) string {
		e := foundExpr(pkgs, "")
		var s string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(e, "!!js ")), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	script := `
const load = async (spec) => { try { return (await import(process.argv[1] + '?' + encodeURIComponent(spec))).default?.name } catch (e) { return 'error: ' + e.message } }
console.log(JSON.stringify([await load('omd/host/tool-x'), await load('@s/plain'), await load('omd/none'), await load('missing/x'), eval(process.argv[2]), eval(process.argv[3])]))
`
	cmd := exec.Command("node", "--input-type=module", "-e", script, url, expr("omd", "@s/plain"), expr("omd", "missing"))
	cmd.Env = append(os.Environ(), "DSH_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := `["x","plain","error: iridium: no omd/none in the profiles at ` + filepath.Join(home, "profiles") + `","error: iridium: no missing/x in the profiles at ` + filepath.Join(home, "profiles") + `",true,false]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
