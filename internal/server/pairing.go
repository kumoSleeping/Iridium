package server

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// Pairing (draft 二「接入」, decisions 6-11): the one way an app comes in, an executor (ir join)
// or any other (ir pair). POST /api/pair, past the gate without a login, only while the window is
// open (ir apps open, or the web for 10 minutes): a code to show and a request to ask after. A
// person sees an app_join need with the same code, or app_update when an app has the name already
// (no other name is made up: the person updates that one, renames this one, or denies). Allowed,
// the TOKEN waits for the asker's GET /api/pair/{request}, once.

// Pairing limits: pending requests per source (an IP, an IPv6 /64) and in all; the body; how
// long the window opens for by default and at most.
const (
	pairPendingSource = 4
	pairPending       = 20
	pairBody          = 16 << 10
	PairWindow        = 10 * time.Minute
	pairWindowMax     = time.Hour
)

type pairs struct {
	mu    sync.Mutex
	reqs  map[string]*pairReq // by request
	until time.Time           // the window is open till then
}

type pairReq struct {
	need   string
	source string
	at     time.Time
	token  string // once allowed, till the app takes it
	taken  bool
}

func newPairs() *pairs { return &pairs{reqs: map[string]*pairReq{}} }

// openPath reports whether r is a pairing request, Satori's event stream (its IDENTIFY carries the
// TOKEN), or the app's icons and manifest: the gate lets these through without a login. A browser
// fetches the manifest without cookies, and the login page is what a phone adds to its home screen
// first.
func openPath(r *http.Request) bool {
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(r.URL.Path == "/favicon.svg" || r.URL.Path == "/manifest.webmanifest" || strings.HasPrefix(r.URL.Path, "/icons/") && path.Clean(r.URL.Path) == r.URL.Path):
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/pair":
		return true
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/pair/"):
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/v1/events":
		return true // Satori's IDENTIFY carries the TOKEN (package satori checks it)
	}
	return false
}

func (s *Server) pairRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pair", s.pairAsk)
	mux.HandleFunc("GET /api/pair/{request}", s.pairState)
	// The window: whoever may approve (the host's ir apps open, a login of ir_web that may).
	mux.HandleFunc("GET /api/pairing", s.approver(func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.pairWindow()) }))
	mux.HandleFunc("PUT /api/pairing", s.approver(s.setPairing))
}

// pairWindow is how the window stands now.
func (s *Server) pairWindow() api.PairWindow {
	ps := s.pairs
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if time.Now().Before(ps.until) {
		return api.PairWindow{Open: true, Until: ps.until.UnixMilli()}
	}
	return api.PairWindow{}
}

func (s *Server) publishPairing() { s.hub.Set("pairing", s.pairWindow()) }

// OpenPairing opens the window for d (PairWindow if 0, at most an hour), or closes it (d < 0).
func (s *Server) OpenPairing(d time.Duration) api.PairWindow {
	ps := s.pairs
	ps.mu.Lock()
	if d < 0 {
		ps.until = time.Time{}
	} else {
		d = min(cmp.Or(d, PairWindow), pairWindowMax)
		ps.until = time.Now().Add(d)
		time.AfterFunc(d+50*time.Millisecond, s.publishPairing)
	}
	ps.mu.Unlock()
	s.publishPairing()
	return s.pairWindow()
}

func (s *Server) setPairing(w http.ResponseWriter, r *http.Request) {
	var req api.PairWindow
	if !readJSON(w, r, &req) {
		return
	}
	if req.Minutes < 0 || req.Minutes > int(pairWindowMax/time.Minute) {
		refuse(w, http.StatusUnprocessableEntity, "bad_minutes", "minutes: 1 to 60")
		return
	}
	d := time.Duration(req.Minutes) * time.Minute
	if !req.Open {
		d = -1
	}
	st := s.OpenPairing(d)
	s.logf("server: %s %s pairing", callerOf(r).Name, map[bool]string{true: "opened", false: "closed"}[st.Open])
	reply(w, http.StatusOK, st)
}

// pairAsk takes an app's request to pair: a code to show, and the request to ask after.
func (s *Server) pairAsk(w http.ResponseWriter, r *http.Request) {
	var m api.PairRequest
	r.Body = http.MaxBytesReader(w, r.Body, pairBody)
	if !readJSON(w, r, &m) {
		return
	}
	now := time.Now()
	if !s.pairWindow().Open {
		refuse(w, http.StatusForbidden, "pair_closed", "pairing is closed: open it on the host (ir apps open) or in 应用管理")
		return
	}
	asked := m.Wants != nil
	m = cleanPair(m)
	if s.Remote != nil && s.Remote.Self != "" && m.MachineID == s.Remote.Self {
		m.Agents = nil // an app on the host: its agents are the host's (decision 22)
	}
	if !asked && len(m.Agents) > 0 && m.Home != "" {
		// Decision 17: a new app carrying agents gets its own home, unless it asks for another.
		m.Wants = &api.Perms{Agents: []string{api.PermsAll}, Scope: []string{m.Name + ":" + m.Home}}
	}
	kind := api.NeedAppJoin
	var old *api.App
	if _, err := s.gate().CheckName(m.Name); errors.Is(err, gate.ErrNameTaken) {
		if info, ok := s.gate().Find(m.Name); ok && info.Kind == gate.KindChannel && !info.Host() {
			a := s.appOf(info)
			kind, old = api.NeedAppUpdate, &a
		} else {
			s.gateDone(w, err)
			return
		}
	} else if err != nil {
		s.gateDone(w, err)
		return
	}
	source := s.gate().ClientKey(r)
	ps := s.pairs
	ps.mu.Lock()
	s.prunePairsLocked(now)
	pending, mine := 0, 0
	for _, q := range ps.reqs {
		if _, ok := s.asks.peek(q.need); ok {
			pending++
			if q.source == source {
				mine++
			}
		}
	}
	if mine >= pairPendingSource || pending >= pairPending {
		ps.mu.Unlock()
		refuse(w, http.StatusForbidden, "too_many_pending", "too many pairing requests waiting: try again later")
		return
	}
	code := pairCode()
	nd := s.asks.addNeed(api.Need{Kind: kind, Name: m.Name, Code: code, Pair: &m, Asker: m.Name, App: old, Wants: m.Wants})
	req := randHex(16)
	ps.reqs[req] = &pairReq{need: nd.ID, source: source, at: now}
	ps.mu.Unlock()
	s.logf("server: %s (%s, %s) asks to pair (%s), need %s", m.Name, cmp.Or(m.OS, m.Program), source, kind, nd.ID)
	reply(w, http.StatusOK, api.Pairing{Code: code, Request: req})
}

// pairState is how a pairing request stands; allowed, the TOKEN, given once.
func (s *Server) pairState(w http.ResponseWriter, r *http.Request) {
	ps := s.pairs
	ps.mu.Lock()
	defer ps.mu.Unlock()
	s.prunePairsLocked(time.Now())
	q := ps.reqs[r.PathValue("request")]
	if q == nil || q.taken {
		refuse(w, http.StatusUnprocessableEntity, "no_such_request", "no such pairing request")
		return
	}
	st, ok := s.asks.wait(r.Context(), q.need, 0)
	out := api.PairState{Status: "pending"}
	switch {
	case !ok, st.State == "expired", st.State == "failed":
		out.Status = "expired"
	case st.State == "denied":
		out.Status = "denied"
	case st.State == "allowed" && q.token != "":
		out.Status, out.Token = "allowed", q.token
		q.token, q.taken = "", true
	case st.State == "allowed":
		out.Status = "expired"
	}
	reply(w, http.StatusOK, out)
}

// prunePairsLocked forgets requests past their need's life and the answer's keeping.
func (s *Server) prunePairsLocked(now time.Time) {
	for id, q := range s.pairs.reqs {
		if now.Sub(q.at) > NeedTTL+KeepAnswered {
			delete(s.pairs.reqs, id)
		}
	}
}

// pairToken keeps tok for the request of pairing need nd.
func (s *Server) pairToken(nd, tok string) {
	ps := s.pairs
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, q := range ps.reqs {
		if q.need == nd {
			q.token = tok
		}
	}
}

// checkPairAnswer checks an answer allowing pairing need nd before it is taken: the name it makes
// an app by is free, the app it updates is there, it gives no more than asked.
func (s *Server) checkPairAnswer(w http.ResponseWriter, nd api.Need, a api.NeedAnswer) bool {
	if nd.Kind == api.NeedAppUpdate && a.Name == "" {
		if _, ok := s.gate().Endpoint(nd.App.ID); !ok {
			refuse(w, http.StatusUnprocessableEntity, "no_such_app", "the app it would update is gone: allow it by another name")
			return false
		}
		return true
	}
	if _, err := s.gate().CheckName(cmp.Or(a.Name, nd.Name)); err != nil {
		s.gateDone(w, err)
		return false
	}
	return s.checkGiven(w, nd, a)
}

// checkGiven checks that an answer gives no more than need nd asked for.
func (s *Server) checkGiven(w http.ResponseWriter, nd api.Need, a api.NeedAnswer) bool {
	if a.Perms == nil {
		return true
	}
	if nd.Wants != nil && !permsWithin(cleanPerms(a.Perms), *nd.Wants) {
		refuse(w, http.StatusUnprocessableEntity, "too_wide", "an approval gives what was asked for or less")
		return false
	}
	return true
}

// pairAllowed makes what an allowed pairing need nd gives: a new app by its name (or the one
// the answer gave), or for app_update the app there with a new TOKEN, its old one void, its
// usage and permissions as they were. ownerBy (ownerSays's): Claude for a new app, if a.Owner.
func (s *Server) pairAllowed(nd api.Need, a api.NeedAnswer, ownerBy string) (*api.Token, error) {
	if nd.Kind == api.NeedAppUpdate && a.Name == "" {
		info, tok, err := s.gate().RenewToken(nd.App.ID)
		if err != nil {
			return nil, err
		}
		if s.Remote != nil {
			s.Remote.Drop(info.ID)
		}
		s.terminalsChanged()
		return &api.Token{App: s.appOf(info), Token: tok}, nil
	}
	typ := ""
	if nd.Pair != nil {
		typ = nd.Pair.Type
	}
	info, tok, err := s.gate().CreateApp(cmp.Or(a.Name, nd.Name), typ)
	if err != nil {
		return nil, err
	}
	perms := cmp.Or(a.Perms, nd.Wants)
	if perms != nil {
		p := cleanPerms(perms)
		if info.Name != nd.Name {
			p.Scope = renamedScope(p.Scope, nd.Name, info.Name)
		}
		if err := s.setAppPerms(info.ID, p); err != nil {
			return nil, err
		}
	}
	if off := cleanAgents(a.AgentsOff); len(off) > 0 {
		if _, err := s.st.SetAgentsOff(info.ID, off); err != nil {
			return nil, err
		}
	}
	if a.Owner && ownerBy != "" {
		if err := s.giveClaude(info, ownerBy); err != nil {
			return nil, err
		}
	}
	s.terminalsChanged()
	return &api.Token{App: s.appOf(info), Token: tok}, nil
}

func (s *Server) setAppPerms(id string, p api.Perms) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.st.SetAppPerms(id, b)
}

// appPerms is what app id was given; nil for all (one paired before 0.2).
func (s *Server) appPerms(id string) *api.Perms {
	b, err := s.st.AppPerms(id)
	if err != nil || len(b) == 0 {
		return nil
	}
	var p api.Perms
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

// renamedScope is scope with the places at app was (by its name) at app now: one allowed by another
// name than it asked with keeps its home.
func renamedScope(scope []string, was, now string) []string {
	out := slices.Clone(scope)
	for i, p := range out {
		if rest, ok := strings.CutPrefix(p, was+":"); ok {
			out[i] = now + ":" + rest
		}
	}
	return out
}

// cleanPair keeps what a pairing request says within bounds: its name as given (the gate judges
// it), a known system, short strings, a few agents, what it wants.
func cleanPair(m api.PairRequest) api.PairRequest {
	switch m.OS {
	case "windows", "darwin", "linux":
	default:
		m.OS = ""
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Type = truncRunes(strings.TrimSpace(m.Type), 16)
	if m.Type == "" && m.OS != "" {
		m.Type = "computer"
	}
	m.Arch, m.Program = truncRunes(m.Arch, 16), truncRunes(m.Program, 64)
	m.Home, m.MachineID = truncRunes(strings.TrimSpace(m.Home), 1024), truncRunes(m.MachineID, 128)
	if m.Shell == nil {
		on := m.AgentShell && !m.NoShell // an older executor's two
		m.Shell = &on
	}
	m.NoShell, m.AgentShell = false, false
	if len(m.Agents) > 8 {
		m.Agents = m.Agents[:8]
	}
	for i := range m.Agents {
		m.Agents[i].Agent, m.Agents[i].Version = truncRunes(m.Agents[i].Agent, 32), truncRunes(m.Agents[i].Version, 32)
	}
	w := cleanPerms(m.Wants)
	m.Wants = &w
	return m
}

// cleanPerms is p within bounds: nil is all; a list with "*" is ["*"]; no repeats.
func cleanPerms(p *api.Perms) api.Perms {
	if p == nil {
		return api.Perms{Agents: []string{api.PermsAll}, Scope: []string{api.PermsAll}}
	}
	list := func(in []string, n, max int) []string {
		out := []string{}
		for _, v := range in {
			v = truncRunes(strings.TrimSpace(v), max)
			if v == api.PermsAll {
				return []string{api.PermsAll}
			}
			if v != "" && !slices.Contains(out, v) && len(out) < n {
				out = append(out, v)
			}
		}
		return out
	}
	return api.Perms{Agents: list(p.Agents, 16, 32), Scope: list(p.Scope, 32, 1024)}
}

// permsWithin reports whether given is no more than wanted: its agents among wanted's, each of
// its places within one of wanted's.
func permsWithin(given, wanted api.Perms) bool {
	if !slices.Contains(wanted.Agents, api.PermsAll) {
		for _, a := range given.Agents {
			if !slices.Contains(wanted.Agents, a) {
				return false
			}
		}
	}
	if slices.Contains(wanted.Scope, api.PermsAll) {
		return true
	}
	for _, g := range given.Scope {
		if !slices.ContainsFunc(wanted.Scope, func(w string) bool { return placeWithin(g, w) }) {
			return false
		}
	}
	return true
}

// placeWithin: place p ("应用名:路径") is at or under place w.
func placeWithin(p, w string) bool {
	if p == w {
		return true
	}
	w = strings.TrimRight(w, `/\`)
	return strings.HasPrefix(p, w) && len(p) > len(w) && (p[len(w)] == '/' || p[len(w)] == '\\')
}

func truncRunes(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// pairCode is four random digits.
func pairCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	return fmt.Sprintf("%04d", n.Int64())
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
