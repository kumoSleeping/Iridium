package webapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// Passkey is a WebAuthn credential of a login of ir_web, one device of it (or the devices a
// provider syncs it to). Credential holds only public data.
type Passkey struct {
	Credential webauthn.Credential `json:"credential"`
	CreatedAt  time.Time           `json:"createdAt"`
	LastUsedAt time.Time           `json:"lastUsedAt"`
	// UserHandle: its own, random as it was made; empty for one made before a login had several,
	// whose handle is its login's (Cred.UserHandle).
	UserHandle []byte `json:"userHandle,omitempty"`
	// Device: what it is on (api.Device), as set by hand; "" by the browser it is used in.
	Device string `json:"device,omitempty"`
	// Platform: the browser it was made in, "Mac · Chrome" (platformOf).
	Platform string `json:"platform,omitempty"`
}

// handle is the user handle pk was made with.
func (pk Passkey) handle(c Cred) []byte {
	if len(pk.UserHandle) > 0 {
		return pk.UserHandle
	}
	return c.UserHandle
}

// Cred is what ir_web keeps to know a login by: its passkeys, each with its own WebAuthn user
// handle (random per registration; UserHandle is the first's, from before). The login itself
// (name, avatar) is the core's (package gate).
type Cred struct {
	Login      string    `json:"login"`
	UserHandle []byte    `json:"userHandle,omitempty"`
	Passkeys   []Passkey `json:"passkeys,omitempty"`
}

// Invite is a slot for a new login of ir_web, approved beforehand: a one-time registration link.
type Invite struct {
	ID    string `json:"id"`             // short ID for listing and revoking
	Hash  string `json:"hash"`           // SHA-256 (hex) of the token; the token itself is never stored
	Name  string `json:"name,omitempty"` // optional name chosen by the inviter; the joiner may change it
	Setup bool   `json:"setup,omitempty"`
	// Perms: what the login that joins may do (the server's JSON; ir_web does not read it).
	Perms []byte `json:"perms,omitempty"`
	// Login: an invite to add a passkey to this login (a device of it), not a new login.
	Login     string    `json:"login,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"` // zero for the first-run setup token
}

// Session is a logged-in browser (a web session of a login). Hash is the SHA-256 (hex) of the
// cookie's value.
type Session struct {
	Hash  string `json:"hash"`
	Login string `json:"login"`
	// Passkey: the credential ID it logged in with (nil for one from before); Device, what its
	// browser said it is (deviceOfBrowser), for a passkey set to none by hand.
	Passkey    []byte    `json:"passkey,omitempty"`
	Device     string    `json:"device,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"` // absolute limit
}

// State is everything ir_web persists.
type State struct {
	Creds    []Cred    `json:"creds"`
	Invites  []Invite  `json:"invites"`
	Sessions []Session `json:"sessions"`
}

// Change is one atomic update: the deletions, then the upserts (by login, ID, or Hash).
type Change struct {
	DeleteInvites  []string
	DeleteSessions []string
	Creds          []Cred
	Invites        []Invite
	Sessions       []Session
}

func (c Change) empty() bool {
	return len(c.DeleteInvites)+len(c.DeleteSessions)+len(c.Creds)+len(c.Invites)+len(c.Sessions) == 0
}

// Store persists ir_web's state. It is small: Load is called once, from New, and every change
// goes through Apply, which must be atomic.
//
// The SQLite store (package store) keeps a login's credentials in its row of logins (user_handle,
// passkeys; the row is the core's and goes with the login), invites in invites and sessions in
// web_sessions.
type Store interface {
	Load() (State, error)
	Apply(Change) error
}

type memState struct {
	creds    map[string]Cred
	invites  map[string]Invite
	sessions map[string]Session
}

func newMemState(s State) *memState {
	m := &memState{creds: map[string]Cred{}, invites: map[string]Invite{}, sessions: map[string]Session{}}
	m.apply(Change{Creds: s.Creds, Invites: s.Invites, Sessions: s.Sessions})
	return m
}

func (m *memState) apply(c Change) {
	for _, id := range c.DeleteInvites {
		delete(m.invites, id)
	}
	for _, h := range c.DeleteSessions {
		delete(m.sessions, h)
	}
	for _, x := range c.Creds {
		m.creds[x.Login] = x
	}
	for _, i := range c.Invites {
		m.invites[i.ID] = i
	}
	for _, s := range c.Sessions {
		m.sessions[s.Hash] = s
	}
}

func (m *memState) state() State {
	return State{
		Creds:    gate.SortedValues(m.creds, func(c Cred) (time.Time, string) { return time.Time{}, c.Login }),
		Invites:  gate.SortedValues(m.invites, func(i Invite) (time.Time, string) { return i.CreatedAt, i.ID }),
		Sessions: gate.SortedValues(m.sessions, func(s Session) (time.Time, string) { return s.CreatedAt, s.Hash }),
	}
}

// JSONStore is a Store in one JSON file, written atomically with mode 0600 (for tests).
type JSONStore struct {
	path string
	mu   sync.Mutex
	cur  *memState
}

type jsonFile struct {
	Version int `json:"version"`
	State
}

// OpenJSONStore opens (or prepares) the file at path.
func OpenJSONStore(path string) (*JSONStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	s := &JSONStore{path: path, cur: newMemState(State{})}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f jsonFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("web store %s: %w", path, err)
	}
	s.cur = newMemState(f.State)
	return s, nil
}

// Load returns the stored state.
func (s *JSONStore) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.state(), nil
}

// Apply writes the state with c applied.
func (s *JSONStore) Apply(c Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := newMemState(s.cur.state())
	next.apply(c)
	data, err := json.MarshalIndent(jsonFile{Version: 1, State: next.state()}, "", "  ")
	if err != nil {
		return err
	}
	if err := gate.WriteFileAtomic(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.cur = next
	return nil
}
