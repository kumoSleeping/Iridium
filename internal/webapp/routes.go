package webapp

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
	"github.com/kumoSleeping/iridium/internal/gate"
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

func (w *Web) gateHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", gateCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if w.secure {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
}

// serveGate answers /_ir/* but avatars (the core's). Its pages and ceremonies are open to anyone
// (they make their own checks).
func (w *Web) serveGate(rw http.ResponseWriter, r *http.Request) {
	w.gateHeaders(rw.Header())
	p := r.URL.Path
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case isUpgrade(r):
		writeJSON(rw, http.StatusNotFound, apiError{"not_found"})
	case (p == "/_ir" || p == "/_ir/") && get:
		http.Redirect(rw, r, "/_ir/login", http.StatusFound)
	case p == "/_ir/health" && get:
		writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
	case pages[p][0] != "" && get:
		if !w.public && strings.HasPrefix(pages[p][1], "text/html") {
			http.Redirect(rw, r, "/", http.StatusFound) // nothing to log in to
			return
		}
		data, _ := webFS.ReadFile(pages[p][0])
		rw.Header().Set("Content-Type", pages[p][1])
		http.ServeContent(rw, r, "", time.Time{}, bytes.NewReader(data))
	case p == "/_ir/api/session" && get:
		w.apiSession(rw, r)
	case strings.HasPrefix(p, "/_ir/api/") && r.Method == http.MethodPost && w.public:
		w.serveAPI(rw, r)
	default:
		writeJSON(rw, http.StatusNotFound, apiError{"not_found"})
	}
}

func (w *Web) serveAPI(rw http.ResponseWriter, r *http.Request) {
	if !w.originOK(r) {
		writeJSON(rw, http.StatusForbidden, apiError{"bad_origin"})
		return
	}
	if r.URL.Path == "/_ir/api/logout" {
		w.apiLogout(rw, r)
		return
	}
	key := gate.ClientKey(r, w.ipHeader)
	if d := w.lim.Wait(key, w.now()); d > 0 {
		tooMany(rw, d)
		return
	}
	r.Body = http.MaxBytesReader(rw, r.Body, maxBody)
	switch r.URL.Path {
	case "/_ir/api/join/check":
		w.apiJoinCheck(rw, r, key)
	case "/_ir/api/join/options":
		w.apiJoinOptions(rw, r, key)
	case "/_ir/api/join/verify":
		w.apiJoinVerify(rw, r, key)
	case "/_ir/api/login/options":
		w.apiLoginOptions(rw, r)
	case "/_ir/api/login/verify":
		w.apiLoginVerify(rw, r, key)
	default:
		writeJSON(rw, http.StatusNotFound, apiError{"not_found"})
	}
}

func decodeJSON(rw http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(rw, http.StatusBadRequest, apiError{"bad_request"})
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
func (w *Web) failed(rw http.ResponseWriter, key, what string, status int, code string, err error) {
	w.lim.Fail(key, w.now())
	args := []any{"client", key}
	var pe *protocol.Error
	if errors.As(err, &pe) {
		args = append(args, "reason", pe.Details) // never DevInfo, which echoes request data
	} else if err != nil {
		args = append(args, "reason", err.Error())
	}
	w.log.Info("webapp: "+what+" failed", args...)
	writeJSON(rw, status, apiError{code})
}

type sessionReply struct {
	Authenticated bool   `json:"authenticated"`
	Name          string `json:"name,omitempty"`
	Local         bool   `json:"local,omitempty"` // local-only mode: no login
	Setup         bool   `json:"setup,omitempty"` // no panel has a passkey yet
}

func (w *Web) apiSession(rw http.ResponseWriter, r *http.Request) {
	if !w.public {
		writeJSON(rw, http.StatusOK, sessionReply{Authenticated: true, Name: gate.HostName, Local: true})
		return
	}
	sess, ok := w.identify(r, w.now())
	reply := sessionReply{Authenticated: ok}
	if ok {
		info, _ := w.core.Endpoint(sess.Login)
		reply.Name = info.Name
	}
	w.mu.Lock()
	reply.Setup = !w.hasPasskeyLocked()
	w.mu.Unlock()
	writeJSON(rw, http.StatusOK, reply)
}

func (w *Web) apiLogout(rw http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil && gate.WellFormedSecret(c.Value) {
		h := gate.HashSecret(c.Value)
		w.mu.Lock()
		if _, ok := w.st.sessions[h]; ok {
			w.commitLocked(Change{DeleteSessions: []string{h}})
		}
		w.mu.Unlock()
		w.cancel(func(f *inFlight) bool { return f.session == h })
	}
	http.SetCookie(rw, clearedCookie())
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

// inviteLocked finds the live invite for token.
func (w *Web) inviteLocked(token string, now time.Time) (Invite, bool) {
	if !gate.WellFormedSecret(token) {
		return Invite{}, false
	}
	h := gate.HashSecret(token)
	for _, inv := range w.st.invites {
		if gate.SameHash(inv.Hash, h) && (inv.Setup || now.Before(inv.ExpiresAt)) {
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
func (w *Web) apiJoinCheck(rw http.ResponseWriter, r *http.Request, key string) {
	var req joinRequest
	if !decodeJSON(rw, r, &req) {
		return
	}
	w.mu.Lock()
	inv, ok := w.inviteLocked(req.Token, w.now())
	var reply joinReply
	if ok {
		reply.Name = inv.Name
		if req.Name != nil {
			_, err := w.checkNameLocked(*req.Name, inv.ID)
			reply.NameError = gate.NameCode(err)
		}
	}
	w.mu.Unlock()
	if !ok {
		w.failed(rw, key, "invite", http.StatusForbidden, "invalid_invite", nil)
		return
	}
	writeJSON(rw, http.StatusOK, reply)
}

type ceremony struct {
	Flow    string `json:"flow"`
	Options any    `json:"options"` // publicKey options for navigator.credentials
}

func (w *Web) apiJoinOptions(rw http.ResponseWriter, r *http.Request, key string) {
	var req joinRequest
	if !decodeJSON(rw, r, &req) {
		return
	}
	now := w.now()
	w.mu.Lock()
	inv, ok := w.inviteLocked(req.Token, now)
	var name string
	var err error
	if ok {
		raw := inv.Name
		if req.Name != nil {
			raw = *req.Name
		}
		name, err = w.checkNameLocked(raw, inv.ID)
	}
	w.mu.Unlock()
	if !ok {
		w.failed(rw, key, "invite", http.StatusForbidden, "invalid_invite", nil)
		return
	}
	if err != nil {
		writeJSON(rw, http.StatusConflict, joinReply{NameError: gate.NameCode(err)})
		return
	}
	// Each registration gets its own random user handle and no excludeCredentials. A
	// synced provider (iCloud Keychain, Google Password Manager) shared by two devices may
	// know a passkey it cannot use on this one: excluding it would block the new device,
	// and reusing the handle would make the provider overwrite the existing passkey.
	handle := gate.RandomBytes(32)
	creation, session, err := w.wa.BeginRegistration(waUser{name: name, cred: Cred{UserHandle: handle}},
		webauthn.WithCredentialParameters(webauthn.CredentialParametersRecommendedL3()))
	if err != nil {
		w.log.Error("webapp: registration options", "err", err)
		writeJSON(rw, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	id := w.flows.put(&flow{kind: flowJoin, session: session, inviteID: inv.ID, name: name, handle: handle}, now)
	writeJSON(rw, http.StatusOK, ceremony{Flow: id, Options: creation.Response})
}

type verifyRequest struct {
	Flow     string          `json:"flow"`
	Response json.RawMessage `json:"response"`
}

func (w *Web) apiJoinVerify(rw http.ResponseWriter, r *http.Request, key string) {
	var req verifyRequest
	if !decodeJSON(rw, r, &req) {
		return
	}
	now := w.now()
	f := w.flows.take(req.Flow, flowJoin, now)
	if f == nil {
		w.failed(rw, key, "join", http.StatusForbidden, "flow_expired", errors.New("unknown or expired flow"))
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Response)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = w.wa.CreateCredential(waUser{name: f.name, cred: Cred{UserHandle: f.handle}}, *f.session, parsed)
	}
	if err != nil {
		w.failed(rw, key, "join", http.StatusBadRequest, "verification_failed", err)
		return
	}

	// The ceremony took time: check the invite and the name again.
	w.mu.Lock()
	inv, ok := w.st.invites[f.inviteID]
	if !ok || (!inv.Setup && !now.Before(inv.ExpiresAt)) {
		w.mu.Unlock()
		writeJSON(rw, http.StatusForbidden, apiError{"invalid_invite"})
		return
	}
	name, err := w.checkNameLocked(f.name, inv.ID)
	if err != nil {
		w.mu.Unlock()
		writeJSON(rw, http.StatusConflict, joinReply{NameError: gate.NameCode(err)})
		return
	}
	for _, c := range w.st.creds {
		for _, pk := range c.Passkeys {
			if bytes.Equal(pk.Credential.ID, cred.ID) {
				w.mu.Unlock()
				writeJSON(rw, http.StatusConflict, apiError{"already_registered"})
				return
			}
		}
	}
	first := !w.core.HasWebLogin()
	info, err := w.core.CreateWebLogin(name)
	if err != nil {
		w.mu.Unlock()
		if gate.NameCode(err) != "" {
			writeJSON(rw, http.StatusConflict, joinReply{NameError: gate.NameCode(err)})
		} else {
			writeJSON(rw, http.StatusInternalServerError, apiError{"internal"})
		}
		return
	}
	c := Cred{Login: info.ID, UserHandle: f.handle, Passkeys: []Passkey{{Credential: *cred, CreatedAt: now, LastUsedAt: now}}}
	token, s := w.newSessionLocked(info.ID, now)
	// With a login there, the first-run link has done its part, whichever invite was used.
	del := append(w.setupInvitesLocked(), inv.ID)
	err = w.commitLocked(Change{DeleteInvites: del, Creds: []Cred{c}, Sessions: []Session{s}})
	w.mu.Unlock()
	if err != nil {
		w.core.Revoke(info.ID)
		writeJSON(rw, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	w.removeSetupFile()
	w.lim.Success(key)
	w.log.Info("webapp: login joined", "endpoint", info.ID, "name", info.Name, "client", key, "setup", inv.Setup, "perms", len(inv.Perms) > 0, "first", first)
	if w.cfg.OnJoin != nil {
		w.cfg.OnJoin(Joined{Endpoint: info, Perms: inv.Perms, First: first})
	}
	http.SetCookie(rw, sessionCookie(token))
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "name": info.Name})
}

func (w *Web) apiLoginOptions(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	has := w.hasPasskeyLocked()
	w.mu.Unlock()
	if !has {
		writeJSON(rw, http.StatusConflict, apiError{"no_passkey"})
		return
	}
	// Discoverable login: no credential IDs go to anonymous visitors; the browser offers
	// the passkeys it holds for this site.
	assertion, session, err := w.wa.BeginDiscoverableLogin()
	if err != nil {
		w.log.Error("webapp: login options", "err", err)
		writeJSON(rw, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	id := w.flows.put(&flow{kind: flowLogin, session: session}, w.now())
	writeJSON(rw, http.StatusOK, ceremony{Flow: id, Options: assertion.Response})
}

var (
	errUnknownCredential = errors.New("unknown credential")
	errCounter           = errors.New("signature counter did not increase")
)

func (w *Web) apiLoginVerify(rw http.ResponseWriter, r *http.Request, key string) {
	var req verifyRequest
	if !decodeJSON(rw, r, &req) {
		return
	}
	now := w.now()
	f := w.flows.take(req.Flow, flowLogin, now)
	if f == nil {
		w.failed(rw, key, "login", http.StatusForbidden, "flow_expired", errors.New("unknown or expired flow"))
		return
	}
	var login string
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, c := range w.st.creds {
			if !bytes.Equal(c.UserHandle, userHandle) {
				continue
			}
			info, live := w.core.Endpoint(c.Login)
			if !live || info.Kind != gate.KindPanel {
				continue
			}
			for _, pk := range c.Passkeys {
				if bytes.Equal(pk.Credential.ID, rawID) {
					login = c.Login
					return waUser{name: info.Name, cred: c}, nil
				}
			}
		}
		return nil, errUnknownCredential
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Response)
	var cred *webauthn.Credential
	if err == nil {
		_, cred, err = w.wa.ValidatePasskeyLogin(handler, *f.session, parsed)
	}
	if err == nil && cred.Authenticator.CloneWarning {
		err = errCounter // a copy of the key is in use somewhere
	}
	if err != nil {
		w.failed(rw, key, "login", http.StatusForbidden, "auth_failed", err)
		return
	}

	w.mu.Lock()
	c, ok := w.st.creds[login]
	info, live := w.core.Endpoint(login)
	i := -1
	if ok && live {
		i = slices.IndexFunc(c.Passkeys, func(pk Passkey) bool { return bytes.Equal(pk.Credential.ID, cred.ID) })
	}
	if i < 0 { // revoked during the ceremony
		w.mu.Unlock()
		writeJSON(rw, http.StatusForbidden, apiError{"auth_failed"})
		return
	}
	c.Passkeys = slices.Clone(c.Passkeys)
	c.Passkeys[i].Credential = *cred
	c.Passkeys[i].LastUsedAt = now
	token, s := w.newSessionLocked(login, now)
	err = w.commitLocked(Change{Creds: []Cred{c}, Sessions: []Session{s}})
	w.mu.Unlock()
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, apiError{"internal"})
		return
	}
	w.core.Seen(login)
	w.lim.Success(key)
	w.log.Info("webapp: login logged in", "endpoint", login, "name", info.Name, "client", key)
	http.SetCookie(rw, sessionCookie(token))
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "name": info.Name})
}

// waUser presents a login to the WebAuthn library.
type waUser struct {
	name string
	cred Cred
}

func (u waUser) WebAuthnID() []byte          { return u.cred.UserHandle }
func (u waUser) WebAuthnName() string        { return u.name }
func (u waUser) WebAuthnDisplayName() string { return u.name }
func (u waUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, len(u.cred.Passkeys))
	for i, pk := range u.cred.Passkeys {
		out[i] = pk.Credential
	}
	return out
}
