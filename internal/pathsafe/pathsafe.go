// Package pathsafe says whether a path may be read or written for someone (plan "文件传输"):
// symlinks are resolved before the path is compared with the allowed roots, and the directories
// that hold secrets are denied whatever the roots say: Iridium's own state and config, ~/.ssh,
// and the agents' homes (~/.claude, ~/.codex, ~/.pi, ~/.dsh). File references, uploads, ir cp and
// ir files all ask a Guard; peers will too.
package pathsafe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrDenied   = errors.New("path denied")
	ErrOutside  = errors.New("path outside the allowed directories")
	ErrRelative = errors.New("path is not absolute")
)

// Guard checks paths. Its zero value allows everything; use New.
type Guard struct {
	allow  []string // resolved; nil: anywhere
	deny   []string // resolved, and as given
	except []string // inside a denied directory but allowed all the same
}

// New makes a Guard. allow: the roots paths must be under, nil for anywhere. deny: directories
// always refused (see Deny). except: directories inside denied ones that are allowed after all
// (the state directory's uploads).
func New(allow, deny, except []string) *Guard {
	g := &Guard{}
	if allow != nil {
		g.allow = []string{}
	}
	for _, a := range allow {
		g.allow = append(g.allow, both(a)...)
	}
	for _, d := range deny {
		g.deny = append(g.deny, both(d)...)
	}
	for _, e := range except {
		g.except = append(g.except, both(e)...)
	}
	return g
}

// Deny is the directories always denied: Iridium's state and config directories, extra (the
// agents' homes where moved, as by CLAUDE_CONFIG_DIR), ~/.ssh, ~/.claude, ~/.codex, ~/.pi, ~/.dsh,
// $CODEX_HOME, and /proc (other processes' environments).
func Deny(state, config string, extra ...string) []string {
	home, _ := os.UserHomeDir()
	out := []string{state, config, "/proc"}
	if home != "" {
		for _, d := range []string{".ssh", ".claude", ".codex", ".pi", ".dsh"} {
			out = append(out, filepath.Join(home, d))
		}
	}
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if v := os.Getenv(env); v != "" {
			out = append(out, v)
		}
	}
	for _, e := range extra {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// both is p cleaned and, if it differs, p with its symlinks resolved.
func both(p string) []string {
	if p == "" || !filepath.IsAbs(p) {
		return nil
	}
	p = filepath.Clean(p)
	r, err := Resolve(p)
	if err != nil || r == p {
		return []string{p}
	}
	return []string{p, r}
}

// Resolve is p (absolute) with every symlink resolved. Where p does not exist, its deepest
// existing parent is resolved and the rest joined on, so a file about to be written resolves
// to where it would land.
func Resolve(p string) (string, error) { return resolve(p, 0) }

func resolve(p string, depth int) (string, error) {
	if !filepath.IsAbs(p) {
		return "", ErrRelative
	}
	if depth > 32 {
		return "", errors.New("too many levels of symbolic links")
	}
	p = filepath.Clean(p)
	rest := ""
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest), nil
		}
		// A dangling symlink: where it points may be anywhere, so resolve its target too.
		if t, lerr := os.Readlink(p); lerr == nil {
			if !filepath.IsAbs(t) {
				t = filepath.Join(parent, t)
			}
			if rest != "" {
				t = filepath.Join(t, rest)
			}
			if t == p {
				return "", err
			}
			return resolve(t, depth+1)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// Check is the resolved path, or why p may not be used. p must be absolute; it need not exist.
// Both the path as given and as resolved must be outside every denied directory (an exception
// aside), and the resolved one inside an allowed root.
func (g *Guard) Check(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", ErrRelative
	}
	p = filepath.Clean(p)
	r, err := Resolve(p)
	if err != nil {
		return "", err
	}
	for _, c := range []string{p, r} {
		if g.denied(c) {
			return "", fmt.Errorf("%w: %s", ErrDenied, p)
		}
	}
	if g.allow != nil {
		ok := false
		for _, a := range g.allow {
			if Within(r, a) {
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrOutside, p)
		}
	}
	return r, nil
}

func (g *Guard) denied(p string) bool {
	for _, d := range g.deny {
		if Within(p, d) {
			for _, e := range g.except {
				if Within(p, e) && len(e) > len(d) {
					return false
				}
			}
			return true
		}
	}
	return false
}

// Within: p is dir or inside it. Both clean and absolute.
func Within(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}
