package discover

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ErrOutside: a path the folder picker may not show.
var ErrOutside = errors.New("not a folder you may pick")

// Folders is the folder picker's view of the disk (plan "网页": 新工作区 = 选文件夹): folders only,
// no hidden ones, below Roots only, and never into Deny (the agents' own and Iridium's
// directories, ~/.ssh), symbolic links resolved first.
type Folders struct {
	Roots []string
	Deny  []string
}

// DefaultFolders: the user's home, without what no picker should show.
func DefaultFolders(home string, more ...string) Folders {
	deny := []string{".ssh", ".gnupg", ".claude", ".codex", ".pi", ".dsh", ".config/iridium", ".local/state/iridium"}
	for i, d := range deny {
		deny[i] = filepath.Join(home, d)
	}
	return Folders{Roots: []string{home}, Deny: append(deny, more...)}
}

// Resolve is path made absolute ("~" is the first root), symbolic links resolved, if it is a
// folder the picker may show.
func (f Folders) Resolve(path string) (string, error) {
	if len(f.Roots) == 0 {
		return "", ErrOutside
	}
	switch {
	case path == "" || path == "~":
		path = f.Roots[0]
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(f.Roots[0], path[2:])
	case !filepath.IsAbs(path):
		return "", ErrOutside
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", errors.New("not a folder")
	}
	if !f.allowed(real) {
		return "", ErrOutside
	}
	return real, nil
}

func (f Folders) allowed(path string) bool {
	for _, d := range f.Deny {
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		}
		if within(path, d) {
			return false
		}
	}
	for _, r := range f.Roots {
		if real, err := filepath.EvalSymlinks(r); err == nil {
			r = real
		}
		if within(path, r) {
			return true
		}
	}
	return false
}

// List is the folders in path, sorted by name; Parent is "" at a root.
func (f Folders) List(path string) (api.Folders, error) {
	dir, err := f.Resolve(path)
	if err != nil {
		return api.Folders{}, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return api.Folders{}, err
	}
	out := api.Folders{Path: dir, Folders: []string{}}
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			st, err := os.Stat(filepath.Join(dir, name))
			isDir = err == nil && st.IsDir()
		}
		if isDir && f.allowedLink(filepath.Join(dir, name)) {
			out.Folders = append(out.Folders, name)
		}
	}
	slices.SortFunc(out.Folders, func(a, b string) int {
		if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	if parent := filepath.Dir(dir); parent != dir && f.allowed(parent) {
		out.Parent = parent
	}
	return out, nil
}

func (f Folders) allowedLink(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	return err == nil && f.allowed(real)
}

// Make makes folder name in path and gives its path. The name is one plain, visible folder name.
func (f Folders) Make(path, name string) (string, error) {
	dir, err := f.Resolve(path)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\x00") || len(name) > 255 {
		return "", errors.New("not a folder name")
	}
	p := filepath.Join(dir, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}
