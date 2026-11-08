package store

import (
	"database/sql"
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

// ---- plugin chats ----

// ChatPointer is what a plugin's chat window points at.
type ChatPointer struct {
	Plugin, Chat string
	Session      string
	Agent, Cwd   string // for /new
}

// Pointer is the chat's pointer; none is the zero one.
func (s *Store) Pointer(plugin, chat string) (ChatPointer, error) {
	p := ChatPointer{Plugin: plugin, Chat: chat}
	var session, agent, cwd sql.NullString
	err := s.db.QueryRow(`SELECT session, agent, cwd FROM chat_pointers WHERE plugin = ? AND chat = ?`, plugin, chat).Scan(&session, &agent, &cwd)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	p.Session, p.Agent, p.Cwd = session.String, agent.String, cwd.String
	return p, err
}

func (s *Store) SetPointer(p ChatPointer) error {
	_, err := s.db.Exec(`INSERT INTO chat_pointers (plugin, chat, session, agent, cwd, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (plugin, chat) DO UPDATE SET session = excluded.session, agent = excluded.agent, cwd = excluded.cwd, updated_at = excluded.updated_at`,
		p.Plugin, p.Chat, nullable(p.Session), nullable(p.Agent), nullable(p.Cwd), now())
	return err
}

// PointedAt is, for each session some plugin's chat points at, those plugins (terminal IDs).
func (s *Store) PointedAt() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT session, plugin FROM chat_pointers WHERE session IS NOT NULL AND session != '' ORDER BY session, plugin`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var session, plugin string
		if err := rows.Scan(&session, &plugin); err != nil {
			return nil, err
		}
		out[session] = append(out[session], plugin)
	}
	return out, rows.Err()
}

// Pointing is every chat window that points at session.
func (s *Store) Pointing(session string) ([]ChatPointer, error) {
	rows, err := s.db.Query(`SELECT plugin, chat, IFNULL(agent, ''), IFNULL(cwd, '') FROM chat_pointers WHERE session = ? ORDER BY plugin, chat`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatPointer
	for rows.Next() {
		p := ChatPointer{Session: session}
		if err := rows.Scan(&p.Plugin, &p.Chat, &p.Agent, &p.Cwd); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OutboxLast is the plugin's newest event's sequence number, 0 if none.
func (s *Store) OutboxLast(plugin string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT IFNULL(MAX(seq), 0) FROM outbox WHERE plugin = ?`, plugin).Scan(&seq)
	return seq, err
}

// ChatSeen is when a chat window last said something with session in it ("": none chosen).
type ChatSeen struct {
	Plugin, Chat, Session string
	At                    int64
}

// Seen notes that the chat said something now, with session in it.
func (s *Store) Seen(plugin, chat, session string) error {
	_, err := s.db.Exec(`INSERT INTO chat_seen (plugin, chat, session, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (plugin, chat, session) DO UPDATE SET at = excluded.at`, plugin, chat, session, now())
	return err
}

// ChatsSeen is every chat window's notes, the latest first.
func (s *Store) ChatsSeen() ([]ChatSeen, error) {
	rows, err := s.db.Query(`SELECT plugin, chat, session, at FROM chat_seen ORDER BY at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSeen
	for rows.Next() {
		var c ChatSeen
		if err := rows.Scan(&c.Plugin, &c.Chat, &c.Session, &c.At); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
