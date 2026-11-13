package gate

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// Admin errors.
var (
	ErrNotFound = errors.New("gate: no such app or login")
	ErrLocal    = errors.New("gate: the host is built in and has no profile")
	ErrBuiltIn  = errors.New("gate: ir_web is built in")
)

// EndpointInfo is an app or a login of ir_web as listed: no secrets, no avatar bytes.
type EndpointInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`             // KindPanel (a login of ir_web) or KindChannel (an app)
	Type       string    `json:"type,omitempty"`   // an app's type, display only
	Avatar     string    `json:"avatar,omitempty"` // URL with a version, for <img src>
	Online     int       `json:"online,omitempty"` // tracked live connections
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt,omitzero"`
	ApprovedAt time.Time `json:"approvedAt,omitzero"`
}

// Host: the host's own (HostID).
func (e EndpointInfo) Host() bool { return e.ID == HostID }

// Identity is the identity of a request from it.
func (e EndpointInfo) Identity() Identity {
	switch {
	case e.ID == HostID:
		return HostIdentity
	case e.Kind == KindPanel:
		return Identity{App: WebApp, Login: e.ID, Name: e.Name}
	}
	return Identity{App: e.ID, Name: e.Name}
}

// Access is what `ir apps` shows of the core: the apps and ir_web's logins.
type Access struct {
	Endpoints []EndpointInfo `json:"endpoints"`
}

// Profile is an update to an endpoint's name and avatar. Zero fields change nothing.
type Profile struct {
	Name         *string // new name; same rules and uniqueness as at creation
	Avatar       []byte  // PNG, JPEG or WebP up to MaxAvatarBytes; stored as AvatarSize²
	RemoveAvatar bool
}

func infoOf(e Endpoint) EndpointInfo {
	info := EndpointInfo{ID: e.ID, Name: e.Name, Kind: e.Kind, Type: e.Type,
		CreatedAt: e.CreatedAt, LastSeenAt: e.LastSeenAt, ApprovedAt: e.ApprovedAt}
	if e.AvatarVersion != "" {
		info.Avatar = "/_ir/avatar/" + e.ID + "?v=" + e.AvatarVersion
	}
	return info
}

// CreateWebLogin registers a new login of ir_web named name (unique among them), approved now: a
// browser joined through an invite (package webapp keeps its passkey).
func (g *Gate) CreateWebLogin(name string) (EndpointInfo, error) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	n, err := g.checkNameLocked(name, KindPanel, "")
	if err != nil {
		return EndpointInfo{}, err
	}
	e := Endpoint{ID: g.newEndpointIDLocked(), Name: n, Kind: KindPanel, CreatedAt: now, LastSeenAt: now, ApprovedAt: now}
	if err := g.commitLocked(Change{Endpoints: []Endpoint{e}}); err != nil {
		return EndpointInfo{}, err
	}
	g.log.Info("gate: login of ir_web added", "endpoint", e.ID, "name", e.Name)
	return infoOf(e), nil
}

// HasWebLogin: ir_web has a login.
func (g *Gate) HasWebLogin() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hasWebLoginLocked()
}

// Seen is login id of ir_web, which just spoke (its last-seen time kept, once a minute).
func (g *Gate) Seen(id string) (EndpointInfo, bool) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.st.endpoints[id]
	if !ok || e.Kind != KindPanel {
		return EndpointInfo{}, false
	}
	if now.Sub(e.LastSeenAt) >= touchEvery {
		e.LastSeenAt = now
		g.commitLocked(Change{Endpoints: []Endpoint{e}})
	}
	return infoOf(e), true
}

// SetAppToken makes token the TOKEN of the built-in app id (ir_web), adding its row if missing.
// It is never listed, found by name, renamed or removed.
func (g *Gate) SetAppToken(id, token string) error {
	if !wellFormedChannel(token) {
		return errors.New("gate: not a TOKEN")
	}
	h := hashToken(token)
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.st.endpoints[id]
	if ok && e.Kind == KindChannel && hashEqual(e.TokenHash, h) {
		return nil
	}
	if !ok {
		e = Endpoint{ID: id, Name: id, Kind: KindChannel, CreatedAt: g.now()}
	}
	e.TokenHash = h
	return g.commitLocked(Change{Endpoints: []Endpoint{e}})
}

// CreateToken returns the TOKEN of the app named name, creating it if needed (a pairing allowed).
// For an existing app the TOKEN is replaced: the old one stops working and its connections are
// closed. Works in both modes.
func (g *Gate) CreateToken(name string) (EndpointInfo, string, error) {
	return g.createToken(name, "", true)
}

// CreateNewToken is CreateToken for a new app only: ErrNameTaken if an app has name.
func (g *Gate) CreateNewToken(name string) (EndpointInfo, string, error) {
	return g.createToken(name, "", false)
}

// CreateApp makes a new app by name, shown as typ (display only), and gives its TOKEN: a pairing
// allowed. ErrNameTaken if an app has name.
func (g *Gate) CreateApp(name, typ string) (EndpointInfo, string, error) {
	return g.createToken(name, typ, false)
}

// RenewToken gives app id a new TOKEN (an update pairing allowed): the old one stops working and
// its connections are closed; its name, usage and permissions stay.
func (g *Gate) RenewToken(id string) (EndpointInfo, string, error) {
	token := newChannelToken()
	g.mu.Lock()
	e, ok := g.st.endpoints[id]
	if !ok || e.Kind != KindChannel {
		g.mu.Unlock()
		return EndpointInfo{}, "", ErrNotFound
	}
	e.TokenHash = hashToken(token)
	err := g.commitLocked(Change{Endpoints: []Endpoint{e}})
	g.mu.Unlock()
	if err != nil {
		return EndpointInfo{}, "", err
	}
	g.closeConns(func(c *conn) bool { return c.endpoint == e.ID })
	g.log.Info("gate: app TOKEN renewed", "endpoint", e.ID, "name", e.Name)
	return infoOf(e), token, nil
}

func (g *Gate) createToken(name, typ string, replace bool) (EndpointInfo, string, error) {
	n, err := NormalizeName(name)
	if err != nil {
		return EndpointInfo{}, "", err
	}
	token := newChannelToken()
	g.mu.Lock()
	e, found := g.byNameLocked(n, KindChannel)
	if found && !replace {
		g.mu.Unlock()
		return EndpointInfo{}, "", ErrNameTaken
	}
	if !found {
		if n, err = g.checkNameLocked(n, KindChannel, ""); err != nil {
			g.mu.Unlock()
			return EndpointInfo{}, "", err
		}
		e = Endpoint{ID: g.newEndpointIDLocked(), Name: n, Kind: KindChannel, Type: typ, CreatedAt: g.now()}
	}
	e.TokenHash = hashToken(token)
	err = g.commitLocked(Change{Endpoints: []Endpoint{e}})
	g.mu.Unlock()
	if err != nil {
		return EndpointInfo{}, "", err
	}
	if found {
		g.closeConns(func(c *conn) bool { return c.endpoint == e.ID })
	}
	g.log.Info("gate: app TOKEN issued", "endpoint", e.ID, "name", e.Name, "replaced", found)
	return infoOf(e), token, nil
}

// Access lists every app and every login of ir_web: `ir apps`. (Not ir_web's own row.)
func (g *Gate) Access() Access {
	online := map[string]int{}
	g.connMu.Lock()
	for c := range g.conns {
		online[c.endpoint]++
	}
	g.connMu.Unlock()

	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.st.state()
	out := Access{Endpoints: []EndpointInfo{}}
	for _, e := range st.Endpoints {
		if e.ID == WebApp {
			continue
		}
		info := infoOf(e)
		info.Online = online[e.ID]
		out.Endpoints = append(out.Endpoints, info)
	}
	return out
}

// Endpoint returns one app or login of ir_web by ID, including the host.
func (g *Gate) Endpoint(id string) (EndpointInfo, bool) {
	if id == HostID {
		return EndpointInfo{ID: HostID, Name: HostName, Kind: KindChannel}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.st.endpoints[id]
	return infoOf(e), ok
}

// ByToken returns the app whose TOKEN tok is, whatever it has been renamed to since.
// ByTokenFrom is ByToken for a TOKEN that came in a request's body (Satori's IDENTIFY), counted
// against r's client as a bearer token is: a good one always passes, a client that fails too
// often waits.
func (g *Gate) ByTokenFrom(r *http.Request, tok string) (EndpointInfo, bool) {
	if info, ok := g.ByToken(tok); ok {
		return info, true
	}
	now := g.now()
	key := ClientKey(r, g.ipHeader)
	if g.lim.Wait(key, now) > 0 {
		return EndpointInfo{}, false
	}
	g.lim.Fail(key, now)
	g.log.Info("gate: bad token in a stream", "client", key)
	return EndpointInfo{}, false
}

func (g *Gate) ByToken(tok string) (EndpointInfo, bool) {
	tok = strings.TrimSpace(tok)
	if !wellFormedChannel(tok) {
		return EndpointInfo{}, false
	}
	h := hashToken(tok)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.st.endpoints {
		if e.Kind == KindChannel && e.ID != WebApp && e.TokenHash != "" && hashEqual(e.TokenHash, h) {
			return infoOf(e), true
		}
	}
	return EndpointInfo{}, false
}

// TokenHash is the hash of endpoint id's TOKEN ("" for none), to know its holder by after it is
// revoked (SameToken).
func (g *Gate) TokenHash(id string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.st.endpoints[id].TokenHash
}

// SameToken: tok is the TOKEN whose hash TokenHash gave.
func SameToken(hash, tok string) bool {
	return hash != "" && hashEqual(hash, hashToken(strings.TrimSpace(tok)))
}

// IsHostRef: ref names the host (its ID, or one of its reserved names).
func IsHostRef(ref string) bool {
	k := nameKey(strings.TrimSpace(ref))
	for _, n := range []string{HostID, HostName, "host", "本机"} {
		if k == nameKey(n) {
			return true
		}
	}
	return false
}

// Find returns one app or login of ir_web by ID or name, including the host: an app by its
// name, a login as "ir_web/名字" (or by its name alone when no app has it).
func (g *Gate) Find(ref string) (EndpointInfo, bool) {
	if IsHostRef(ref) {
		return g.Endpoint(HostID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.findLocked(ref)
	return infoOf(e), ok
}

// CheckName returns the stored form of name if a new app could take it.
func (g *Gate) CheckName(name string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkNameLocked(name, KindChannel, "")
}

// CheckLoginName returns the stored form of name if no login of ir_web has it (an invite's names
// are package webapp's to check).
func (g *Gate) CheckLoginName(name string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkNameLocked(name, KindPanel, "")
}

// UpdateProfile changes the name and/or avatar of the endpoint ref (ID or name):
// `ir profile`, and ServeProfile for the web and plugins.
func (g *Gate) UpdateProfile(ref string, p Profile) (EndpointInfo, error) {
	var avatar []byte
	var version string
	if p.Avatar != nil {
		var err error
		if avatar, version, err = makeAvatar(p.Avatar); err != nil {
			return EndpointInfo{}, err
		}
	}
	// A name an invite of ir_web holds, asked before the lock: ir_web calls in here holding its own.
	held := false
	if n, err := NormalizeName(deref(p.Name)); p.Name != nil && err == nil && g.cfg.LoginNameTaken != nil {
		held = g.cfg.LoginNameTaken(n)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.findLocked(ref)
	if !ok {
		if IsHostRef(ref) {
			return EndpointInfo{}, ErrLocal
		}
		return EndpointInfo{}, ErrNotFound
	}
	if p.Name != nil {
		n, err := g.checkNameLocked(*p.Name, e.Kind, e.ID)
		if err != nil {
			return EndpointInfo{}, err
		}
		if held && e.Kind == KindPanel {
			return EndpointInfo{}, ErrNameTaken
		}
		e.Name = n
	}
	if p.RemoveAvatar {
		e.Avatar, e.AvatarVersion = nil, ""
	}
	if avatar != nil {
		e.Avatar, e.AvatarVersion = avatar, version
	}
	if err := g.commitLocked(Change{Endpoints: []Endpoint{e}}); err != nil {
		return EndpointInfo{}, err
	}
	g.log.Info("gate: profile updated", "endpoint", e.ID, "name", e.Name, "avatar", e.AvatarVersion != "")
	return infoOf(e), nil
}

// Revoke removes the app or login of ir_web ref (Find's), closing its tracked connections, then
// tells Config.OnRevoke (ir_web ends a login's web sessions there): `ir apps rm`.
func (g *Gate) Revoke(ref string) error {
	if IsHostRef(ref) {
		return ErrLocal
	}
	if ref == WebApp {
		return ErrBuiltIn
	}
	g.mu.Lock()
	e, ok := g.findLocked(ref)
	if !ok {
		g.mu.Unlock()
		return ErrNotFound
	}
	err := g.commitLocked(Change{DeleteEndpoints: []string{e.ID}})
	g.mu.Unlock()
	if err != nil {
		return err
	}
	g.closeConns(func(c *conn) bool { return c.endpoint == e.ID })
	g.log.Info("gate: endpoint revoked", "endpoint", e.ID, "name", e.Name)
	if g.cfg.OnRevoke != nil {
		g.cfg.OnRevoke(e.ID)
	}
	return nil
}

// findLocked resolves an app or a login of ir_web by ID or by name (Find's). IDs are longer than
// any name.
func (g *Gate) findLocked(ref string) (Endpoint, bool) {
	if ref == WebApp {
		return Endpoint{}, false
	}
	if e, ok := g.st.endpoints[ref]; ok {
		return e, true
	}
	if login, ok := strings.CutPrefix(ref, WebApp+"/"); ok {
		if e, ok := g.st.endpoints[login]; ok && e.Kind == KindPanel {
			return e, true
		}
		return g.byNameLocked(login, KindPanel)
	}
	if e, ok := g.byNameLocked(ref, KindChannel); ok {
		return e, true
	}
	return g.byNameLocked(ref, KindPanel)
}

// byNameLocked finds the app (KindChannel) or login of ir_web (KindPanel) by name.
func (g *Gate) byNameLocked(name, kind string) (Endpoint, bool) {
	n, err := NormalizeName(name)
	if err != nil {
		return Endpoint{}, false
	}
	k := nameKey(n)
	for _, e := range g.st.endpoints {
		if e.Kind == kind && nameKey(e.Name) == k {
			return e, true
		}
	}
	return Endpoint{}, false
}

// checkNameLocked normalizes raw and checks that no other of its kind (but exceptEndpoint) holds
// it: names are unique among apps, and among ir_web's logins.
func (g *Gate) checkNameLocked(raw, kind, exceptEndpoint string) (string, error) {
	n, err := NormalizeName(raw)
	if err != nil {
		return "", err
	}
	k := nameKey(n)
	for _, e := range g.st.endpoints {
		if e.Kind == kind && e.ID != exceptEndpoint && nameKey(e.Name) == k {
			return "", ErrNameTaken
		}
	}
	return n, nil
}

// NameKey is the form names are compared in (case and width folded).
func NameKey(name string) string { return nameKey(name) }

func (g *Gate) newEndpointIDLocked() string {
	for {
		id := newID(10) // 16 characters: longer than any name
		if _, taken := g.st.endpoints[id]; !taken {
			return id
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
