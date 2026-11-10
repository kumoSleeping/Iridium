package gate

import (
	"errors"
	"strings"
	"time"
)

// Admin errors.
var (
	ErrNotFound    = errors.New("gate: no such app, login or invite")
	ErrLastPasskey = errors.New("gate: cannot remove the last login of ir_web with a passkey")
	ErrNotPublic   = errors.New("gate: no public origin configured")
	ErrLocal       = errors.New("gate: the host is built in and has no profile")
)

// EndpointInfo is an app or a login of ir_web as listed: no secrets, no avatar bytes.
type EndpointInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`             // KindPanel (a login of ir_web) or KindChannel (an app)
	Type       string    `json:"type,omitempty"`   // an app's type, display only
	Avatar     string    `json:"avatar,omitempty"` // URL with a version, for <img src>
	Passkeys   int       `json:"passkeys,omitempty"`
	Sessions   int       `json:"sessions,omitempty"` // logged-in browsers
	Online     int       `json:"online,omitempty"`   // tracked live connections
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

// InviteInfo is a pending invite as listed.
type InviteInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Setup     bool      `json:"setup,omitempty"`
	Perms     []byte    `json:"perms,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

// Access is what `ir access` shows.
type Access struct {
	Endpoints []EndpointInfo `json:"endpoints"`
	Invites   []InviteInfo   `json:"invites"`
}

// InviteLink is a new panel invite. URL holds the secret: show it to the operator only,
// never log it or let it reach an agent's transcript.
type InviteLink struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Profile is an update to an endpoint's name and avatar. Zero fields change nothing.
type Profile struct {
	Name         *string // new name; same rules and uniqueness as at creation
	Avatar       []byte  // PNG, JPEG or WebP up to MaxAvatarBytes; stored as AvatarSize²
	RemoveAvatar bool
}

func infoOf(e Endpoint) EndpointInfo {
	info := EndpointInfo{ID: e.ID, Name: e.Name, Kind: e.Kind, Type: e.Type, Passkeys: len(e.Passkeys),
		CreatedAt: e.CreatedAt, LastSeenAt: e.LastSeenAt, ApprovedAt: e.ApprovedAt}
	if e.AvatarVersion != "" {
		info.Avatar = "/_ir/avatar/" + e.ID + "?v=" + e.AvatarVersion
	}
	return info
}

// CreateInvite makes a slot for a new login of ir_web, approved beforehand: a one-time link
// (valid InviteTTL), `ir invite [名字]`. A name, if given, is reserved for the invite and offered
// to the joiner, who may change it. perms: what the login may do (the server's JSON), handed back
// in Joined.
func (g *Gate) CreateInvite(name string, perms []byte) (InviteLink, error) {
	if !g.public {
		return InviteLink{}, ErrNotPublic
	}
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if name != "" {
		n, err := g.checkNameLocked(name, KindPanel, "", "")
		if err != nil {
			return InviteLink{}, err
		}
		name = n
	}
	token := newToken()
	id := newID(5) // 8 characters; never "setup"
	for g.st.invites[id].ID != "" {
		id = newID(5)
	}
	inv := Invite{ID: id, Hash: hashToken(token), Name: name, Perms: perms, CreatedAt: now, ExpiresAt: now.Add(InviteTTL)}
	if err := g.commitLocked(Change{Invites: []Invite{inv}}); err != nil {
		return InviteLink{}, err
	}
	g.log.Info("gate: invite created", "invite", inv.ID, "name", name)
	return InviteLink{ID: inv.ID, Name: name, URL: g.joinURL(token), ExpiresAt: inv.ExpiresAt}, nil
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
		if n, err = g.checkNameLocked(n, KindChannel, "", ""); err != nil {
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

// Access lists every app, every login of ir_web and the pending invites: `ir apps`.
func (g *Gate) Access() Access {
	now := g.now()
	online := map[string]int{}
	g.connMu.Lock()
	for c := range g.conns {
		online[c.endpoint]++
	}
	g.connMu.Unlock()

	g.mu.Lock()
	defer g.mu.Unlock()
	sessions := map[string]int{}
	for _, s := range g.st.sessions {
		if !sessionExpired(s, now) {
			sessions[s.EndpointID]++
		}
	}
	st := g.st.state()
	out := Access{Endpoints: []EndpointInfo{}, Invites: []InviteInfo{}}
	for _, e := range st.Endpoints {
		info := infoOf(e)
		info.Sessions, info.Online = sessions[e.ID], online[e.ID]
		out.Endpoints = append(out.Endpoints, info)
	}
	panel := g.hasPanelLocked() // then a setup link is no use, and not listed
	for _, inv := range st.Invites {
		if (inv.Setup && !panel) || (!inv.Setup && now.Before(inv.ExpiresAt)) {
			out.Invites = append(out.Invites, InviteInfo{ID: inv.ID, Name: inv.Name, Setup: inv.Setup, Perms: inv.Perms,
				CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt})
		}
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
func (g *Gate) ByToken(tok string) (EndpointInfo, bool) {
	tok = strings.TrimSpace(tok)
	if !wellFormedChannel(tok) {
		return EndpointInfo{}, false
	}
	h := hashToken(tok)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.st.endpoints {
		if e.Kind == KindChannel && e.TokenHash != "" && hashEqual(e.TokenHash, h) {
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
	return g.checkNameLocked(name, KindChannel, "", "")
}

// CheckLoginName returns the stored form of name if a new login of ir_web could take it.
func (g *Gate) CheckLoginName(name string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkNameLocked(name, KindPanel, "", "")
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
		n, err := g.checkNameLocked(*p.Name, e.Kind, e.ID, "")
		if err != nil {
			return EndpointInfo{}, err
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

// Revoke removes the app or login of ir_web ref (Find's), ending its sessions and closing its
// tracked connections, or else the pending invite ref (ID or reserved name): `ir apps rm`.
// The last login of ir_web with a passkey cannot be removed; with every passkey lost, run
// `ir invite` on the machine instead.
func (g *Gate) Revoke(ref string) error {
	if IsHostRef(ref) {
		return ErrLocal
	}
	g.mu.Lock()
	if e, ok := g.findLocked(ref); ok {
		if e.Kind == KindPanel && len(e.Passkeys) > 0 {
			others := 0
			for _, o := range g.st.endpoints {
				if o.ID != e.ID && o.Kind == KindPanel && len(o.Passkeys) > 0 {
					others++
				}
			}
			if others == 0 {
				g.mu.Unlock()
				return ErrLastPasskey
			}
		}
		ch := Change{DeleteEndpoints: []string{e.ID}}
		for h, s := range g.st.sessions {
			if s.EndpointID == e.ID {
				ch.DeleteSessions = append(ch.DeleteSessions, h)
			}
		}
		err := g.commitLocked(ch)
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
	defer g.mu.Unlock()
	key := ""
	if n, err := NormalizeName(strings.TrimPrefix(ref, WebApp+"/")); err == nil {
		key = nameKey(n)
	}
	for id, inv := range g.st.invites {
		if id == ref || (inv.Name != "" && nameKey(inv.Name) == key) {
			if err := g.commitLocked(Change{DeleteInvites: []string{id}}); err != nil {
				return err
			}
			if inv.Setup {
				g.removeSetupFile()
			}
			g.log.Info("gate: invite revoked", "invite", id)
			return nil
		}
	}
	return ErrNotFound
}

// findLocked resolves an app or a login of ir_web by ID or by name (Find's). IDs are longer than
// any name.
func (g *Gate) findLocked(ref string) (Endpoint, bool) {
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
// it: names are unique among apps, and among ir_web's logins and the live invites (but
// exceptInvite) for them.
func (g *Gate) checkNameLocked(raw, kind, exceptEndpoint, exceptInvite string) (string, error) {
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
	if kind != KindPanel {
		return n, nil
	}
	now := g.now()
	for _, inv := range g.st.invites {
		if inv.ID != exceptInvite && inv.Name != "" && (inv.Setup || now.Before(inv.ExpiresAt)) && nameKey(inv.Name) == k {
			return "", ErrNameTaken
		}
	}
	return n, nil
}

func (g *Gate) newEndpointIDLocked() string {
	for {
		id := newID(10) // 16 characters: longer than any name
		if _, taken := g.st.endpoints[id]; !taken {
			return id
		}
	}
}
