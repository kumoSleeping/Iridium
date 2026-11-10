// Package place is how a place (a folder at the host or at an app's computer) is written for a
// person and an agent: "<app name>:<path>", the host "host:<path>" (or 主机:, 本机:, its reserved
// names). Input is split at its first colon and the prefix looked up by name; without one it is the
// caller's own computer. Output always has the prefix. Stored, a place stays "<app ID>:<path>"
// (the host's, its bare path): Key, FromKey. web/src/places.ts does the same, by the same table of
// cases (testdata/places.json).
package place

import (
	"errors"
	"fmt"
	"strings"
)

// Host is how output names the host.
const Host = "host"

// HostNames are the host's reserved names, any of which input may use.
var HostNames = []string{"host", "主机", "本机"}

// IsHost: name is one of the host's (ASCII in any case).
func IsHost(name string) bool {
	for _, h := range HostNames {
		if strings.EqualFold(name, h) {
			return true
		}
	}
	return false
}

// Split cuts s at its first colon into the app's name and the path there; ok false if s has no
// prefix: no colon, nothing before it, a single ASCII letter (a Windows drive: C:\x), or what
// only a path has before it (a slash, a backslash, a leading ~).
func Split(s string) (name, path string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return "", s, false
	}
	name = s[:i]
	if len(name) == 1 && (name[0]|0x20 >= 'a' && name[0]|0x20 <= 'z') {
		return "", s, false
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, "~") {
		return "", s, false
	}
	return name, s[i+1:], true
}

// Place is a place resolved: App, its app's ID ("" the host); Path, as written there.
type Place struct {
	App  string `json:"app"`
	Path string `json:"path"`
}

// ErrNoApp: the prefix names no app.
var ErrNoApp = errors.New("no such app")

// Parse resolves s: its prefix by lookup (an app's name to its ID; ok false if none), the host by
// its reserved names; without a prefix, self (the caller's own computer: "" the host).
func Parse(s, self string, lookup func(name string) (id string, ok bool)) (Place, error) {
	name, path, ok := Split(s)
	switch {
	case !ok:
		return Place{App: self, Path: s}, nil
	case IsHost(name):
		return Place{Path: path}, nil
	}
	if lookup != nil {
		if id, ok := lookup(name); ok {
			return Place{App: id, Path: path}, nil
		}
	}
	return Place{}, fmt.Errorf("%w: %s", ErrNoApp, name)
}

// Format is p as output writes it, always prefixed: its app's name by nameOf (its ID if none), or
// Host.
func Format(p Place, nameOf func(id string) string) string {
	if p.App == "" {
		return Host + ":" + p.Path
	}
	name := ""
	if nameOf != nil {
		name = nameOf(p.App)
	}
	if name == "" {
		name = p.App
	}
	return name + ":" + p.Path
}

// Key is p as stored (a group's path, a layout's key): "<app ID>:<path with />", the host's its
// path alone.
func Key(p Place) string {
	if p.App == "" {
		return p.Path
	}
	return p.App + ":" + strings.ReplaceAll(p.Path, `\`, "/")
}

// FromKey is the place stored as k (Key's form).
func FromKey(k string) Place {
	if strings.HasPrefix(k, "/") || strings.HasPrefix(k, "~") {
		return Place{Path: k}
	}
	if id, path, ok := strings.Cut(k, ":"); ok && id != "" {
		return Place{App: id, Path: path}
	}
	return Place{Path: k}
}
