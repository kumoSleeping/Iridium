package store

import (
	"errors"
)

// ---- the queue, more ----

// ErrNoInput: no such input waits in the queue.
var ErrNoInput = errors.New("no such queued message")

// EditInput changes the text of an input not sent yet.
func (s *Store) EditInput(id, text string) error {
	return s.one(`UPDATE inputs SET text = ? WHERE id = ? AND state != 'sent'`, text, id)
}

// DeleteInput drops an input not sent yet.
func (s *Store) DeleteInput(id string) error {
	return s.one(`DELETE FROM inputs WHERE id = ? AND state != 'sent'`, id)
}

// InputOf is input id's session and state, if the store has it.
func (s *Store) InputOf(id string) (session, state string, ok bool) {
	if s.db.QueryRow(`SELECT session, state FROM inputs WHERE id = ?`, id).Scan(&session, &state) != nil {
		return "", "", false
	}
	return session, state, true
}

// ForgetInput drops input id whatever its state: one whose writing to the agent failed.
func (s *Store) ForgetInput(id string) error {
	_, err := s.db.Exec(`DELETE FROM inputs WHERE id = ?`, id)
	return err
}

// MoveQueue moves the session's inputs in state from to state to (queued, paused).
func (s *Store) MoveQueue(session, from, to string) error {
	_, err := s.db.Exec(`UPDATE inputs SET state = ? WHERE session = ? AND state = ?`, to, session, from)
	return err
}

func (s *Store) one(q string, args ...any) error {
	r, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNoInput
	}
	return nil
}

// ---- always allow ----

type AllowRule struct {
	Tool    string
	Pattern string
}

func (s *Store) AddRule(session string, r AllowRule) error {
	_, err := s.db.Exec(`INSERT INTO allow_rules (session, tool, pattern, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, session, r.Tool, r.Pattern, now())
	return err
}

func (s *Store) Rules(session string) ([]AllowRule, error) {
	rows, err := s.db.Query(`SELECT tool, pattern FROM allow_rules WHERE session = ? ORDER BY created_at, rowid`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllowRule
	for rows.Next() {
		var r AllowRule
		if err := rows.Scan(&r.Tool, &r.Pattern); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClearRules drops the session's rules.
func (s *Store) ClearRules(session string) error {
	_, err := s.db.Exec(`DELETE FROM allow_rules WHERE session = ?`, session)
	return err
}

// ---- push ----

type PushSubscription struct {
	ID       int64
	Terminal string
	Endpoint string
	Keys     string // JSON: p256dh, auth
}

// AddPushSubscription keeps a browser's subscription; the same endpoint again replaces it.
func (s *Store) AddPushSubscription(p PushSubscription) error {
	_, err := s.db.Exec(`INSERT INTO push_subscriptions (terminal, endpoint, keys, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET terminal = excluded.terminal, keys = excluded.keys`, p.Terminal, p.Endpoint, p.Keys, now())
	return err
}

func (s *Store) DeletePushSubscription(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// DeleteTerminalPush drops every subscription of a terminal (revoked).
func (s *Store) DeleteTerminalPush(terminal string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE terminal = ?`, terminal)
	return err
}

func (s *Store) PushSubscriptions() ([]PushSubscription, error) {
	rows, err := s.db.Query(`SELECT id, terminal, endpoint, keys FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.Terminal, &p.Endpoint, &p.Keys); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- the outbox ----

// OutboxLast is the plugin's newest event's sequence number, 0 if none.
func (s *Store) OutboxLast(plugin string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT IFNULL(MAX(seq), 0) FROM outbox WHERE plugin = ?`, plugin).Scan(&seq)
	return seq, err
}
