package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// GitHub (docs/api.md "GitHub", kumo 2026-11-27): a person binds their own GitHub account to their
// login from a panel; an agent's git and gh (the helper and the shim Iridium puts in its
// environment, ir github) then act as the person whose message its turn runs for. The token goes
// only to the agent's ir, never out of the API otherwise, and is masked as the secrets are.

// githubAPI is where a token is checked (a test's server in tests).
var githubAPI = "https://api.github.com"

var (
	errGitHubPanel = errors.New("a person binds their own GitHub account, from a panel or the host")
	errGitHubAgent = errors.New("only an agent's git and gh (ir github) take a token")
)

func (s *Server) githubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/github", s.listGitHub)
	mux.HandleFunc("PUT /v1/internal/github", s.bindGitHub)
	mux.HandleFunc("DELETE /v1/internal/github", s.unbindGitHub)
	mux.HandleFunc("DELETE /v1/internal/github/{member}", s.unbindGitHub)
	mux.HandleFunc("GET /v1/internal/github/token", s.githubToken)
	mux.HandleFunc("POST /v1/internal/github/device", s.beginGitHubDevice)
	mux.HandleFunc("POST /v1/internal/github/device/{id}", s.pollGitHubDevice)
}

// githubMember is the binding a caller's own is kept under: its login's ID, the host's "local".
func githubMember(c caller) string { return c.ID() }

// turnMember is whose GitHub session id's turn now runs as: the person its turn is for.
func (s *Server) turnMember(session string) string {
	if session == "" {
		return gate.HostID
	}
	o, ok := s.originOf(session)
	if !ok {
		return ""
	}
	return o.ID()
}

func (s *Server) listGitHub(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	all, err := s.st.GitHubs()
	if err != nil {
		s.error(w, err)
		return
	}
	out := api.GitHubList{Bindings: []api.GitHubBinding{}}
	if !c.byAgent() {
		out.Mine = githubMember(c)
	}
	turn := ""
	if c.session != "" {
		turn = s.turnMember(c.session)
	}
	for _, g := range all {
		out.Bindings = append(out.Bindings, api.GitHubBinding{Member: g.Member, Name: s.terminalName(g.Member), Login: g.Login, ID: g.ID, Avatar: g.Avatar, At: g.At})
		if g.Member == turn {
			out.Turn = g.Member
		}
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) bindGitHub(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.panel() {
		forbid(w, errGitHubPanel)
		return
	}
	var req api.SetGitHub
	if !readJSON(w, r, &req) {
		return
	}
	tok := strings.TrimSpace(req.Token)
	if tok == "" || len(tok) > 4096 {
		refuse(w, http.StatusUnprocessableEntity, "bad_token", "no token")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	b, err := s.keepGitHub(ctx, githubMember(c), tok)
	if err != nil {
		refuse(w, http.StatusUnprocessableEntity, "bad_token", err.Error())
		return
	}
	reply(w, http.StatusOK, b)
}

// unbindGitHub: one's own; a person on a panel any (a trusted team: kumo, 2026-11-26).
func (s *Server) unbindGitHub(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.panel() {
		forbid(w, errGitHubPanel)
		return
	}
	member := r.PathValue("member")
	if member == "" {
		member = githubMember(c)
	}
	if err := s.st.DeleteGitHub(member); err != nil {
		s.error(w, err)
		return
	}
	s.reloadSecrets()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// githubToken: the token an agent's git or gh is to use now: user's when it names one (its
// member ID, name or GitHub login), else the person's its turn runs for; none: 204 (git and gh
// go on as the machine's own).
func (s *Server) githubToken(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.session == "" && !c.machine() {
		forbid(w, errGitHubAgent)
		return
	}
	member := s.turnMember(c.session)
	if u := strings.TrimSpace(r.URL.Query().Get("user")); u != "" {
		all, err := s.st.GitHubs()
		if err != nil {
			s.error(w, err)
			return
		}
		member = ""
		var names []string
		for _, g := range all {
			name := s.terminalName(g.Member)
			if strings.EqualFold(u, g.Member) || strings.EqualFold(u, name) || strings.EqualFold(strings.TrimPrefix(u, "@"), g.Login) {
				member = g.Member
				break
			}
			names = append(names, fmt.Sprintf("%s (@%s)", name, g.Login))
		}
		if member == "" {
			refuse(w, http.StatusUnprocessableEntity, "no_github", fmt.Sprintf("no GitHub bound for %q; bound: %s", u, strings.Join(names, ", ")))
			return
		}
	}
	g, tok, ok, err := s.st.GitHubToken(member)
	if err != nil {
		s.error(w, err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	reply(w, http.StatusOK, api.GitHubToken{Member: g.Member, Login: g.Login, Token: tok})
}

type githubWho struct {
	Login  string `json:"login"`
	ID     int64  `json:"id"`
	Avatar string `json:"avatar_url"`
}

// githubUser is the account tok is of, as GitHub says.
func githubUser(ctx context.Context, tok string) (githubWho, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPI+"/user", nil)
	if err != nil {
		return githubWho{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return githubWho{}, fmt.Errorf("GitHub not reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return githubWho{}, errors.New("GitHub refused the token")
	}
	if resp.StatusCode != http.StatusOK {
		return githubWho{}, fmt.Errorf("GitHub: %s", resp.Status)
	}
	var who githubWho
	if err := json.NewDecoder(resp.Body).Decode(&who); err != nil || who.Login == "" {
		return githubWho{}, errors.New("GitHub gave no account")
	}
	return who, nil
}
