package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Endpoint kinds.
const (
	KindPanel   = "panel"   // a web panel: one browser, logs in with a passkey
	KindChannel = "channel" // a plugin or peer: authenticates with its own token
)

// Endpoint is one registered interactive terminal (交互终端).
type Endpoint struct {
	ID   string `json:"id"`   // stable internal ID; names can change
	Name string `json:"name"` // unique, case-insensitive; see NormalizeName
	Kind string `json:"kind"` // KindPanel or KindChannel

	UserHandle []byte    `json:"userHandle,omitempty"` // panel: WebAuthn user handle, random per registration
	Passkeys   []Passkey `json:"passkeys,omitempty"`   // panel
	TokenHash  string    `json:"tokenHash,omitempty"`  // channel: SHA-256 (hex) of its bearer token

	Avatar        []byte `json:"avatar,omitempty"`        // 128×128 PNG or JPEG, re-encoded by the gate
	AvatarVersion string `json:"avatarVersion,omitempty"` // short hash of Avatar, for cache busting

	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// Passkey is a WebAuthn credential of a panel. Credential holds only public data.
type Passkey struct {
	Credential webauthn.Credential `json:"credential"`
	CreatedAt  time.Time           `json:"createdAt"`
	LastUsedAt time.Time           `json:"lastUsedAt"`
}

// Invite is a pending one-time registration link for a new panel.
type Invite struct {
	ID        string    `json:"id"`             // short ID for listing and revoking
	Hash      string    `json:"hash"`           // SHA-256 (hex) of the token; the token itself is never stored
	Name      string    `json:"name,omitempty"` // optional name chosen by the inviter; the joiner may change it
	Setup     bool      `json:"setup,omitempty"`
	Owner     bool      `json:"owner,omitempty"` // ir invite --owner: the joiner is the owner's own (Claude)
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"` // zero for the first-run setup token
}

// Session is a logged-in panel browser. Hash is the SHA-256 (hex) of the cookie value.
type Session struct {
	Hash       string    `json:"hash"`
	EndpointID string    `json:"endpointId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"` // absolute limit
}

// State is everything the gate persists.
type State struct {
	Endpoints []Endpoint `json:"endpoints"`
	Invites   []Invite   `json:"invites"`
	Sessions  []Session  `json:"sessions"`
}

// Change is one atomic update: the deletions, then the upserts (by ID, or Hash for sessions).
type Change struct {
	DeleteEndpoints []string
	DeleteInvites   []string
	DeleteSessions  []string
	Endpoints       []Endpoint
	Invites         []Invite
	Sessions        []Session
}

func (c Change) empty() bool {
	return len(c.DeleteEndpoints)+len(c.DeleteInvites)+len(c.DeleteSessions)+
		len(c.Endpoints)+len(c.Invites)+len(c.Sessions) == 0
}

// Store persists the gate's state. The state is small, so the gate keeps all of it in
// memory: it calls Load once, from New, and sends every change through Apply.
//
// Apply must be atomic (all of the Change or none of it). The gate serialises its calls,
// so a Store needs no locking for the gate's sake.
//
// For SQLite: tables endpoints (id primary key, name, kind, user_handle, token_hash
// unique, avatar blob, avatar_version, created_at, last_seen_at; passkeys as a JSON
// column or a child table keyed by credential ID), invites (id primary key, hash unique,
// name, setup, owner, created_at, expires_at) and sessions (hash primary key, endpoint_id
// indexed, created_at, last_seen_at, expires_at). Apply is one transaction: deletes, then
// INSERT ... ON CONFLICT DO UPDATE. The gate deletes an endpoint's sessions itself, in the
// same Change, so no cascade is needed.
type Store interface {
	Load() (State, error)
	Apply(Change) error
}

// memState is the in-memory form of State, shared by the gate and JSONStore.
type memState struct {
	endpoints map[string]Endpoint
	invites   map[string]Invite
	sessions  map[string]Session
}

func newMemState(s State) *memState {
	m := &memState{
		endpoints: make(map[string]Endpoint, len(s.Endpoints)),
		invites:   make(map[string]Invite, len(s.Invites)),
		sessions:  make(map[string]Session, len(s.Sessions)),
	}
	m.apply(Change{Endpoints: s.Endpoints, Invites: s.Invites, Sessions: s.Sessions})
	return m
}

func (m *memState) apply(c Change) {
	for _, id := range c.DeleteEndpoints {
		delete(m.endpoints, id)
	}
	for _, id := range c.DeleteInvites {
		delete(m.invites, id)
	}
	for _, h := range c.DeleteSessions {
		delete(m.sessions, h)
	}
	for _, e := range c.Endpoints {
		m.endpoints[e.ID] = e
	}
	for _, i := range c.Invites {
		m.invites[i.ID] = i
	}
	for _, s := range c.Sessions {
		m.sessions[s.Hash] = s
	}
}

func (m *memState) clone() *memState { return newMemState(m.state()) }

// state returns the content sorted, so files and listings are stable.
func (m *memState) state() State {
	s := State{
		Endpoints: sortedValues(m.endpoints, func(e Endpoint) (time.Time, string) { return e.CreatedAt, e.ID }),
		Invites:   sortedValues(m.invites, func(i Invite) (time.Time, string) { return i.CreatedAt, i.ID }),
		Sessions:  sortedValues(m.sessions, func(s Session) (time.Time, string) { return s.CreatedAt, s.Hash }),
	}
	return s
}

func sortedValues[V any](m map[string]V, key func(V) (time.Time, string)) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b V) int {
		ta, ia := key(a)
		tb, ib := key(b)
		if c := ta.Compare(tb); c != 0 {
			return c
		}
		return strings.Compare(ia, ib)
	})
	return out
}

// JSONStore is a Store in one JSON file, written atomically with mode 0600.
type JSONStore struct {
	path string
	mu   sync.Mutex
	cur  *memState
}

type jsonFile struct {
	Version int `json:"version"`
	State
}

// OpenJSONStore opens (or prepares) the file at path. Its directory is created with mode
// 0700 if missing. The file itself is first written on the first change.
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
		return nil, fmt.Errorf("gate store %s: %w", path, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("gate store %s: unknown version %d", path, f.Version)
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

// Apply writes the state with c applied to a temp file, then renames it into place.
func (s *JSONStore) Apply(c Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur.clone()
	next.apply(c)
	data, err := json.MarshalIndent(jsonFile{Version: 1, State: next.state()}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.cur = next
	return nil
}

// writeFileAtomic writes data to path with mode 0600 via a temp file in the same directory.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*") // created with mode 0600
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
