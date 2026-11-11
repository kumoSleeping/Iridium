package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/place"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Folders and files as a caller writes them (plan "ir"): ir api passes them on as given, the
// server makes them whole. A place is <app>:<path> at that app's computer, host:<path> here;
// without a prefix, the computer the caller's own session runs at (here, if none). There, ~ and
// ~/x are the home; ".", "" and a relative path go from the caller's session's folder, when it
// runs at that computer.

// errWhole: a relative folder with no session of the caller's at that computer to go from.
func errWhole(p string) error {
	return fmt.Errorf("%w: %q: a whole path (or ~/…); no session of yours there to go from", session.ErrBadRequest, p)
}

// base is the caller's own session's computer ("" here) and folder; ok false: none.
func (s *Server) base(c caller) (machine, cwd string, ok bool) {
	if c.session == "" {
		return "", "", false
	}
	st, err := s.m.State(c.session)
	if err != nil {
		return "", "", false
	}
	return st.Machine, st.Cwd, true
}

// where makes req's folder whole: req.Machine the app's ID ("" here), req.Cwd a whole path there.
func (s *Server) where(c caller, req *api.NewSession) error {
	dir, machine := req.Cwd, req.Machine
	bm, bcwd, hasBase := s.base(c)
	if machine == "" {
		if name, path, ok := place.Split(dir); ok {
			dir = path
			if !place.IsHost(name) {
				machine = name
			}
		} else if hasBase {
			machine = bm // no prefix: the caller's computer
		}
	}
	if machine != "" {
		info, ok := s.gate().Find(machine)
		if !ok {
			return fmt.Errorf("%w: no computer %s (ir api internal/apps lists them)", session.ErrBadRequest, machine)
		}
		machine = info.ID
	}
	own := hasBase && bm == machine
	if machine == "" {
		p, err := s.hostPath(c, dir)
		if err != nil {
			return err
		}
		req.Machine, req.Cwd = "", p
		return nil
	}
	m := s.computerOf(machine)
	if m == nil { // never seen: its folder as given, for the checks after to judge
		req.Machine, req.Cwd = machine, dir
		return nil
	}
	switch {
	case (dir == "." || dir == "") && own:
		dir = bcwd
	case dir == "." || dir == "" || dir == "~":
		dir = m.Home
	case strings.HasPrefix(dir, "~/") || (m.Sep == `\` && strings.HasPrefix(dir, `~\`)):
		dir = remote.Join(m.Sep, m.Home, dir[2:])
	case remote.IsAbs(m.Sep, dir):
	case own:
		dir = remote.Join(m.Sep, bcwd, dir)
	default:
		return errWhole(dir)
	}
	req.Machine, req.Cwd = machine, dir
	return nil
}

// hostPath is p as a whole path here (host:p too): ~ the home, a relative path from the caller's
// session's folder when that runs here.
func (s *Server) hostPath(c caller, p string) (string, error) {
	if name, path, ok := place.Split(p); ok {
		if !place.IsHost(name) {
			return "", fmt.Errorf("%w: %s is at %s, not here", session.ErrBadRequest, p, name)
		}
		p = path
	}
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[1:]), nil
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	}
	if bm, cwd, ok := s.base(c); ok && bm == "" {
		return filepath.Join(cwd, p), nil
	}
	return "", errWhole(p)
}
