package join

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// knownAgents are the agents looked for on PATH, by program name. The Iridium ignores those it
// cannot drive at another machine yet.
var knownAgents = []string{"dsh", "claude", "codex", "pi"}

// FindAgents is the agents installed here, as the hello gives them. A Node program is started as
// node and its entry script, never through npm's .cmd shim (docs/exec.md "握手").
func FindAgents(logf func(string, ...any)) []wire.AgentInfo {
	out := []wire.AgentInfo{}
	for _, name := range knownAgents {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		a, err := resolveAgent(name, path)
		if err != nil {
			if logf != nil {
				logf("join: %s at %s: %v", name, path, err)
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// resolveAgent is how agent name, found at path, starts: its argv, and for a Node package its
// root and version (dsh: its @deepseek-ai scope and DSH_HOME too).
func resolveAgent(name, path string) (wire.AgentInfo, error) {
	a := wire.AgentInfo{Agent: name}
	script := ""
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		js, node, err := readShim(path)
		if err != nil {
			return a, err
		}
		script, a.Argv = js, []string{node, js}
	default:
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return a, err
		}
		a.Argv = []string{real}
		if isScript(real) {
			script = real
			if node, err := exec.LookPath("node"); err == nil {
				a.Argv = []string{node, real}
			}
		}
	}
	pkgName := ""
	if script != "" {
		if dir, pkg := packageOf(script); dir != "" {
			a.Package, a.Version, pkgName = dir, pkg.Version, pkg.Name
		}
	}
	if name == "dsh" {
		if pkgName != "@deepseek-ai/dsh" {
			return a, errors.New("in no @deepseek-ai/dsh package")
		}
		// Installed globally its packages are its own; in a project they may be hoisted beside it.
		for _, s := range []string{filepath.Join(a.Package, "node_modules", "@deepseek-ai"), filepath.Dir(a.Package)} {
			if st, err := os.Stat(filepath.Join(s, "dsh-web-app", "presets")); err == nil && st.IsDir() {
				a.Scope = s
				break
			}
		}
		if a.Scope == "" {
			return a, errors.New("no dsh-web-app presets beside it")
		}
		a.Home = os.Getenv("DSH_HOME")
		if a.Home == "" {
			home, _ := os.UserHomeDir()
			a.Home = filepath.Join(home, ".dsh")
		}
	}
	if a.Version == "" {
		a.Version = versionOf(a.Argv)
	}
	return a, nil
}

func isScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return true
	}
	return false
}

// shimTarget is a path npm's cmd-shim (or pnpm's) puts after %dp0% or %~dp0.
var shimTarget = regexp.MustCompile(`"%~?dp0%?\\([^"%]+)"`)

// readShim reads an npm .cmd shim: the script it runs, and the node it runs it with (the one
// beside the shim, else node on PATH).
func readShim(path string) (script, node string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Dir(path)
	for _, m := range shimTarget.FindAllStringSubmatch(string(b), -1) {
		p := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(m[1], `\`, "/")))
		if strings.EqualFold(filepath.Base(p), "node.exe") || strings.EqualFold(filepath.Base(p), "node") {
			continue
		}
		script = p
	}
	if script == "" {
		return "", "", errors.New("not an npm shim")
	}
	if _, err := os.Stat(script); err != nil {
		return "", "", err
	}
	node = filepath.Join(dir, "node.exe")
	if _, err := os.Stat(node); err != nil {
		if node, err = exec.LookPath("node"); err != nil {
			return "", "", errors.New("no node")
		}
	}
	return script, node, nil
}

type packageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// packageOf is the Node package a script is in: its root and package.json; "" if none.
func packageOf(script string) (string, packageJSON) {
	for dir := filepath.Dir(script); ; dir = filepath.Dir(dir) {
		var p packageJSON
		if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(b, &p) == nil && p.Name != "" {
			return dir, p
		}
		if filepath.Base(dir) == "node_modules" || filepath.Dir(dir) == dir {
			return "", packageJSON{}
		}
	}
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// versionOf asks a program its version (--version), "" if it does not say.
func versionOf(argv []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], "--version")...)
	b, _ := cmd.Output()
	return versionRe.FindString(string(b))
}
