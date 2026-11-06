package discover

import (
	"errors"
	"path/filepath"
	"slices"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ErrColor is a colour tag that is none of api.Colors.
var ErrColor = errors.New("not a colour tag")

func validColor(c api.Color) bool { return c == "" || slices.Contains(api.Colors, c) }

// FolderColors is every folder's colour tag, by path: the carried state "colors".
func (x *Index) FolderColors() (api.FolderColors, error) {
	m, err := x.o.Store.FolderColors()
	if err != nil {
		return nil, err
	}
	out := make(api.FolderColors, len(m))
	for p, c := range m {
		out[p] = api.Color(c)
	}
	return out, nil
}

// SetFolderColor tags folder path (absolute) with a colour, "" none, and carries it at once.
func (x *Index) SetFolderColor(path string, c api.Color) error {
	if !validColor(c) {
		return ErrColor
	}
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return errors.New("not an absolute path")
	}
	if err := x.o.Store.SetFolderColor(filepath.Clean(path), string(c)); err != nil {
		return err
	}
	x.publishColors()
	return nil
}

func (x *Index) publishColors() {
	if x.o.Hub == nil {
		return
	}
	c, err := x.FolderColors()
	if err != nil {
		x.o.Logf("discover: colors: %v", err)
		return
	}
	x.o.Hub.Set("colors", c)
}
