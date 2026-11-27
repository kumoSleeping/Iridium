// Package ghenv is what an agent's environment gets for GitHub (docs/api.md "GitHub", kumo
// 2026-11-27): git's credential helper for github.com, Iridium's own (ir github credential), in
// place of those configured before it, and a gh in front of the real one on PATH (ir github gh).
// Each asks Iridium for the token of the person the agent's turn is for, at each use; none bound,
// git's own helpers and gh's own login answer, as without it. Nothing on disk of the user's is
// touched: the config is the environment's (GIT_CONFIG_COUNT), the gh a link in Iridium's state.
package ghenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// UserVar names another bound person to act as (a member ID, a name or a GitHub login).
	UserVar = "IR_GITHUB_USER"
	// ShimVar is the directory of Iridium's gh, to be passed over for the real one.
	ShimVar = "IR_GH_SHIM"
	// countVar is GIT_CONFIG_COUNT as it was before Iridium's entries.
	countVar = "IR_GIT_CONFIG_COUNT"
	// Username is the user name the helper answers with (GitHub takes any with a token).
	Username  = "x-access-token"
	helperKey = "credential.https://github.com.helper"
)

// Shim makes Iridium's gh in dir: a link named gh to exe (the iridium binary, which runs as gh
// when so named). Not on Windows (no links to count on): there git's helper alone.
func Shim(dir, exe string) bool {
	if runtime.GOOS == "windows" || dir == "" || exe == "" {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	p := filepath.Join(dir, "gh")
	if cur, err := os.Readlink(p); err == nil && cur == exe {
		return true
	}
	_ = os.Remove(p)
	return os.Symlink(exe, p) == nil
}

// Env is env with git's helper and, shim given, Iridium's gh first on PATH.
func Env(env []string, exe, shim string) []string {
	if exe == "" {
		return env
	}
	n, _ := strconv.Atoi(get(env, "GIT_CONFIG_COUNT"))
	i := strconv.Itoa
	env = set(env, countVar, i(n))
	// An empty value first: the helpers configured before are not asked (git asks each in turn
	// till one answers); ir github credential asks them itself where it has none (Without).
	env = set(env, "GIT_CONFIG_KEY_"+i(n), helperKey)
	env = set(env, "GIT_CONFIG_VALUE_"+i(n), "")
	env = set(env, "GIT_CONFIG_KEY_"+i(n+1), helperKey)
	env = set(env, "GIT_CONFIG_VALUE_"+i(n+1), "!"+quote(exe)+" ir github credential")
	env = set(env, "GIT_CONFIG_COUNT", i(n+2))
	if shim != "" {
		env = set(env, ShimVar, shim)
		env = set(env, "PATH", shim+string(os.PathListSeparator)+get(env, "PATH"))
	}
	return env
}

// Without is env as it was before Env's git entries (its count as it was).
func Without(env []string) []string {
	n := get(env, countVar)
	if n == "" {
		return env
	}
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == countVar || k == "GIT_CONFIG_COUNT" {
			continue
		}
		out = append(out, kv)
	}
	if n != "0" {
		out = append(out, "GIT_CONFIG_COUNT="+n)
	}
	return out
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func get(env []string, k string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], k+"="); ok {
			return v
		}
	}
	return ""
}

func set(env []string, k, v string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, k+"=") {
			out = append(out, kv)
		}
	}
	return append(out, k+"="+v)
}
