package webapp

import (
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/gate"
)

// InviteInfo is a pending invite as listed.
type InviteInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Setup     bool      `json:"setup,omitempty"`
	Perms     []byte    `json:"perms,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

// InviteLink is a new invite. URL holds the secret: show it to the operator only, never log it or
// let it reach an agent's transcript.
type InviteLink struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// checkNameLocked normalizes raw and checks that no login of ir_web (the core's) nor a live invite
// but exceptInvite holds it.
func (w *Web) checkNameLocked(raw, exceptInvite string) (string, error) {
	n, err := w.core.CheckLoginName(raw)
	if err != nil {
		return "", err
	}
	if w.inviteNamedLocked(n, exceptInvite) {
		return "", gate.ErrNameTaken
	}
	return n, nil
}

func (w *Web) inviteNamedLocked(name, exceptInvite string) bool {
	k, now := gate.NameKey(name), w.now()
	for _, inv := range w.st.invites {
		if inv.ID != exceptInvite && inv.Name != "" && (inv.Setup || now.Before(inv.ExpiresAt)) && gate.NameKey(inv.Name) == k {
			return true
		}
	}
	return false
}

// NameTaken: a live invite holds name for its login (gate.Config.LoginNameTaken).
func (w *Web) NameTaken(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inviteNamedLocked(name, "")
}

// CreateInvite makes a slot for a new login of ir_web, approved beforehand: a one-time link
// (valid InviteTTL), `ir invite [名字]`. A name, if given, is reserved for the invite and offered
// to the joiner, who may change it. perms: what the login may do (the server's JSON), handed back
// in Joined.
func (w *Web) CreateInvite(name string, perms []byte) (InviteLink, error) {
	if !w.public {
		return InviteLink{}, ErrNotPublic
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if name != "" {
		n, err := w.checkNameLocked(name, "")
		if err != nil {
			return InviteLink{}, err
		}
		name = n
	}
	token := gate.NewSecret()
	id := gate.NewID(5) // 8 characters; never "setup"
	for w.st.invites[id].ID != "" {
		id = gate.NewID(5)
	}
	inv := Invite{ID: id, Hash: gate.HashSecret(token), Name: name, Perms: perms, CreatedAt: now, ExpiresAt: now.Add(InviteTTL)}
	if err := w.commitLocked(Change{Invites: []Invite{inv}}); err != nil {
		return InviteLink{}, err
	}
	w.log.Info("webapp: invite created", "invite", inv.ID, "name", name)
	return InviteLink{ID: inv.ID, Name: name, URL: w.joinURL(token), ExpiresAt: inv.ExpiresAt}, nil
}

// Invites are the pending invites (the setup link only while ir_web has no login).
func (w *Web) Invites() []InviteInfo {
	now := w.now()
	login := w.core.HasWebLogin()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []InviteInfo{}
	for _, inv := range w.st.state().Invites {
		if (inv.Setup && !login) || (!inv.Setup && now.Before(inv.ExpiresAt)) {
			out = append(out, InviteInfo{ID: inv.ID, Name: inv.Name, Setup: inv.Setup, Perms: inv.Perms,
				CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt})
		}
	}
	return out
}

// RevokeInvite removes the pending invite ref: its ID, or the name it holds (also "ir_web/名字").
func (w *Web) RevokeInvite(ref string) error {
	key := ""
	if n, err := gate.NormalizeName(strings.TrimPrefix(ref, gate.WebApp+"/")); err == nil {
		key = gate.NameKey(n)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, inv := range w.st.invites {
		if id == ref || (inv.Name != "" && gate.NameKey(inv.Name) == key) {
			if err := w.commitLocked(Change{DeleteInvites: []string{id}}); err != nil {
				return err
			}
			if inv.Setup {
				w.removeSetupFile()
			}
			w.log.Info("webapp: invite revoked", "invite", id)
			return nil
		}
	}
	return ErrNotFound
}

// MayRemove refuses removing login, the last of ir_web with a passkey: with every passkey lost,
// run `ir invite` on the machine instead.
func (w *Web) MayRemove(login string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.st.creds[login].Passkeys) == 0 {
		return nil
	}
	for id, c := range w.st.creds {
		if id != login && len(c.Passkeys) > 0 {
			if _, ok := w.core.Endpoint(id); ok {
				return nil
			}
		}
	}
	return ErrLastPasskey
}

// Forget drops what ir_web kept of a login the core removed (its row, passkeys in it, is gone):
// its web sessions end, and their requests with them.
func (w *Web) Forget(login string) {
	w.mu.Lock()
	var ch Change
	for h, s := range w.st.sessions {
		if s.Login == login {
			ch.DeleteSessions = append(ch.DeleteSessions, h)
		}
	}
	w.commitLocked(ch)
	delete(w.st.creds, login)
	w.mu.Unlock()
	w.cancel(func(f *inFlight) bool { return f.login == login })
}

// Counts is how many passkeys login has and how many browsers are logged in as it.
func (w *Web) Counts(login string) (passkeys, sessions int) {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.st.sessions {
		if s.Login == login && !sessionExpired(s, now) {
			sessions++
		}
	}
	return len(w.st.creds[login].Passkeys), sessions
}

// HasPasskey: some login of ir_web has a passkey.
func (w *Web) HasPasskey() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hasPasskeyLocked()
}

// CheckLoginName returns the stored form of name if a new login of ir_web could take it: no login
// nor a live invite has it.
func (w *Web) CheckLoginName(name string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.checkNameLocked(name, "")
}
