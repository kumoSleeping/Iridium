package gate

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

//go:embed web
var webFS embed.FS

var pages = map[string][2]string{ // path → file, content type
	"/_ir/login":    {"web/gate.html", "text/html; charset=utf-8"},
	"/_ir/join":     {"web/gate.html", "text/html; charset=utf-8"},
	"/_ir/gate.js":  {"web/gate.js", "text/javascript; charset=utf-8"},
	"/_ir/gate.css": {"web/gate.css", "text/css; charset=utf-8"},
}

const gateCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; manifest-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'; require-trusted-types-for 'script'"

const maxBody = 64 << 10

func (g *Gate) gateHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", gateCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if g.secure {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
}

// serveGate answers /_ir/*. Its pages and ceremonies are open to anyone (they make
// their own checks); the avatar route needs the same login as the app.
func (g *Gate) serveGate(w http.ResponseWriter, r *http.Request) {
	g.gateHeaders(w.Header())
	p := r.URL.Path
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case isUpgrade(r):
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
	case (p == "/_ir" || p == "/_ir/") && get:
		http.Redirect(w, r, "/_ir/login", http.StatusFound)
	case p == "/_ir/health" && get:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case pages[p][0] != "" && get:
		if !g.public && strings.HasPrefix(pages[p][1], "text/html") {
			http.Redirect(w, r, "/", http.StatusFound) // nothing to log in to
			return
		}
		data, _ := webFS.ReadFile(pages[p][0])
		w.Header().Set("Content-Type", pages[p][1])
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	case p == "/_ir/api/session" && get:
		g.apiSession(w, r)
	case strings.HasPrefix(p, "/_ir/avatar/") && get:
		g.serveAvatar(w, r, strings.TrimPrefix(p, "/_ir/avatar/"))
	case strings.HasPrefix(p, "/_ir/api/") && r.Method == http.MethodPost && g.public:
		g.serveAPI(w, r)
	default:
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
	}
}

func (g *Gate) serveAPI(w http.ResponseWriter, r *http.Request) {
	if !g.originOK(r) {
		writeJSON(w, http.StatusForbidden, apiError{"bad_origin"})
		return
	}
	if r.URL.Path == "/_ir/api/logout" {
		g.apiLogout(w, r)
		return
	}
	key := clientKey(r, g.ipHeader)
	if d := g.lim.wait(key, g.now()); d > 0 {
		tooMany(w, d)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	switch r.URL.Path {
	case "/_ir/api/join/check":
		g.apiJoinCheck(w, r, key)
	case "/_ir/api/join/options":
		g.apiJoinOptions(w, r, key)
	case "/_ir/api/join/verify":
		g.apiJoinVerify(w, r, key)
	case "/_ir/api/login/options":
		g.apiLoginOptions(w, r)
	case "/_ir/api/login/verify":
		g.apiLoginVerify(w, r, key)
	default:
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{"bad_request"})
		return false
	}
	return true
}

func sessionCookie(token string) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: token, Path: "/", MaxAge: int(AbsoluteTimeout / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
}

func clearedCookie() *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
}

// failed counts a failure against the client and answers.
func (g *Gate) failed(w http.ResponseWriter, key, what string, status int, code string, err error) {
	g.lim.fail(key, g.now())
	args := []any{"client", key}
	var pe *protocol.Error
	if errors.As(err, &pe) {
		args = append(args, "reason", pe.Details) // never DevInfo, which echoes request data
	} else if err != nil {
		args = append(args, "reason", err.Error())
	}
	g.log.Info("gate: "+what+" failed", args...)
	writeJSON(w, status, apiError{code})
}

type sessionReply struct {
	Authenticated bool   `json:"authenticated"`
	Name          string `json:"name,omitempty"`
	Local         bool   `json:"local,omitempty"` // local-only mode: no login
	Setup         bool   `json:"setup,omitempty"` // no panel has a passkey yet
}

func (g *Gate) apiSession(w http.ResponseWriter, r *http.Request) {
	if !g.public {
		writeJSON(w, http.StatusOK, sessionReply{Authenticated: true, Name: HostName, Local: true})
		return
	}
	a, ok, _ := g.identify(r, g.now())
	g.mu.Lock()
	reply := sessionReply{Authenticated: ok, Name: a.id.Name, Setup: !g.hasPasskeyLocked()}
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, reply)
}

func (g *Gate) apiLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil && wellFormed(c.Value) {
		h := hashToken(c.Value)
		g.mu.Lock()
		if _, ok := g.st.sessions[h]; ok {
			g.commitLocked(Change{DeleteSessions: []string{h}})
		}
		g.mu.Unlock()
		g.closeConns(func(c *conn) bool { return c.session == h })
	}
	http.SetCookie(w, clearedCookie())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// inviteLocked finds the live invite for token.
func (g *Gate) inviteLocked(token string, now time.Time) (Invite, bool) {
	if !wellFormed(token) {
		return Invite{}, false
	}
	h := hashToken(token)
	for _, inv := range g.st.invites {
		if hashEqual(inv.Hash, h) && (inv.Setup || now.Before(inv.ExpiresAt)) {
			return inv, true
		}
	}
	return Invite{}, false
}

type joinRequest struct {
	Token string  `json:"token"`
	Name  *string `json:"name"`
}

type joinReply struct {
	Name      string `json:"name,omitempty"` // the inviter's suggestion
	NameError string `json:"nameError,omitempty"`
}

// apiJoinCheck validates an invite token and, if given, a name (the page checks as you type).
func (g *Gate) apiJoinCheck(w http.ResponseWriter, r *http.Request, key string) {
	var req joinRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	g.mu.Lock()
	inv, ok := g.inviteLocked(req.Token, g.now())
	var reply joinReply
	if ok {
		reply.Name = inv.Name
		if req.Name != nil {
			_, err := g.checkNameLocked(*req.Name, KindPanel, "", inv.ID)
			reply.NameError = nameCode(err)
		}
	}
	g.mu.Unlock()
	if !ok {
		g.failed(w, key, "invite", http.StatusForbidden, "invalid_invite", nil)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

type ceremony struct {
	Flow    string `json:"flow"`
	Options any    `json:"options"` // publicKey options for navigator.credentials
}

func (g *Gate) apiJoinOptions(w http.ResponseWriter, r *http.Request, key string) {
	var req joinRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := g.now()
	g.mu.Lock()
	inv, ok := g.inviteLocked(req.Token, now)
	var name string
	var err error
	if ok {
		raw := inv.Name
		if req.Name != nil {
			raw = *req.Name
		}
		name, err = g.checkNameLocked(raw, KindPanel, "", inv.ID)
	}
	g.mu.Unlock()
	if !ok {
		g.failed(w, key, "invite", http.StatusForbidden, "invalid_invite", nil)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, joinReply{NameError: nameCode(err)})
		return
	}
	// Each registration gets its own random user handle and no excludeCredentials. A
	// synced provider (iCloud Keychain, Google Password Manager) shared by two devices may
	// know a passkey it cannot use on this one: excluding it would block the new device,
	// and reusing the handle would make the provider overwrite the existing passkey.
	handle := randomBytes(32)
	creation, session, err := g.wa.BeginRegistration(waUser{Endpoint{Name: name, UserHandle: handle}},
		webauthn.WithCredentialParameters(webauthn.CredentialParametersRecommendedL3()))
	if err != nil {
		g.log.Error("gate: registration options", "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	id := g.flows.put(&flow{kind: flowJoin, session: session, inviteID: inv.ID, name: name, handle: handle}, now)
	writeJSON(w, http.StatusOK, ceremony{Flow: id, Options: creation.Response})
}

type verifyRequest struct {
	Flow     string          `json:"flow"`
	Response json.RawMessage `json:"response"`
}

func (g *Gate) apiJoinVerify(w http.ResponseWriter, r *http.Request, key string) {
	var req verifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := g.now()
	f := g.flows.take(req.Flow, flowJoin, now)
	if f == nil {
		g.failed(w, key, "join", http.StatusForbidden, "flow_expired", errors.New("unknown or expired flow"))
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Response)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = g.wa.CreateCredential(waUser{Endpoint{Name: f.name, UserHandle: f.handle}}, *f.session, parsed)
	}
	if err != nil {
		g.failed(w, key, "join", http.StatusBadRequest, "verification_failed", err)
		return
	}

	// The ceremony took time: check the invite and the name again.
	g.mu.Lock()
	inv, ok := g.st.invites[f.inviteID]
	if !ok || (!inv.Setup && !now.Before(inv.ExpiresAt)) {
		g.mu.Unlock()
		writeJSON(w, http.StatusForbidden, apiError{"invalid_invite"})
		return
	}
	name, err := g.checkNameLocked(f.name, KindPanel, "", inv.ID)
	if err != nil {
		g.mu.Unlock()
		writeJSON(w, http.StatusConflict, joinReply{NameError: nameCode(err)})
		return
	}
	for _, e := range g.st.endpoints {
		for _, pk := range e.Passkeys {
			if bytes.Equal(pk.Credential.ID, cred.ID) {
				g.mu.Unlock()
				writeJSON(w, http.StatusConflict, apiError{"already_registered"})
				return
			}
		}
	}
	ep := Endpoint{
		ID: g.newEndpointIDLocked(), Name: name, Kind: KindPanel, UserHandle: f.handle,
		Passkeys:  []Passkey{{Credential: *cred, CreatedAt: now, LastUsedAt: now}},
		CreatedAt: now, LastSeenAt: now, ApprovedAt: now,
	}
	token, s := g.newSessionLocked(ep.ID, now)
	first := !g.hasPanelLocked()
	// With a panel there, the first-run link has done its part, whichever invite was used.
	del := append(g.setupInvitesLocked(), inv.ID)
	err = g.commitLocked(Change{DeleteInvites: del, Endpoints: []Endpoint{ep}, Sessions: []Session{s}})
	g.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	g.removeSetupFile()
	g.lim.success(key)
	g.log.Info("gate: panel joined", "endpoint", ep.ID, "name", ep.Name, "client", key, "setup", inv.Setup, "perms", len(inv.Perms) > 0, "first", first)
	if g.cfg.OnJoin != nil {
		g.cfg.OnJoin(Joined{Endpoint: infoOf(ep), Perms: inv.Perms, First: first})
	}
	http.SetCookie(w, sessionCookie(token))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

func (g *Gate) apiLoginOptions(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	has := g.hasPasskeyLocked()
	g.mu.Unlock()
	if !has {
		writeJSON(w, http.StatusConflict, apiError{"no_passkey"})
		return
	}
	// Discoverable login: no credential IDs go to anonymous visitors; the browser offers
	// the passkeys it holds for this site.
	assertion, session, err := g.wa.BeginDiscoverableLogin()
	if err != nil {
		g.log.Error("gate: login options", "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	id := g.flows.put(&flow{kind: flowLogin, session: session}, g.now())
	writeJSON(w, http.StatusOK, ceremony{Flow: id, Options: assertion.Response})
}

var (
	errUnknownCredential = errors.New("unknown credential")
	errCounter           = errors.New("signature counter did not increase")
)

func (g *Gate) apiLoginVerify(w http.ResponseWriter, r *http.Request, key string) {
	var req verifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := g.now()
	f := g.flows.take(req.Flow, flowLogin, now)
	if f == nil {
		g.failed(w, key, "login", http.StatusForbidden, "flow_expired", errors.New("unknown or expired flow"))
		return
	}
	var epID string
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, e := range g.st.endpoints {
			if e.Kind != KindPanel || !bytes.Equal(e.UserHandle, userHandle) {
				continue
			}
			for _, pk := range e.Passkeys {
				if bytes.Equal(pk.Credential.ID, rawID) {
					epID = e.ID
					return waUser{e}, nil
				}
			}
		}
		return nil, errUnknownCredential
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Response)
	var cred *webauthn.Credential
	if err == nil {
		_, cred, err = g.wa.ValidatePasskeyLogin(handler, *f.session, parsed)
	}
	if err == nil && cred.Authenticator.CloneWarning {
		err = errCounter // a copy of the key is in use somewhere
	}
	if err != nil {
		g.failed(w, key, "login", http.StatusForbidden, "auth_failed", err)
		return
	}

	g.mu.Lock()
	e, ok := g.st.endpoints[epID]
	i := -1
	if ok {
		i = slices.IndexFunc(e.Passkeys, func(pk Passkey) bool { return bytes.Equal(pk.Credential.ID, cred.ID) })
	}
	if i < 0 { // revoked during the ceremony
		g.mu.Unlock()
		writeJSON(w, http.StatusForbidden, apiError{"auth_failed"})
		return
	}
	e.Passkeys = slices.Clone(e.Passkeys)
	e.Passkeys[i].Credential = *cred
	e.Passkeys[i].LastUsedAt = now
	e.LastSeenAt = now
	token, s := g.newSessionLocked(e.ID, now)
	err = g.commitLocked(Change{Endpoints: []Endpoint{e}, Sessions: []Session{s}})
	g.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	g.lim.success(key)
	g.log.Info("gate: panel logged in", "endpoint", e.ID, "name", e.Name, "client", key)
	http.SetCookie(w, sessionCookie(token))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": e.Name})
}

// serveAvatar serves an endpoint's avatar to anyone logged in. With the current
// version in ?v= it may be cached for good.
func (g *Gate) serveAvatar(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := g.authorize(w, r); !ok {
		return
	}
	g.mu.Lock()
	e, ok := g.st.endpoints[id]
	g.mu.Unlock()
	if !ok || len(e.Avatar) == 0 {
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", http.DetectContentType(e.Avatar))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("ETag", `"`+e.AvatarVersion+`"`)
	if r.URL.Query().Get("v") == e.AvatarVersion {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(e.Avatar))
}

// waUser presents an endpoint to the WebAuthn library.
type waUser struct{ e Endpoint }

func (u waUser) WebAuthnID() []byte          { return u.e.UserHandle }
func (u waUser) WebAuthnName() string        { return u.e.Name }
func (u waUser) WebAuthnDisplayName() string { return u.e.Name }
func (u waUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, len(u.e.Passkeys))
	for i, pk := range u.e.Passkeys {
		out[i] = pk.Credential
	}
	return out
}
