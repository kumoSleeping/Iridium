package discover

import (
	"errors"
	"slices"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ErrColor is a colour tag that is none of api.Colors.
var ErrColor = errors.New("not a colour tag")

func validColor(c api.Color) bool { return c == "" || slices.Contains(api.Colors, c) }

// dropStaleColors takes off the tags in a colour no longer offered (gray, the user, 2026-11-06).
func (x *Index) dropStaleColors() {
	if x.o.Store == nil {
		return
	}
	keep := make([]string, len(api.Colors))
	for i, c := range api.Colors {
		keep[i] = string(c)
	}
	if err := x.o.Store.KeepColors(keep); err != nil {
		x.o.Logf("discover: colors: %v", err)
	}
}
