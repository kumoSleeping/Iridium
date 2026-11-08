package server

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// Pairing (docs/exec.md "连接"): an executor that has no TOKEN asks at POST /api/exec/pair, a Bot
// at POST /api/pair (the user, 2026-11-08: the one way a Bot comes in, no TOKEN handed out by
// hand), the doors past the gate without a login, and gets nothing but a code and a request to
// ask after. A panel sees an exec-pair or bot-pair confirmation with the same code; allowed, a new
// Bot is made (or the one by its name given a new TOKEN, Replace) and its TOKEN waits for the
// asker's GET /api/pair/{request}, once. The settings' pairClosed turns every request away.

// Pairing limits, loose (the user, 2026-11-08: pairClosed is the stop): requests per client an
// hour, pending ones per client and in all, the body.
const (
	pairPerHour       = 30
	pairPendingClient = 4
	pairPending       = 20
	pairBody          = 16 << 10
)

type pairs struct {
	mu    sync.Mutex
	reqs  map[string]*pairReq // by request
	tries map[string][]time.Time
}

type pairReq struct {
	confirm string
	client  string
	at      time.Time
	token   string // once allowed, till the executor takes it
	taken   bool
}

func newPairs() *pairs { return &pairs{reqs: map[string]*pairReq{}, tries: map[string][]time.Time{}} }

// openPath reports whether r is a pairing request: the gate lets these through without a login.
func openPath(r *http.Request) bool {
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == "/api/exec/pair" || r.URL.Path == "/api/pair"):
		return true
	case r.Method == http.MethodGet && (strings.HasPrefix(r.URL.Path, "/api/exec/pair/") || strings.HasPrefix(r.URL.Path, "/api/pair/")):
		return true
	}
	return false
}

func (s *Server) pairRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/exec/pair", func(w http.ResponseWriter, r *http.Request) { s.pairAsk(w, r, "exec-pair") })
	mux.HandleFunc("POST /api/pair", func(w http.ResponseWriter, r *http.Request) { s.pairAsk(w, r, "bot-pair") })
	mux.HandleFunc("GET /api/exec/pair/{request}", s.pairState)
	mux.HandleFunc("GET /api/pair/{request}", s.pairState)
}

// pairAsk takes an executor's (exec-pair) or a Bot's (bot-pair) request to pair: a code to show,
// and the request to ask after.
func (s *Server) pairAsk(w http.ResponseWriter, r *http.Request, kind string) {
	if kind == "exec-pair" && s.Remote == nil {
		fail(w, http.StatusNotFound, "no executors here")
		return
	}
	s.cmu.Lock()
	closed := s.cfg.PairClosed
	s.cmu.Unlock()
	if closed {
		fail(w, http.StatusForbidden, "not taking pairing requests")
		return
	}
	var m api.ExecPair
	r.Body = http.MaxBytesReader(w, r.Body, pairBody)
	if !readJSON(w, r, &m) {
		return
	}
	m = cleanPair(m)
	if kind == "bot-pair" {
		m = api.ExecPair{Name: m.Name, Program: m.Program}
		if m.Name == "machine" {
			m.Name = "bot"
		}
	}
	old, taken := s.gate().Find(m.Name)
	client := s.gate().ClientKey(r)
	now := time.Now()
	ps := s.pairs
	ps.mu.Lock()
	s.prunePairsLocked(now)
	tries := ps.tries[client]
	pending, mine := 0, 0
	for _, q := range ps.reqs {
		if _, ok := s.asks.peek(q.confirm); ok {
			pending++
			if q.client == client {
				mine++
			}
		}
	}
	if len(tries) >= pairPerHour || mine >= pairPendingClient || pending >= pairPending {
		ps.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(int(ConfirmTTL/time.Second)))
		fail(w, http.StatusTooManyRequests, "too many pairing requests: try again later")
		return
	}
	ps.tries[client] = append(tries, now)
	code := pairCode()
	cf := s.asks.addConfirm(api.Confirm{Kind: kind, Name: m.Name, Code: code, Machine: &m, Asker: m.Name, Replace: taken && old.Kind == gate.KindChannel})
	req := randHex(16)
	ps.reqs[req] = &pairReq{confirm: cf.ID, client: client, at: now}
	ps.mu.Unlock()
	s.logf("server: %s (%s, %s) asks to pair (%s), confirmation %s", m.Name, cmp.Or(m.OS, m.Program), client, kind, cf.ID)
	reply(w, http.StatusOK, api.ExecPairing{Code: code, Request: req})
}

// pairState is how a pairing request stands; allowed, the TOKEN, given once.
func (s *Server) pairState(w http.ResponseWriter, r *http.Request) {
	ps := s.pairs
	ps.mu.Lock()
	defer ps.mu.Unlock()
	s.prunePairsLocked(time.Now())
	q := ps.reqs[r.PathValue("request")]
	if q == nil || q.taken {
		fail(w, http.StatusNotFound, "no such pairing request")
		return
	}
	st, ok := s.asks.wait(r.Context(), q.confirm, 0)
	out := api.ExecPairState{Status: "pending"}
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

// prunePairsLocked forgets requests past their confirmation's life and the answer's keeping, and
// tries older than an hour.
func (s *Server) prunePairsLocked(now time.Time) {
	ps := s.pairs
	for id, q := range ps.reqs {
		if now.Sub(q.at) > ConfirmTTL+KeepAnswered {
			delete(ps.reqs, id)
		}
	}
	for c, ts := range ps.tries {
		i := 0
		for i < len(ts) && now.Sub(ts[i]) > time.Hour {
			i++
		}
		if i == len(ts) {
			delete(ps.tries, c)
		} else {
			ps.tries[c] = ts[i:]
		}
	}
}

// pairAllowed keeps tok for the request of exec-pair confirmation cf.
func (s *Server) pairAllowed(cf, tok string) {
	ps := s.pairs
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, q := range ps.reqs {
		if q.confirm == cf {
			q.token = tok
		}
	}
}

// makePairedBot makes the Bot an allowed pairing connects as: a new terminal by the name it asked
// with, or the first of name-2, name-3… free (it never takes over another's TOKEN unasked);
// ownerBy, Claude for it. replace: the Bot by that name, a new TOKEN, its Claude as it was.
func (s *Server) makePairedBot(name, ownerBy string, replace bool) (*api.Token, error) {
	if replace {
		info, tok, err := s.gate().CreateToken(name)
		if err != nil {
			return nil, err
		}
		return &api.Token{Terminal: s.terminalOf(info), Token: tok}, nil
	}
	for i := 1; i < 100; i++ {
		n := name
		if i > 1 {
			suffix := "-" + strconv.Itoa(i)
			n = truncRunes(name, 12-len(suffix)) + suffix
		}
		info, tok, err := s.gate().CreateNewToken(n)
		if errors.Is(err, gate.ErrNameTaken) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ownerBy != "" {
			if err := s.giveClaude(info, ownerBy); err != nil {
				return nil, err
			}
		}
		return &api.Token{Terminal: s.terminalOf(info), Token: tok}, nil
	}
	return nil, fmt.Errorf("no free name for %s", name)
}

// cleanPair keeps what a pairing request says within bounds: a valid terminal name (else the
// system's), a known system, short strings, a few agents.
func cleanPair(m api.ExecPair) api.ExecPair {
	switch m.OS {
	case "windows", "darwin", "linux":
	default:
		m.OS = ""
	}
	if n, err := gate.NormalizeName(truncRunes(strings.TrimSpace(m.Name), 12)); err == nil {
		m.Name = n
	} else {
		m.Name = map[string]string{"windows": "windows", "darwin": "mac", "linux": "linux"}[m.OS]
		if m.Name == "" {
			m.Name = "machine"
		}
	}
	m.Arch, m.Program = truncRunes(m.Arch, 16), truncRunes(m.Program, 64)
	if len(m.Agents) > 8 {
		m.Agents = m.Agents[:8]
	}
	for i := range m.Agents {
		m.Agents[i].Agent, m.Agents[i].Version = truncRunes(m.Agents[i].Agent, 32), truncRunes(m.Agents[i].Version, 32)
	}
	return m
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
