package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// terminalsSchema is migration 3. The tables of migration 1 for terminals were placeholders and
// never written; the gate's endpoints take their place (docs/gate.md "存储接口"). Permissions have
// no foreign key: the built-in 本机 is no row, and the gate deletes a terminal in the same
// transaction that deletes its rules (ApplyGate).
const terminalsSchema = `DROP TABLE permissions;
	DROP TABLE terminals;
	-- The gate's endpoints. Times in ns since the epoch, 0 for none.
	CREATE TABLE terminals (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		user_handle BLOB,
		passkeys TEXT NOT NULL DEFAULT '[]',
		token_hash TEXT UNIQUE,
		avatar BLOB,
		avatar_version TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE permissions (
		terminal TEXT PRIMARY KEY,
		rules TEXT NOT NULL
	);
	CREATE TABLE invites (
		id TEXT PRIMARY KEY,
		hash TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		setup INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL DEFAULT 0
	);
	-- Logged-in panel browsers: hash is the SHA-256 of the cookie.
	CREATE TABLE logins (
		hash TEXT PRIMARY KEY,
		terminal TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE INDEX logins_terminal ON logins(terminal);
	-- Who opened a session through Iridium; one not here is 本机's.
	CREATE TABLE openers (
		session TEXT PRIMARY KEY,
		terminal TEXT NOT NULL,
		at INTEGER NOT NULL
	);
	ALTER TABLE inputs ADD COLUMN tag TEXT NOT NULL DEFAULT '';
	CREATE INDEX usage_terminal ON usage(terminal, at);`

// devicesSchema is migration 7: the device each terminal is on, "computer", "laptop", "phone" or
// "none" (no badge shown); no row is a computer. No foreign key, like permissions: 本机 is no row.
const devicesSchema = `CREATE TABLE devices (
		terminal TEXT PRIMARY KEY,
		device TEXT NOT NULL
	);`

// ownerSchema is migration 5: an invite made with ir invite --owner says so. Its step,
// claudeForFirstPanel, gives the first panel Claude.
const ownerSchema = `ALTER TABLE invites ADD COLUMN owner INTEGER NOT NULL DEFAULT 0;`

// claudeForFirstPanel is migration 5's step, run once: Claude only for the owner's own terminals,
// and the first panel registered is the owner's (plan "交互终端"). Panels from before the rule have
// Claude only if a panel turned it on; when none has it, the earliest registered gets it.
func claudeForFirstPanel(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT t.id, IFNULL(p.rules, '') FROM terminals t LEFT JOIN permissions p ON p.terminal = t.id
		WHERE t.kind = ? ORDER BY t.created_at, t.id`, gate.KindPanel)
	if err != nil {
		return err
	}
	type panel struct {
		id    string
		rules api.Rules
	}
	var panels []panel
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		r := access.Default()
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &r); err != nil {
				rows.Close()
				return fmt.Errorf("rules of %s: %w", id, err)
			}
		}
		panels = append(panels, panel{id, r})
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(panels) == 0 {
		return err
	}
	for _, p := range panels {
		if access.HasClaude(p.rules) {
			return nil
		}
	}
	b, err := json.Marshal(access.WithClaude(panels[0].rules, access.ByFirst, now()))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO permissions (terminal, rules) VALUES (?, ?) ON CONFLICT (terminal) DO UPDATE SET rules = excluded.rules`, panels[0].id, string(b))
	return err
}

// Gate is the store as the gate's Store: its state in the terminals, invites and logins tables.
func (s *Store) Gate() gate.Store { return gateStore{s} }

type gateStore struct{ s *Store }

func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func (g gateStore) Load() (gate.State, error) {
	st := gate.State{Endpoints: []gate.Endpoint{}, Invites: []gate.Invite{}, Sessions: []gate.Session{}}
	db := g.s.db
	rows, err := db.Query(`SELECT id, name, kind, user_handle, passkeys, IFNULL(token_hash, ''), avatar, avatar_version, created_at, last_seen_at FROM terminals ORDER BY created_at, id`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var e gate.Endpoint
		var passkeys string
		var created, seen int64
		if err := rows.Scan(&e.ID, &e.Name, &e.Kind, &e.UserHandle, &passkeys, &e.TokenHash, &e.Avatar, &e.AvatarVersion, &created, &seen); err != nil {
			rows.Close()
			return st, err
		}
		if err := json.Unmarshal([]byte(passkeys), &e.Passkeys); err != nil {
			rows.Close()
			return st, err
		}
		e.CreatedAt, e.LastSeenAt = fromNanos(created), fromNanos(seen)
		st.Endpoints = append(st.Endpoints, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(`SELECT id, hash, name, setup, owner, created_at, expires_at FROM invites ORDER BY created_at, id`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var i gate.Invite
		var created, expires int64
		if err := rows.Scan(&i.ID, &i.Hash, &i.Name, &i.Setup, &i.Owner, &created, &expires); err != nil {
			rows.Close()
			return st, err
		}
		i.CreatedAt, i.ExpiresAt = fromNanos(created), fromNanos(expires)
		st.Invites = append(st.Invites, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(`SELECT hash, terminal, created_at, last_seen_at, expires_at FROM logins ORDER BY created_at, hash`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var l gate.Session
		var created, seen, expires int64
		if err := rows.Scan(&l.Hash, &l.EndpointID, &created, &seen, &expires); err != nil {
			return st, err
		}
		l.CreatedAt, l.LastSeenAt, l.ExpiresAt = fromNanos(created), fromNanos(seen), fromNanos(expires)
		st.Sessions = append(st.Sessions, l)
	}
	return st, rows.Err()
}

// Apply is one transaction: the deletions, then the upserts. A terminal deleted takes its
// permissions with it.
func (g gateStore) Apply(c gate.Change) error {
	return g.s.Tx(func(tx *sql.Tx) error {
		for _, id := range c.DeleteEndpoints {
			if _, err := tx.Exec(`DELETE FROM terminals WHERE id = ?`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM permissions WHERE terminal = ?`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM devices WHERE terminal = ?`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM bot_follows WHERE bot = ? OR panel = ?`, id, id); err != nil {
				return err
			}
		}
		for _, id := range c.DeleteInvites {
			if _, err := tx.Exec(`DELETE FROM invites WHERE id = ?`, id); err != nil {
				return err
			}
		}
		for _, h := range c.DeleteSessions {
			if _, err := tx.Exec(`DELETE FROM logins WHERE hash = ?`, h); err != nil {
				return err
			}
		}
		for _, e := range c.Endpoints {
			passkeys, err := json.Marshal(e.Passkeys)
			if err != nil {
				return err
			}
			if e.Passkeys == nil {
				passkeys = []byte("[]")
			}
			var token any
			if e.TokenHash != "" {
				token = e.TokenHash
			}
			_, err = tx.Exec(`INSERT INTO terminals (id, name, kind, user_handle, passkeys, token_hash, avatar, avatar_version, created_at, last_seen_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET name = excluded.name, kind = excluded.kind, user_handle = excluded.user_handle,
					passkeys = excluded.passkeys, token_hash = excluded.token_hash, avatar = excluded.avatar,
					avatar_version = excluded.avatar_version, created_at = excluded.created_at, last_seen_at = excluded.last_seen_at`,
				e.ID, e.Name, e.Kind, e.UserHandle, string(passkeys), token, e.Avatar, e.AvatarVersion, nanos(e.CreatedAt), nanos(e.LastSeenAt))
			if err != nil {
				return err
			}
		}
		for _, i := range c.Invites {
			_, err := tx.Exec(`INSERT INTO invites (id, hash, name, setup, owner, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET hash = excluded.hash, name = excluded.name, setup = excluded.setup,
					owner = excluded.owner, created_at = excluded.created_at, expires_at = excluded.expires_at`,
				i.ID, i.Hash, i.Name, i.Setup, i.Owner, nanos(i.CreatedAt), nanos(i.ExpiresAt))
			if err != nil {
				return err
			}
		}
		for _, l := range c.Sessions {
			_, err := tx.Exec(`INSERT INTO logins (hash, terminal, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (hash) DO UPDATE SET terminal = excluded.terminal, created_at = excluded.created_at,
					last_seen_at = excluded.last_seen_at, expires_at = excluded.expires_at`,
				l.Hash, l.EndpointID, nanos(l.CreatedAt), nanos(l.LastSeenAt), nanos(l.ExpiresAt))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// TerminalRules is terminal's permission table as stored (JSON), ok false when it has none of its own.
func (s *Store) TerminalRules(terminal string) (rules []byte, ok bool, err error) {
	err = s.db.QueryRow(`SELECT rules FROM permissions WHERE terminal = ?`, terminal).Scan(&rules)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return rules, err == nil, err
}

// SetTerminalRules stores terminal's permission table (JSON).
func (s *Store) SetTerminalRules(terminal string, rules []byte) error {
	_, err := s.db.Exec(`INSERT INTO permissions (terminal, rules) VALUES (?, ?) ON CONFLICT (terminal) DO UPDATE SET rules = excluded.rules`, terminal, string(rules))
	return err
}

// Devices is the device each terminal set (SetDevice), by terminal ID.
func (s *Store) Devices() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT terminal, device FROM devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}

// Device is the device terminal set, "" for none.
func (s *Store) Device(terminal string) (string, error) {
	var d string
	err := s.db.QueryRow(`SELECT device FROM devices WHERE terminal = ?`, terminal).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return d, err
}

// SetDevice stores the device terminal is on.
func (s *Store) SetDevice(terminal, device string) error {
	_, err := s.db.Exec(`INSERT INTO devices (terminal, device) VALUES (?, ?) ON CONFLICT (terminal) DO UPDATE SET device = excluded.device`, terminal, device)
	return err
}

// SetOpener records that terminal opened session.
func (s *Store) SetOpener(session, terminal string) error {
	_, err := s.db.Exec(`INSERT INTO openers (session, terminal, at) VALUES (?, ?, ?) ON CONFLICT (session) DO NOTHING`, session, terminal, now())
	return err
}

// Openers is who opened each session Iridium opened, by session id.
func (s *Store) Openers() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT session, terminal FROM openers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sess, t string
		if err := rows.Scan(&sess, &t); err != nil {
			return nil, err
		}
		out[sess] = t
	}
	return out, rows.Err()
}

// Opener is who opened session; "" for one Iridium did not open.
func (s *Store) Opener(session string) string {
	var t string
	_ = s.db.QueryRow(`SELECT terminal FROM openers WHERE session = ?`, session).Scan(&t)
	return t
}

// TokensSince is the tokens (input, output and cache) of the turns terminal started since at (ms).
func (s *Store) TokensSince(terminal string, at int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT IFNULL(SUM(input + output + cache_read + cache_write), 0) FROM usage WHERE terminal = ? AND at >= ?`, terminal, at).Scan(&n)
	return n, err
}

// SourceByText finds who sent a message of session's from its text, for agents whose transcripts
// keep no id of Iridium's (pi, dsh): the latest input sent whose "[tag] text" (or text, for a slash
// command) is text. It gives the terminal's ID and the tag.
func (s *Store) SourceByText(session, text string) (terminal, tag string, ok bool) {
	err := s.db.QueryRow(`SELECT terminal, IIF(tag = '', terminal, tag) FROM inputs
		WHERE session = ? AND state = 'sent' AND ('[' || IIF(tag = '', terminal, tag) || '] ' || text = ? OR (text = ? AND text LIKE '/%'))
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, session, text, text).Scan(&terminal, &tag)
	return terminal, tag, err == nil
}
