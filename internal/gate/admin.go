package gate

import (
	"errors"
	"time"
)

// Admin errors.
var (
	ErrNotFound    = errors.New("gate: no such endpoint or invite")
	ErrLastPasskey = errors.New("gate: cannot revoke the last web panel with a passkey")
	ErrNotPublic   = errors.New("gate: no public origin configured")
	ErrLocal       = errors.New("gate: 本机 is built in and has no profile")
)

// EndpointInfo is an endpoint as listed: no secrets, no avatar bytes.
type EndpointInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`             // KindPanel or KindChannel
	Avatar     string    `json:"avatar,omitempty"` // URL with a version, for <img src>
	Passkeys   int       `json:"passkeys,omitempty"`
	Sessions   int       `json:"sessions,omitempty"` // logged-in browsers
	Online     int       `json:"online,omitempty"`   // tracked live connections
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt,omitzero"`
}

// InviteInfo is a pending invite as listed.
type InviteInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Setup     bool      `json:"setup,omitempty"`
	Owner     bool      `json:"owner,omitempty"`
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
	info := EndpointInfo{ID: e.ID, Name: e.Name, Kind: e.Kind, Passkeys: len(e.Passkeys),
		CreatedAt: e.CreatedAt, LastSeenAt: e.LastSeenAt}
	if e.AvatarVersion != "" {
		info.Avatar = "/_ir/avatar/" + e.ID + "?v=" + e.AvatarVersion
	}
	return info
}

// CreateInvite makes a one-time link (valid InviteTTL) for a new web panel: `ir invite
// [名字]`. A name, if given, is reserved for the invite and offered to the joiner, who may
// change it. owner (ir invite --owner): the panel that joins with it is the owner's own; Joined
// says so.
func (g *Gate) CreateInvite(name string, owner bool) (InviteLink, error) {
	if !g.public {
		return InviteLink{}, ErrNotPublic
	}
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if name != "" {
		n, err := g.checkNameLocked(name, "", "")
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
	inv := Invite{ID: id, Hash: hashToken(token), Name: name, Owner: owner, CreatedAt: now, ExpiresAt: now.Add(InviteTTL)}
	if err := g.commitLocked(Change{Invites: []Invite{inv}}); err != nil {
		return InviteLink{}, err
	}
	g.log.Info("gate: invite created", "invite", inv.ID, "name", name, "owner", owner)
	return InviteLink{ID: inv.ID, Name: name, URL: g.joinURL(token), ExpiresAt: inv.ExpiresAt}, nil
}

// CreateToken returns the bearer token of the channel endpoint (plugin, peer) named name,
// creating it if needed: `ir token <名字>`. For an existing channel the token is replaced:
// the old one stops working and its connections are closed. Works in both modes.
func (g *Gate) CreateToken(name string) (EndpointInfo, string, error) {
	n, err := NormalizeName(name)
	if err != nil {
		return EndpointInfo{}, "", err
	}
	token := newChannelToken()
	g.mu.Lock()
	e, found := g.findLocked(n)
	if found && e.Kind != KindChannel {
		g.mu.Unlock()
		return EndpointInfo{}, "", ErrNameTaken
	}
	if !found {
		if n, err = g.checkNameLocked(n, "", ""); err != nil {
			g.mu.Unlock()
			return EndpointInfo{}, "", err
		}
		e = Endpoint{ID: g.newEndpointIDLocked(), Name: n, Kind: KindChannel, CreatedAt: g.now()}
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
	g.log.Info("gate: channel token issued", "endpoint", e.ID, "name", e.Name, "replaced", found)
	return infoOf(e), token, nil
}

// Access lists all endpoints and pending invites: `ir access`.
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
			out.Invites = append(out.Invites, InviteInfo{ID: inv.ID, Name: inv.Name, Setup: inv.Setup, Owner: inv.Owner,
				CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt})
		}
	}
	return out
}

// Endpoint returns one endpoint by ID, including the built-in 本机.
func (g *Gate) Endpoint(id string) (EndpointInfo, bool) {
	if id == LocalEndpointID {
		return EndpointInfo{ID: LocalEndpointID, Name: LocalName, Kind: KindChannel}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.st.endpoints[id]
	return infoOf(e), ok
}

// Find returns one endpoint by ID or name, including the built-in 本机.
func (g *Gate) Find(ref string) (EndpointInfo, bool) {
	if ref == LocalEndpointID || nameKey(ref) == nameKey(LocalName) {
		return g.Endpoint(LocalEndpointID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.findLocked(ref)
	return infoOf(e), ok
}

// CheckName returns the stored form of name if a new endpoint could take it.
func (g *Gate) CheckName(name string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkNameLocked(name, "", "")
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
		if ref == LocalEndpointID || nameKey(ref) == nameKey(LocalName) {
			return EndpointInfo{}, ErrLocal
		}
		return EndpointInfo{}, ErrNotFound
	}
	if p.Name != nil {
		n, err := g.checkNameLocked(*p.Name, e.ID, "")
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

// Revoke removes the endpoint ref (ID or name), ending its sessions and closing its
// tracked connections, or else the pending invite ref (ID or reserved name): `ir revoke`.
// The last web panel with a passkey cannot be revoked; with every passkey lost, run
// `ir invite` on the machine instead.
func (g *Gate) Revoke(ref string) error {
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
	if n, err := NormalizeName(ref); err == nil {
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

// findLocked resolves an endpoint by ID or by name. IDs are longer than any name.
func (g *Gate) findLocked(ref string) (Endpoint, bool) {
	if e, ok := g.st.endpoints[ref]; ok {
		return e, true
	}
	n, err := NormalizeName(ref)
	if err != nil {
		return Endpoint{}, false
	}
	k := nameKey(n)
	for _, e := range g.st.endpoints {
		if nameKey(e.Name) == k {
			return e, true
		}
	}
	return Endpoint{}, false
}

// checkNameLocked normalizes raw and checks that no endpoint (but exceptEndpoint) and
// no live invite (but exceptInvite) holds it.
func (g *Gate) checkNameLocked(raw, exceptEndpoint, exceptInvite string) (string, error) {
	n, err := NormalizeName(raw)
	if err != nil {
		return "", err
	}
	k := nameKey(n)
	for _, e := range g.st.endpoints {
		if e.ID != exceptEndpoint && nameKey(e.Name) == k {
			return "", ErrNameTaken
		}
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
