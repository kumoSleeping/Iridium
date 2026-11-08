package executor

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Secret is what the hello says a secret's value is (docs/exec.md "握手"): the value stays here,
// and a hold.start that passes it back gets the real one. The Iridium bases its spawns on the
// hello's env and keeps them on its disk, so a key never leaves this machine; old Iridiums pass
// the env back as they got it, so the same works with them.
const Secret = "(secret kept at the executor)"

// secretName: a variable whose value may be a key, a password or a login.
var secretName = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSW|CREDENTIAL|COOKIE|AUTH|SESSION|PRIVATE|^(DEEPSEEK|ANTHROPIC|OPENAI|AWS|AZURE|GOOGLE|GH|GITHUB|NPM|HF)_`)

// ownName: the variables that point an ir at an Iridium or say whose session it runs in: this
// machine's are for an Iridium here, not the one it connects to, so neither it nor its processes
// get them (that one sets its own).
var ownName = regexp.MustCompile(`^IRIDIUM_(SESSION|SOCKET|CONFIG|URL|TOKEN)$`)

// helloEnv is env as the hello gives it: secrets' values as Secret, the Iridium's own left out.
func helloEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case ownName.MatchString(k):
		case secretName.MatchString(k):
			out = append(out, k+"="+Secret)
		default:
			out = append(out, kv)
		}
	}
	return out
}

// startEnv is a hold.start's env with each Secret its value in own (left out if own has none).
func startEnv(env, own []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if v != Secret {
			out = append(out, kv)
			continue
		}
		for _, o := range own {
			if ok, ov, _ := strings.Cut(o, "="); ok == k {
				out = append(out, k+"="+ov)
				break
			}
		}
	}
	return out
}

// denied are the paths never served, besides Options.Deny: its TOKEN's, ~/.ssh, and the agents'
// logins and keys (dsh's credentials, .env and profiles with the plugin's settings; Claude's,
// Codex's and pi's logins; cloud, gh, npm and git credentials; GnuPG; .netrc).
func (e *Executor) denied() []string {
	home, _ := os.UserHomeDir()
	env := func(k, def string) string {
		for _, kv := range e.o.Env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok && v != "" {
				return v
			}
		}
		return def
	}
	at := func(elem ...string) string { return filepath.Join(append([]string{home}, elem...)...) }
	d := []string{
		filepath.Join(e.o.State, "token"), at(".ssh"),
		filepath.Join(env("CLAUDE_CONFIG_DIR", at(".claude")), ".credentials.json"),
		filepath.Join(env("CODEX_HOME", at(".codex")), "auth.json"),
		at(".pi", "agent", "auth.json"),
		at(".aws"), at(".config", "gh"), at(".gnupg"), at(".netrc"), at(".npmrc"), at(".git-credentials"),
		at(".docker", "config.json"),
	}
	dshHomes := []string{env("DSH_HOME", at(".dsh"))}
	for _, a := range e.o.Agents {
		if a.Agent == "dsh" && a.Home != "" {
			dshHomes = append(dshHomes, a.Home)
		}
	}
	for _, h := range dshHomes {
		d = append(d, filepath.Join(h, ".credentials.yaml"), filepath.Join(h, ".env"), filepath.Join(h, "profiles"))
	}
	d = append(d, e.o.Deny...)
	// Each as given and with its links resolved: a ~/.claude linked elsewhere is denied there too.
	for _, p := range slices.Clone(d) {
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			d = append(d, r)
		}
	}
	return d
}
