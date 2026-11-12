package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/place"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Permissions (draft 二「权限」, decisions 14-19): what an app or a login may use, its agents and
// its scope (places "应用名:路径", or all). What a caller may use is its login's within its app's
// (checked as they meet: the intersection). The host may use all. Claude stays the owner's own
// (access.Rules): Perms narrow it further, never widen it.
//
// A session is out of scope when its place (the app it runs at, its folder) is in none of the
// scope's places: it is not there for the caller (not listed, not found). A session of an agent
// the caller may not use it may see but not use: not start, send to, answer, rename, archive.

func allPerms() api.Perms { return api.Perms{Agents: []string{api.PermsAll}, Scope: []string{api.PermsAll}} }

func orAll(p *api.Perms) api.Perms {
	if p == nil {
		return allPerms()
	}
	return *p
}

// permsFor is what caller c may use: the host all; a login of ir_web its own; an app's login its
// app's within its own.
func (s *Server) permsFor(c caller) api.Perms {
	switch {
	case c.Host():
		return allPerms()
	case c.Web():
		return orAll(s.loginPermsOf(gate.WebApp, c.Login))
	}
	p := orAll(s.appPerms(c.App))
	if c.Login != "" {
		p = s.intersect(p, orAll(s.loginPermsOf(c.App, c.Login)))
	}
	// A session's own TOKEN (its agent's ir at another computer): within its turn's login's too.
	if c.session != "" && !c.socket {
		if o, ok := s.originOf(c.session); ok && !o.Host() && o.App != c.App {
			p = s.intersect(p, s.permsFor(caller{Identity: o}))
		}
	}
	return p
}

// loginPermsOf is what login id of app was given; nil for all.
func (s *Server) loginPermsOf(app, id string) *api.Perms {
	b, err := s.st.LoginPermsOf(app, id)
	if err != nil || len(b) == 0 {
		return nil
	}
	var p api.Perms
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

// intersect is what both a and b allow: the agents in both, the places of each within the other's.
func (s *Server) intersect(a, b api.Perms) api.Perms {
	out := api.Perms{}
	switch {
	case slices.Contains(a.Agents, api.PermsAll):
		out.Agents = slices.Clone(b.Agents)
	case slices.Contains(b.Agents, api.PermsAll):
		out.Agents = slices.Clone(a.Agents)
	default:
		for _, x := range a.Agents {
			if slices.Contains(b.Agents, x) {
				out.Agents = append(out.Agents, x)
			}
		}
	}
	switch {
	case slices.Contains(a.Scope, api.PermsAll):
		out.Scope = slices.Clone(b.Scope)
	case slices.Contains(b.Scope, api.PermsAll):
		out.Scope = slices.Clone(a.Scope)
	default:
		for _, x := range a.Scope {
			if app, p := s.parsePlace(x); s.scopeHas(b.Scope, app, p) {
				out.Scope = append(out.Scope, x)
			}
		}
		for _, x := range b.Scope {
			if app, p := s.parsePlace(x); s.scopeHas(a.Scope, app, p) && !slices.Contains(out.Scope, x) {
				out.Scope = append(out.Scope, x)
			}
		}
	}
	if out.Agents == nil {
		out.Agents = []string{}
	}
	if out.Scope == nil {
		out.Scope = []string{}
	}
	return out
}

// mayAgent reports whether p lets one use agent a.
func mayAgent(p api.Perms, a api.Agent) bool {
	return slices.Contains(p.Agents, api.PermsAll) || slices.Contains(p.Agents, string(a))
}

// parsePlace splits place "应用名:路径" into the app's ID and the path: split at the first colon,
// the prefix an app's name or ID (the host by its names); none (or a drive letter, C:\…) is the
// host's.
func (s *Server) parsePlace(p string) (app, path string) {
	name, rest, ok := place.Split(p)
	switch {
	case !ok:
		return gate.HostID, p
	case place.IsHost(name):
		return gate.HostID, rest
	}
	if info, found := s.gate().Find(name); found {
		if info.Host() {
			return gate.HostID, rest
		}
		return info.ID, rest
	}
	return name, rest
}

// scopeHas reports whether place (app, path) is within scope.
func (s *Server) scopeHas(scope []string, app, path string) bool {
	if app == "" {
		app = gate.HostID
	}
	for _, e := range scope {
		if e == api.PermsAll {
			return true
		}
		a, p := s.parsePlace(e)
		if a != app {
			continue
		}
		if p == "" || placeWithin(path, p) || p == "/" && strings.HasPrefix(path, "/") {
			return true
		}
	}
	return false
}

// sees reports whether p's scope has session st.
func (s *Server) sees(p api.Perms, st api.Session) bool { return s.scopeHas(p.Scope, st.Machine, st.Cwd) }

var errNoSession = fmt.Errorf("%w", session.ErrNotFound)

// seeing is session id if the caller may see it (its scope has it).
func (s *Server) seeing(c caller, id string) (*api.SessionState, error) {
	st, err := s.m.State(id)
	if err != nil {
		return nil, err
	}
	if !c.Host() && !s.sees(s.permsFor(c), st.Session) {
		return nil, errNoSession
	}
	return st, nil
}

// checkPerms checks that the caller may use a session of agent a at place (app, path).
func (s *Server) checkPerms(c caller, a api.Agent, app, path string) error {
	if c.Host() {
		return nil
	}
	p := s.permsFor(c)
	if !s.scopeHas(p.Scope, app, path) {
		return &access.Denied{Reason: fmt.Sprintf("%s is out of this one's scope", path)}
	}
	if !mayAgent(p, a) {
		return &access.Denied{Reason: fmt.Sprintf("%s is not among the agents this one may use", a)}
	}
	return nil
}

// sessionGuard checks every /v1/internal/sessions/{id}/… request before its route: GET (reading) needs
// the session in the caller's scope, the rest also its agent among the caller's.
func (s *Server) sessionGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, "/v1/internal/sessions/")
		if !ok || rest == "" || s.m == nil {
			next.ServeHTTP(w, r)
			return
		}
		id, _, _ := strings.Cut(rest, "/")
		c := callerOf(r)
		if c.Host() {
			next.ServeHTTP(w, r)
			return
		}
		var err error
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			_, err = s.seeing(c, id)
		} else {
			_, err = s.using(c, id)
		}
		// A session not there at all its route answers (some find one not loaded yet).
		if err != nil && (errors.Is(err, errNoSession) || !errors.Is(err, session.ErrNotFound)) {
			s.error(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// filterSessions keeps the sessions p's scope has.
func (s *Server) filterSessions(p api.Perms, l []api.Session) []api.Session {
	if slices.Contains(p.Scope, api.PermsAll) {
		return l
	}
	out := make([]api.Session, 0, len(l))
	for _, x := range l {
		if s.sees(p, x) {
			out = append(out, x)
		}
	}
	return out
}

// stateFilter is an event stream's hub.Filter for a caller that does not see every session: the
// list without the others, a session's own states as none.
func (s *Server) stateFilter(c caller) func(key string, v []byte) []byte {
	if c.Host() {
		return nil
	}
	p := s.permsFor(c)
	if slices.Contains(p.Scope, api.PermsAll) {
		return nil
	}
	return func(key string, v []byte) []byte {
		switch {
		case key == "guilds":
			// Its groups, and the marks of the folders in its scope.
			var g api.Guilds
			if json.Unmarshal(v, &g) != nil {
				return v
			}
			g.Guilds = s.scopeWorkspaces(p, g.Guilds)
			g.Folders = slices.DeleteFunc(g.Folders, func(f api.FolderMark) bool { return !s.scopeHas(p.Scope, gate.HostID, f.Path) })
			b, _ := json.Marshal(g)
			return b
		case key == "list":
			var l []api.Session
			if json.Unmarshal(v, &l) != nil {
				return v
			}
			b, _ := json.Marshal(s.filterSessions(p, l))
			return b
		case key == "voice":
			// The rooms of the folder groups it sees.
			var rooms map[string]json.RawMessage
			if json.Unmarshal(v, &rooms) != nil {
				return v
			}
			for id := range rooms {
				if n, err := strconv.ParseInt(id, 10, 64); err != nil || !s.seesGuild(c, n) {
					delete(rooms, id)
				}
			}
			b, _ := json.Marshal(rooms)
			return b
		case strings.HasPrefix(key, "session:"), strings.HasPrefix(key, "context:"):
			_, id, _ := strings.Cut(key, ":")
			if st, err := s.m.State(id); err == nil && !s.sees(p, st.Session) {
				return []byte("null")
			}
		}
		return v
	}
}

// scopeLeadsTo reports whether some place of scope lies under (app, path): a folder on the way to
// what the caller may use, which it may open to go on (only the folders that lead there).
func (s *Server) scopeLeadsTo(scope []string, app, path string) bool {
	for _, e := range scope {
		a, p := s.parsePlace(e)
		if a == app && (placeWithin(p, path) || path == "/" && strings.HasPrefix(p, "/")) {
			return true
		}
	}
	return false
}

// scopeFolders keeps of folder list l (at app) what the caller may see: all of it within its
// scope; on the way there only the folders that lead into it; elsewhere nothing (denied).
func (s *Server) scopeFolders(c caller, app string, l *api.Folders) error {
	if c.Host() {
		return nil
	}
	p := s.permsFor(c)
	if slices.Contains(p.Scope, api.PermsAll) || s.scopeHas(p.Scope, app, l.Path) {
		return nil
	}
	if !s.scopeLeadsTo(p.Scope, app, l.Path) {
		return &access.Denied{Reason: l.Path + " is out of this one's scope"}
	}
	sep := "/"
	if strings.Contains(l.Path, `\`) {
		sep = `\`
	}
	keep := l.Folders[:0]
	for _, f := range l.Folders {
		full := strings.TrimRight(l.Path, `/\`) + sep + f
		if s.scopeHas(p.Scope, app, full) || s.scopeLeadsTo(p.Scope, app, full) {
			keep = append(keep, f)
		}
	}
	l.Folders = keep
	return nil
}

// inScope is an error if (app, path) is out of the caller's scope.
func (s *Server) inScope(c caller, app, path string) error {
	if c.Host() {
		return nil
	}
	if p := s.permsFor(c); !s.scopeHas(p.Scope, app, path) {
		return &access.Denied{Reason: path + " is out of this one's scope"}
	}
	return nil
}

// scopeWorkspaces keeps the folder groups the caller's scope has (the host's folders).
func (s *Server) scopeWorkspaces(p api.Perms, ws []api.Workspace) []api.Workspace {
	if slices.Contains(p.Scope, api.PermsAll) {
		return ws
	}
	out := make([]api.Workspace, 0, len(ws))
	for _, w := range ws {
		if s.scopeHas(p.Scope, gate.HostID, w.Path) || s.scopeLeadsTo(p.Scope, gate.HostID, w.Path) {
			out = append(out, w)
		}
	}
	return out
}

// remoteScope: another computer's folder path (as asked: absolute, or "" / ~ for its home) is
// one the caller may open: within its scope there or on the way to it; all for a scope of all.
func (s *Server) remoteScope(c caller, ref, path string) error {
	if c.Host() {
		return nil
	}
	p := s.permsFor(c)
	if slices.Contains(p.Scope, api.PermsAll) {
		return nil
	}
	app := ref
	if info, ok := s.gate().Find(ref); ok {
		app = info.ID
	}
	if path != "" && !strings.HasPrefix(path, "~") && (s.scopeHas(p.Scope, app, path) || s.scopeLeadsTo(p.Scope, app, path)) {
		return nil
	}
	return &access.Denied{Reason: ref + ":" + path + " is out of this one's scope"}
}

// LoginSees says whether login (a login of ir_web by its ID, or the host) may see session id:
// Web Push sends a session's notices to those only.
func (s *Server) LoginSees(login, id string) bool {
	if login == "" || login == gate.HostID {
		return true
	}
	st, err := s.m.State(id)
	if err != nil {
		return false
	}
	return s.sees(s.permsFor(caller{Identity: gate.Identity{App: gate.WebApp, Login: login}}), st.Session)
}
