package server

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/secrets"
	"github.com/kumoSleeping/iridium/internal/store"
)

// The credentials (plan "凭据", docs/api.md "凭据"): stored from a panel, a person's ir, a channel
// that may, or an agent's ir through a panel's confirmation that types the value; used by ir run
// alone, whose values never go back out but to it; masked in what the API gives of transcripts.

// maxNote is the longest note a secret may have, in characters.
const maxNote = 200

// vault is what the server keeps of the credentials in memory: the masks of their values, and the
// uses panels allowed (once, by confirmation; or for the rest of a session's process).
type vault struct {
	json atomic.Pointer[secrets.Masker] // in JSON strings
	text atomic.Pointer[secrets.Masker] // in plain text

	mu       sync.Mutex
	grants   map[string]grant     // allowed secret-use confirmations, by id: one use each
	sessions map[string]allowance // allowed for the rest of a session, by session
}

type grant struct {
	session string
	names   []string
	at      time.Time
}

type allowance struct {
	proc  int // the session's process it holds for
	names map[string]bool
}

func newVault() *vault {
	return &vault{grants: map[string]grant{}, sessions: map[string]allowance{}}
}

// reloadSecrets makes the masks again from the stored values.
func (s *Server) reloadSecrets() {
	vals, err := s.st.SecretValues()
	if err != nil {
		s.logf("server: secrets: %v", err)
	}
	s.vault.json.Store(secrets.NewMasker(vals, secrets.MinShown, true))
	s.vault.text.Store(secrets.NewMasker(vals, secrets.MinShown, false))
}

// MaskText is s with the known values replaced by [secret:NAME] (pushes).
func (s *Server) MaskText(t string) string { return s.vault.text.Load().MaskString(t) }

func (s *Server) jsonMask() *secrets.Masker { return s.vault.json.Load() }

// masked serves h with the known values masked in what it writes (transcripts, JSON and event
// streams alike); a value split across writes is masked too.
func (s *Server) masked(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mw := &maskedWriter{ResponseWriter: w}
		mw.mw = secrets.NewWriter(w, s.jsonMask)
		h(mw, r)
		_ = mw.mw.Close()
	}
}

type maskedWriter struct {
	http.ResponseWriter
	mw *secrets.Writer
}

func (m *maskedWriter) Write(b []byte) (int, error) { return m.mw.Write(b) }

func (m *maskedWriter) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (m *maskedWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

func (s *Server) secretRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/secrets", s.listSecrets)
	mux.HandleFunc("PUT /api/secrets/{name}", s.setSecret)
	mux.HandleFunc("DELETE /api/secrets/{name}", func(w http.ResponseWriter, r *http.Request) {
		if c := callerOf(r); !c.Host() || c.agent {
			forbid(w, errHostSecrets)
			return
		}
		s.deleteSecret(w, r)
	})
	mux.HandleFunc("POST /api/secrets/use", s.useSecrets)
}

// terminalName is terminal id's name, for lists; the ID if it is gone.
func (s *Server) terminalName(id string) string {
	if n := s.Tag(id); n != "" {
		return n
	}
	if id == gate.HostID {
		return gate.HostName
	}
	return id
}

func (s *Server) secretInfo(sec store.Secret, uses map[string]int, last map[string]store.SecretUse) api.SecretInfo {
	info := api.SecretInfo{Name: sec.Name, Note: sec.Note, Scope: api.SecretScope(sec.Scope), Workspaces: sec.Workspaces, Always: sec.Always,
		By: s.terminalName(sec.By), CreatedAt: sec.CreatedAt, UpdatedAt: sec.UpdatedAt, Uses: uses[sec.Name]}
	if u, ok := last[sec.Name]; ok {
		su := s.secretUse(u)
		info.LastUse = &su
	}
	return info
}

func (s *Server) secretUse(u store.SecretUse) api.SecretUse {
	return api.SecretUse{Secret: u.Secret, Session: u.Session, Terminal: s.terminalName(u.Terminal), Command: u.Command, At: u.At}
}

// listSecrets: names, notes, scopes, who stored them, their uses; never a value. For every
// terminal, ir too (an agent's: it learns which names there are).
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	// Credentials are the host's alone (draft 二「权限」): to list, set, delete and use.
	if !callerOf(r).Host() {
		forbid(w, errHostSecrets)
		return
	}
	all, err := s.st.Secrets()
	if err != nil {
		s.error(w, err)
		return
	}
	counts, last, err := s.st.SecretUseCounts()
	if err != nil {
		s.error(w, err)
		return
	}
	uses, err := s.st.SecretUses("", 50)
	if err != nil {
		s.error(w, err)
		return
	}
	out := api.Secrets{Secrets: []api.SecretInfo{}, Uses: []api.SecretUse{}}
	for _, sec := range all {
		out.Secrets = append(out.Secrets, s.secretInfo(sec, counts, last))
	}
	for _, u := range uses {
		out.Uses = append(out.Uses, s.secretUse(u))
	}
	reply(w, http.StatusOK, out)
}

var errHostSecrets = errors.New("credentials are the host's alone")

func validScope(sc api.SecretScope) bool {
	return sc == "" || sc == api.ScopeAll || sc == api.ScopeWorkspaces || sc == api.ScopeOwner
}

// setSecret stores or replaces a secret: from a panel or a person's ir at once, from a channel
// that may (value and note only), from an agent's ir through a confirmation whose panel types the
// value.
func (s *Server) setSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !secrets.ValidName(name) {
		fail(w, http.StatusBadRequest, "a secret's name is capitals, digits and _, a letter first, at most 64")
		return
	}
	var req api.SetSecret
	if !readJSON(w, r, &req) {
		return
	}
	switch {
	case len(req.Value) > secrets.MaxValue:
		fail(w, http.StatusBadRequest, "the value is too long (64 KiB at most)")
		return
	case req.Note != nil && utf8.RuneCountInString(*req.Note) > maxNote:
		fail(w, http.StatusBadRequest, "the note is too long (200 characters at most)")
		return
	case !validScope(req.Scope):
		fail(w, http.StatusBadRequest, `scope is "all", "workspaces" or "owner"`)
		return
	}
	c := callerOf(r)
	old, err := s.st.Secret(name)
	exists := err == nil
	if err != nil && !errors.Is(err, store.ErrNoSecret) {
		s.error(w, err)
		return
	}
	if c.agent {
		note := old.Note
		if req.Note != nil {
			note = strings.TrimSpace(*req.Note)
		}
		cf := s.asks.addNeed(api.Need{Kind: api.NeedSecretSet, Name: name, Note: note, Replace: exists, Asker: c.Name, Session: c.session})
		s.logf("server: %s asks a person for the secret %s, need %s", c.Name, name, cf.ID)
		reply(w, http.StatusAccepted, api.Pending{Need: cf})
		return
	}
	if !c.Host() {
		forbid(w, errHostSecrets)
		return
	}
	if !c.panel() {
		if req.Scope != "" || req.Workspaces != nil || req.Always != nil {
			forbid(w, errors.New("only a web panel sets who may use a credential"))
			return
		}
	}
	if req.Value == "" && !exists {
		fail(w, http.StatusBadRequest, "no value")
		return
	}
	sec := old
	if !exists {
		sec = store.Secret{Name: name, Scope: string(api.ScopeAll)}
	}
	if req.Note != nil {
		sec.Note = strings.TrimSpace(*req.Note)
	}
	if req.Scope != "" {
		sec.Scope = string(req.Scope)
		sec.Workspaces = []int64{}
		if req.Scope == api.ScopeWorkspaces {
			sec.Workspaces = slices.Compact(slices.Sorted(slices.Values(req.Workspaces)))
		}
	}
	if req.Always != nil {
		sec.Always = *req.Always
	}
	if req.Value != "" {
		sec.By = c.ID()
		err = s.st.PutSecret(sec, req.Value)
	} else {
		err = s.st.SetSecretMeta(sec)
	}
	if err != nil {
		s.error(w, err)
		return
	}
	s.reloadSecrets()
	s.logf("server: %s stored the secret %s", c.Name, name)
	s.replySecret(w, name)
}

func (s *Server) replySecret(w http.ResponseWriter, name string) {
	sec, err := s.st.Secret(name)
	if err != nil {
		s.error(w, err)
		return
	}
	counts, last, _ := s.st.SecretUseCounts()
	reply(w, http.StatusOK, s.secretInfo(sec, counts, last))
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.st.DeleteSecret(name)
	if errors.Is(err, store.ErrNoSecret) {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.error(w, err)
		return
	}
	s.reloadSecrets()
	s.logf("server: %s deleted the secret %s", callerOf(r).Name, name)
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// ownSession reports whether session (the caller's) is the owner's own: run by a person at the
// machine, opened by none through Iridium or by 本机, or by a terminal that has Claude.
func (s *Server) ownSession(c caller, session string) bool {
	if c.machine() {
		return true
	}
	id := ""
	if session != "" {
		id = s.st.Opener(session)
	}
	if id == "" || id == gate.HostID {
		return true
	}
	info, ok := s.gate().Endpoint(id)
	return ok && access.HasClaude(s.rulesOf(info.ID, false))
}

// useSecrets gives ir run the values it asked for, if their scopes let the session have them and
// no panel must allow it first (202: a secret-use confirmation; ir run asks again with its ID once
// it is allowed). Each use is recorded. On the unix socket only: values never go over the network.
func (s *Server) useSecrets(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.socket {
		forbid(w, errors.New("only ir run on this machine uses credentials"))
		return
	}
	var req api.UseSecrets
	if !readJSON(w, r, &req) {
		return
	}
	var names []string
	for _, n := range req.Names {
		if !secrets.ValidName(n) {
			fail(w, http.StatusBadRequest, "bad secret name "+n)
			return
		}
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 || len(names) > 32 {
		fail(w, http.StatusBadRequest, "name 1 to 32 secrets")
		return
	}
	session := c.session
	if session == "" {
		session = req.Session
	}
	command := req.Command
	if utf8.RuneCountInString(command) > 120 {
		command = string([]rune(command)[:119]) + "…"
	}
	own := s.ownSession(c, session)
	var need []string
	always := false
	for _, n := range names {
		sec, err := s.st.Secret(n)
		if errors.Is(err, store.ErrNoSecret) {
			fail(w, http.StatusNotFound, "no secret "+n+" (ir secret set "+n+")")
			return
		}
		if err != nil {
			s.error(w, err)
			return
		}
		if !c.machine() {
			switch api.SecretScope(sec.Scope) {
			case api.ScopeOwner:
				if !own {
					forbid(w, errors.New(n+" is only for the sessions the owner's own terminals opened"))
					return
				}
			case api.ScopeWorkspaces:
				st, err := s.m.State(session)
				if err != nil || !slices.Contains(sec.Workspaces, st.Workspace) {
					forbid(w, errors.New(n+" is not for this session's workspace"))
					return
				}
			}
			if sec.Always || (!own && !s.vault.allowed(s, session, n)) {
				need = append(need, n)
				always = always || sec.Always
			}
		}
	}
	if len(need) > 0 && !s.vault.use(req.Confirm, session, need) {
		cf := s.asks.addNeed(api.Need{Kind: api.NeedSecretUse, Secrets: need, Command: command, Always: always, Owner: true, Asker: c.Name, Session: session})
		s.logf("server: %s asks a person to use %s, need %s", c.Name, strings.Join(need, ", "), cf.ID)
		reply(w, http.StatusAccepted, api.Pending{Need: cf})
		return
	}
	out := api.SecretValues{Values: map[string]string{}}
	for _, n := range names {
		v, err := s.st.SecretValue(n)
		if err != nil {
			s.error(w, err)
			return
		}
		out.Values[n] = v
	}
	for _, n := range names {
		if err := s.st.AddSecretUse(store.SecretUse{Secret: n, Session: session, Terminal: c.ID(), Command: command}); err != nil {
			s.logf("server: a use of %s: %v", n, err)
		}
	}
	s.logf("server: %s used %s (%s)", c.Name, strings.Join(names, ", "), command)
	reply(w, http.StatusOK, out)
}

// allowSecretUse is a panel allowing secret-use confirmation cf: once, or for the rest of the
// session's process (forSession; not for a secret confirmed every time).
func (s *Server) allowSecretUse(cf api.Need, forSession bool) {
	v := s.vault
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	for id, g := range v.grants {
		if now.Sub(g.at) > NeedTTL {
			delete(v.grants, id)
		}
	}
	v.grants[cf.ID] = grant{session: cf.Session, names: cf.Secrets, at: now}
	if !forSession || cf.Always || cf.Session == "" {
		return
	}
	proc := 0
	if st, err := s.m.State(cf.Session); err == nil {
		proc = st.Proc
	}
	a := v.sessions[cf.Session]
	if a.names == nil || a.proc != proc {
		a = allowance{proc: proc, names: map[string]bool{}}
	}
	for _, n := range cf.Secrets {
		a.names[n] = true
	}
	v.sessions[cf.Session] = a
}

// allowed reports whether session may use secret name without asking: a panel allowed it for the
// session, and the session's process is the one it was allowed for (one that stopped forgets).
func (v *vault) allowed(s *Server, session, name string) bool {
	if session == "" {
		return false
	}
	v.mu.Lock()
	a, ok := v.sessions[session]
	v.mu.Unlock()
	if !ok || !a.names[name] {
		return false
	}
	if st, err := s.m.State(session); err == nil && (st.Proc != a.proc || (st.Hosted && !st.Live)) {
		v.mu.Lock()
		delete(v.sessions, session)
		v.mu.Unlock()
		return false
	}
	return true
}

// use spends allowed confirmation id on names for session: true if it covers them.
func (v *vault) use(id, session string, names []string) bool {
	if id == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	g, ok := v.grants[id]
	if !ok || g.session != session || time.Since(g.at) > NeedTTL {
		return false
	}
	for _, n := range names {
		if !slices.Contains(g.names, n) {
			return false
		}
	}
	delete(v.grants, id)
	return true
}

// storeAsked stores the value panel c typed for secret-set confirmation cf: the note it was asked
// with, the scope it had (a new one: all).
func (s *Server) storeAsked(cf api.Need, value string, c caller) error {
	sec, err := s.st.Secret(cf.Name)
	if errors.Is(err, store.ErrNoSecret) {
		sec, err = store.Secret{Name: cf.Name, Scope: string(api.ScopeAll)}, nil
	}
	if err != nil {
		return err
	}
	sec.Note, sec.By = cf.Note, c.ID()
	if err := s.st.PutSecret(sec, value); err != nil {
		return err
	}
	s.reloadSecrets()
	s.logf("server: %s stored the secret %s for %s", c.Name, cf.Name, cf.Asker)
	return nil
}
