package server

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Binding GitHub by a link (kumo, 2026-11-27): GitHub's device flow through Iridium's OAuth App,
// no secret and no way back to this server needed, so any Iridium, inside a network too, binds the
// same way. The server asks GitHub for a code, the page shows it with GitHub's link, and asks the
// server again every few seconds; the server asks GitHub once each time (never sooner than GitHub
// said) and, allowed, keeps the token as a pasted one is kept. The device code never leaves here.

// githubClientID is Iridium's own OAuth App (kumoSleeping's "Iridium"), config github.clientId
// another.
const githubClientID = "Ov23liZZDQnAyYHHCNfR"

// githubWeb is where the device flow is asked (a test's server in tests).
var githubWeb = "https://github.com"

// githubScopes: what gh asks for itself, and workflow so the Actions' files can be pushed.
const githubScopes = "repo read:org workflow"

type githubFlow struct {
	member   string
	device   string
	interval time.Duration
	next     time.Time
	expires  time.Time
}

var githubFlows = struct {
	sync.Mutex
	m map[string]*githubFlow
}{m: map[string]*githubFlow{}}

func (s *Server) githubClient() string {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return cmp.Or(s.cfg.GitHub.ClientID, githubClientID)
}

// githubPost posts form to GitHub's path, its JSON answer into out.
func githubPost(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", githubWeb+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub not reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *Server) beginGitHubDevice(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.panel() {
		forbid(w, errGitHubPanel)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var got struct {
		Device   string `json:"device_code"`
		User     string `json:"user_code"`
		URI      string `json:"verification_uri"`
		Expires  int    `json:"expires_in"`
		Interval int    `json:"interval"`
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
	}
	if err := githubPost(ctx, "/login/device/code", url.Values{"client_id": {s.githubClient()}, "scope": {githubScopes}}, &got); err != nil {
		refuse(w, http.StatusBadGateway, "github", err.Error())
		return
	}
	if got.Device == "" {
		refuse(w, http.StatusBadGateway, "github", cmp.Or(got.Desc, got.Error, "GitHub gave no code"))
		return
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	now := time.Now()
	f := &githubFlow{member: githubMember(c), device: got.Device, interval: time.Duration(max(got.Interval, 5)) * time.Second, expires: now.Add(time.Duration(cmp.Or(got.Expires, 900)) * time.Second)}
	f.next = now.Add(f.interval)
	githubFlows.Lock()
	for k, o := range githubFlows.m {
		if now.After(o.expires) {
			delete(githubFlows.m, k)
		}
	}
	githubFlows.m[id] = f
	githubFlows.Unlock()
	reply(w, http.StatusOK, api.GitHubDevice{ID: id, Code: got.User, URI: cmp.Or(got.URI, "https://github.com/login/device"), Interval: int(f.interval / time.Second), Expires: f.expires.UnixMilli()})
}

func (s *Server) pollGitHubDevice(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.panel() {
		forbid(w, errGitHubPanel)
		return
	}
	id := r.PathValue("id")
	githubFlows.Lock()
	f, ok := githubFlows.m[id]
	if ok && f.member != githubMember(c) {
		ok = false // another's
	}
	now := time.Now()
	if ok && now.After(f.expires) {
		delete(githubFlows.m, id)
		githubFlows.Unlock()
		reply(w, http.StatusOK, api.GitHubDeviceState{State: "expired"})
		return
	}
	early := ok && now.Before(f.next)
	if ok && !early {
		f.next = now.Add(f.interval)
	}
	githubFlows.Unlock()
	if !ok {
		refuse(w, http.StatusNotFound, "no_flow", "no such binding under way")
		return
	}
	if early {
		reply(w, http.StatusOK, api.GitHubDeviceState{State: "waiting"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var got struct {
		Token    string `json:"access_token"`
		Error    string `json:"error"`
		Interval int    `json:"interval"`
	}
	if err := githubPost(ctx, "/login/oauth/access_token", url.Values{"client_id": {s.githubClient()}, "device_code": {f.device}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &got); err != nil {
		refuse(w, http.StatusBadGateway, "github", err.Error())
		return
	}
	switch got.Error {
	case "":
	case "authorization_pending":
		reply(w, http.StatusOK, api.GitHubDeviceState{State: "waiting"})
		return
	case "slow_down":
		githubFlows.Lock()
		f.interval = time.Duration(max(got.Interval, int(f.interval/time.Second)+5)) * time.Second
		f.next = time.Now().Add(f.interval)
		githubFlows.Unlock()
		reply(w, http.StatusOK, api.GitHubDeviceState{State: "waiting"})
		return
	case "expired_token", "access_denied":
		githubFlows.Lock()
		delete(githubFlows.m, id)
		githubFlows.Unlock()
		reply(w, http.StatusOK, api.GitHubDeviceState{State: map[string]string{"expired_token": "expired", "access_denied": "denied"}[got.Error]})
		return
	default:
		refuse(w, http.StatusBadGateway, "github", "GitHub: "+got.Error)
		return
	}
	if got.Token == "" {
		refuse(w, http.StatusBadGateway, "github", "GitHub gave no token")
		return
	}
	githubFlows.Lock()
	delete(githubFlows.m, id)
	githubFlows.Unlock()
	b, err := s.keepGitHub(ctx, f.member, got.Token)
	if err != nil {
		refuse(w, http.StatusBadGateway, "github", err.Error())
		return
	}
	reply(w, http.StatusOK, api.GitHubDeviceState{State: "done", Binding: &b})
}

// keepGitHub checks tok with GitHub and keeps it as member's binding.
func (s *Server) keepGitHub(ctx context.Context, member, tok string) (api.GitHubBinding, error) {
	who, err := githubUser(ctx, tok)
	if err != nil {
		return api.GitHubBinding{}, err
	}
	g := store.GitHub{Member: member, Login: who.Login, ID: who.ID, Avatar: who.Avatar, At: time.Now().UnixMilli()}
	if err := s.st.PutGitHub(g, tok); err != nil {
		return api.GitHubBinding{}, errors.Join(errors.New("keeping it"), err)
	}
	s.reloadSecrets()
	return api.GitHubBinding{Member: g.Member, Name: s.terminalName(g.Member), Login: g.Login, ID: g.ID, Avatar: g.Avatar, At: g.At}, nil
}
