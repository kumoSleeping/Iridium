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
)

// Endpoint kinds: what the gate holds, an ir_web login or an app. Not stored as such: the store
// keeps logins and apps in tables of their own (docs/next/decisions.md 1-4).
const (
	KindPanel   = "panel"   // a login of ir_web: one browser, logs in with a passkey
	KindChannel = "channel" // an app: authenticates with its own TOKEN
)

// Endpoint is one login of ir_web or one app, as the gate holds it.
type Endpoint struct {
	ID   string `json:"id"`   // stable internal ID; names can change
	Name string `json:"name"` // unique, case-insensitive, among its kind; see NormalizeName
	Kind string `json:"kind"` // KindPanel or KindChannel
	// Type: an app's type as shown (computer, bot, script…); display only, no rights.
	Type string `json:"type,omitempty"`

	TokenHash string `json:"tokenHash,omitempty"` // channel: SHA-256 (hex) of its bearer token

	Avatar        []byte `json:"avatar,omitempty"`        // 128×128 PNG or JPEG, re-encoded by the gate
	AvatarVersion string `json:"avatarVersion,omitempty"` // short hash of Avatar, for cache busting

	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	// ApprovedAt: when a login was approved (a browser: as it joined); zero for pending. Apps: zero.
	ApprovedAt time.Time `json:"approvedAt,omitzero"`
}

// State is everything the gate persists.
type State struct {
	Endpoints []Endpoint `json:"endpoints"`
}

// Change is one atomic update: the deletions, then the upserts (by ID).
type Change struct {
	DeleteEndpoints []string
	Endpoints       []Endpoint
}

func (c Change) empty() bool { return len(c.DeleteEndpoints)+len(c.Endpoints) == 0 }

// Store persists the gate's state. The state is small, so the gate keeps all of it in
// memory: it calls Load once, from New, and sends every change through Apply.
//
// Apply must be atomic (all of the Change or none of it). The gate serialises its calls,
// so a Store needs no locking for the gate's sake.
//
// The SQLite store (package store) keeps apps (KindChannel) in apps and ir_web's logins
// (KindPanel) in logins. Apply is one transaction: deletes, then INSERT ... ON CONFLICT DO UPDATE.
// What ir_web keeps of its logins (passkeys, web sessions, invites) is package webapp's.
type Store interface {
	Load() (State, error)
	Apply(Change) error
}

// memState is the in-memory form of State, shared by the gate and JSONStore.
type memState struct {
	endpoints map[string]Endpoint
}

func newMemState(s State) *memState {
	m := &memState{endpoints: make(map[string]Endpoint, len(s.Endpoints))}
	m.apply(Change{Endpoints: s.Endpoints})
	return m
}

func (m *memState) apply(c Change) {
	for _, id := range c.DeleteEndpoints {
		delete(m.endpoints, id)
	}
	for _, e := range c.Endpoints {
		m.endpoints[e.ID] = e
	}
}

func (m *memState) clone() *memState { return newMemState(m.state()) }

// state returns the content sorted, so files and listings are stable.
func (m *memState) state() State {
	return State{Endpoints: SortedValues(m.endpoints, func(e Endpoint) (time.Time, string) { return e.CreatedAt, e.ID })}
}

// SortedValues is m's values by key's time, then its string: stable files and listings.
func SortedValues[V any](m map[string]V, key func(V) (time.Time, string)) []V {
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
	if err := WriteFileAtomic(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.cur = next
	return nil
}

// WriteFileAtomic writes data to path with mode 0600 via a temp file in the same directory.
func WriteFileAtomic(path string, data []byte) error {
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
