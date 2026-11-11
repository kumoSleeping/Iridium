package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/webapp"
)

// appsSchema is migration 26 (0.2, docs/next/decisions.md 1-6): terminals split into apps and the
// logins of ir_web; the host stays "local".
//
//   - The old logins table (the panels' browser cookies) is web_sessions, by login.
//   - A channel is an app: its ID and token_hash kept, so its TOKEN still works. type is what it
//     is shown as, display only: "computer" for an executor, else "".
//   - A panel is a login of the built-in app ir_web, its ID kept; approved as it was made.
//   - logins is any app's: (app, id); approved_at NULL is a login waiting to be approved. perms is
//     a placeholder for S2's permissions; until then they stay in permissions, by ID.
//   - An invite is a slot for a login of ir_web, approved beforehand, with what it may do (perms,
//     the server's JSON): an --owner one's is Claude, by the person at the machine.
//   - A device is its app's or login's; the devices table keeps only the host's.
//   - Push subscriptions only of the host and ir_web's logins (an app's browser had none).
const appsSchema = `ALTER TABLE logins RENAME TO web_sessions;
	ALTER TABLE web_sessions RENAME COLUMN terminal TO login;
	DROP INDEX logins_terminal;
	CREATE INDEX web_sessions_login ON web_sessions(login);
	-- Times in ns since the epoch, 0 for none.
	CREATE TABLE apps (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT '',
		token_hash TEXT UNIQUE,
		avatar BLOB,
		avatar_version TEXT NOT NULL DEFAULT '',
		device TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE logins (
		app TEXT NOT NULL,
		id TEXT NOT NULL,
		name TEXT NOT NULL,
		avatar BLOB,
		avatar_version TEXT NOT NULL DEFAULT '',
		user_handle BLOB,
		passkeys TEXT NOT NULL DEFAULT '[]',
		device TEXT NOT NULL DEFAULT '',
		perms TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		approved_at INTEGER,
		last_active INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (app, id)
	);
	INSERT INTO apps (id, name, type, token_hash, avatar, avatar_version, device, created_at, last_seen_at)
		SELECT t.id, t.name, IIF(EXISTS (SELECT 1 FROM machines m WHERE m.terminal = t.id), 'computer', ''), t.token_hash,
			t.avatar, t.avatar_version, IFNULL((SELECT d.device FROM devices d WHERE d.terminal = t.id), ''), t.created_at, t.last_seen_at
		FROM terminals t WHERE t.kind = 'channel';
	INSERT INTO logins (app, id, name, avatar, avatar_version, user_handle, passkeys, device, created_at, approved_at, last_active)
		SELECT 'ir_web', t.id, t.name, t.avatar, t.avatar_version, t.user_handle, t.passkeys,
			IFNULL((SELECT d.device FROM devices d WHERE d.terminal = t.id), ''), t.created_at, t.created_at, t.last_seen_at
		FROM terminals t WHERE t.kind = 'panel';
	DELETE FROM devices WHERE terminal <> 'local';
	DROP TABLE terminals;
	ALTER TABLE invites ADD COLUMN perms TEXT NOT NULL DEFAULT '';
	UPDATE invites SET perms = json_object('claudeOwner', json_object('by', 'local', 'at', created_at / 1000000)) WHERE owner = 1;
	ALTER TABLE invites DROP COLUMN owner;
	DELETE FROM push_subscriptions WHERE terminal <> 'local'
		AND terminal NOT IN (SELECT id FROM logins WHERE app = 'ir_web');`

// Gate is the store as the core's (package gate's) Store: apps and the logins of ir_web.
func (s *Store) Gate() gate.Store { return gateStore{s} }

// Web is the store as ir_web's (package webapp's) Store: its logins' passkeys (in their rows of
// logins), invites and web sessions.
func (s *Store) Web() webapp.Store { return webStore{s} }

type gateStore struct{ s *Store }

type webStore struct{ s *Store }

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
	st := gate.State{Endpoints: []gate.Endpoint{}}
	rows, err := g.s.db.Query(`SELECT id, name, kind, type, token_hash, avatar, avatar_version, created_at, last_seen_at, approved_at FROM (
			SELECT id, name, 'channel' AS kind, type, IFNULL(token_hash, '') AS token_hash, avatar, avatar_version, created_at, last_seen_at, 0 AS approved_at FROM apps
			UNION ALL SELECT id, name, 'panel', '', '', avatar, avatar_version, created_at, last_active, IFNULL(approved_at, 0) FROM logins WHERE app = ?
		) ORDER BY created_at, id`, gate.WebApp)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var e gate.Endpoint
		var created, seen, approved int64
		if err := rows.Scan(&e.ID, &e.Name, &e.Kind, &e.Type, &e.TokenHash, &e.Avatar, &e.AvatarVersion, &created, &seen, &approved); err != nil {
			return st, err
		}
		e.CreatedAt, e.LastSeenAt, e.ApprovedAt = fromNanos(created), fromNanos(seen), fromNanos(approved)
		st.Endpoints = append(st.Endpoints, e)
	}
	return st, rows.Err()
}

// Apply is one transaction: the deletions, then the upserts. An app or login deleted takes its
// permissions, and an app its logins, with it; a login of ir_web its passkeys (in its row).
func (g gateStore) Apply(c gate.Change) error {
	return g.s.Tx(func(tx *sql.Tx) error {
		for _, id := range c.DeleteEndpoints {
			for _, q := range []string{
				`DELETE FROM apps WHERE id = ?1`,
				`DELETE FROM logins WHERE app = ?1 OR (app = 'ir_web' AND id = ?1)`,
				`DELETE FROM permissions WHERE terminal = ?1`,
			} {
				if _, err := tx.Exec(q, id); err != nil {
					return err
				}
			}
		}
		for _, e := range c.Endpoints {
			var err error
			if e.Kind == gate.KindPanel {
				var approved any
				if !e.ApprovedAt.IsZero() {
					approved = nanos(e.ApprovedAt)
				}
				_, err = tx.Exec(`INSERT INTO logins (app, id, name, avatar, avatar_version, created_at, approved_at, last_active)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT (app, id) DO UPDATE SET name = excluded.name, avatar = excluded.avatar, avatar_version = excluded.avatar_version,
						created_at = excluded.created_at, approved_at = excluded.approved_at, last_active = excluded.last_active`,
					gate.WebApp, e.ID, e.Name, e.Avatar, e.AvatarVersion, nanos(e.CreatedAt), approved, nanos(e.LastSeenAt))
			} else {
				var token any
				if e.TokenHash != "" {
					token = e.TokenHash
				}
				_, err = tx.Exec(`INSERT INTO apps (id, name, type, token_hash, avatar, avatar_version, created_at, last_seen_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT (id) DO UPDATE SET name = excluded.name, type = excluded.type, token_hash = excluded.token_hash,
						avatar = excluded.avatar, avatar_version = excluded.avatar_version, created_at = excluded.created_at,
						last_seen_at = excluded.last_seen_at`,
					e.ID, e.Name, e.Type, token, e.Avatar, e.AvatarVersion, nanos(e.CreatedAt), nanos(e.LastSeenAt))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (w webStore) Load() (webapp.State, error) {
	st := webapp.State{Creds: []webapp.Cred{}, Invites: []webapp.Invite{}, Sessions: []webapp.Session{}}
	db := w.s.db
	rows, err := db.Query(`SELECT id, user_handle, passkeys FROM logins WHERE app = ? ORDER BY created_at, id`, gate.WebApp)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var c webapp.Cred
		var passkeys string
		if err := rows.Scan(&c.Login, &c.UserHandle, &passkeys); err != nil {
			rows.Close()
			return st, err
		}
		if err := json.Unmarshal([]byte(passkeys), &c.Passkeys); err != nil {
			rows.Close()
			return st, err
		}
		if len(c.Passkeys) == 0 {
			c.Passkeys = nil
		}
		st.Creds = append(st.Creds, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(`SELECT id, hash, name, setup, perms, created_at, expires_at FROM invites ORDER BY created_at, id`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var i webapp.Invite
		var perms string
		var created, expires int64
		if err := rows.Scan(&i.ID, &i.Hash, &i.Name, &i.Setup, &perms, &created, &expires); err != nil {
			rows.Close()
			return st, err
		}
		if perms != "" {
			i.Perms = []byte(perms)
		}
		i.CreatedAt, i.ExpiresAt = fromNanos(created), fromNanos(expires)
		st.Invites = append(st.Invites, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(`SELECT hash, login, created_at, last_seen_at, expires_at FROM web_sessions ORDER BY created_at, hash`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var l webapp.Session
		var created, seen, expires int64
		if err := rows.Scan(&l.Hash, &l.Login, &created, &seen, &expires); err != nil {
			return st, err
		}
		l.CreatedAt, l.LastSeenAt, l.ExpiresAt = fromNanos(created), fromNanos(seen), fromNanos(expires)
		st.Sessions = append(st.Sessions, l)
	}
	return st, rows.Err()
}

// Apply is one transaction: the deletions, then the upserts. A login's credentials go into its
// row of logins, which must be there (the core made it).
func (w webStore) Apply(c webapp.Change) error {
	return w.s.Tx(func(tx *sql.Tx) error {
		for _, id := range c.DeleteInvites {
			if _, err := tx.Exec(`DELETE FROM invites WHERE id = ?`, id); err != nil {
				return err
			}
		}
		for _, h := range c.DeleteSessions {
			if _, err := tx.Exec(`DELETE FROM web_sessions WHERE hash = ?`, h); err != nil {
				return err
			}
		}
		for _, x := range c.Creds {
			passkeys := []byte("[]")
			if x.Passkeys != nil {
				var err error
				if passkeys, err = json.Marshal(x.Passkeys); err != nil {
					return err
				}
			}
			res, err := tx.Exec(`UPDATE logins SET user_handle = ?, passkeys = ? WHERE app = ? AND id = ?`,
				x.UserHandle, string(passkeys), gate.WebApp, x.Login)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("store: no login %s of ir_web for its passkeys", x.Login)
			}
		}
		for _, i := range c.Invites {
			_, err := tx.Exec(`INSERT INTO invites (id, hash, name, setup, perms, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET hash = excluded.hash, name = excluded.name, setup = excluded.setup,
					perms = excluded.perms, created_at = excluded.created_at, expires_at = excluded.expires_at`,
				i.ID, i.Hash, i.Name, i.Setup, string(i.Perms), nanos(i.CreatedAt), nanos(i.ExpiresAt))
			if err != nil {
				return err
			}
		}
		for _, l := range c.Sessions {
			_, err := tx.Exec(`INSERT INTO web_sessions (hash, login, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (hash) DO UPDATE SET login = excluded.login, created_at = excluded.created_at,
					last_seen_at = excluded.last_seen_at, expires_at = excluded.expires_at`,
				l.Hash, l.Login, nanos(l.CreatedAt), nanos(l.LastSeenAt), nanos(l.ExpiresAt))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// Devices is the device each app, login and the host set (SetDevice), by storage ID.
func (s *Store) Devices() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, device FROM apps WHERE device <> ''
		UNION ALL SELECT id, device FROM logins WHERE device <> ''
		UNION ALL SELECT terminal, device FROM devices`)
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

// Device is the device id (an app's, an ir_web login's, or the host's) set, "" for none.
func (s *Store) Device(id string) (string, error) {
	var d string
	err := s.db.QueryRow(`SELECT device FROM apps WHERE id = ?1
		UNION ALL SELECT device FROM logins WHERE app = 'ir_web' AND id = ?1
		UNION ALL SELECT device FROM devices WHERE terminal = ?1 LIMIT 1`, id).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return d, err
}

// ErrNoOne: no app, login of ir_web or host by that ID.
var ErrNoOne = errors.New("no such app or login")

// SetDevice stores the device id (an app, a login of ir_web, or the host) is on.
func (s *Store) SetDevice(id, device string) error {
	if id == gate.HostID {
		_, err := s.db.Exec(`INSERT INTO devices (terminal, device) VALUES (?, ?) ON CONFLICT (terminal) DO UPDATE SET device = excluded.device`, id, device)
		return err
	}
	return s.Tx(func(tx *sql.Tx) error {
		n := int64(0)
		for _, q := range []string{`UPDATE apps SET device = ?2 WHERE id = ?1`, `UPDATE logins SET device = ?2 WHERE app = 'ir_web' AND id = ?1`} {
			r, err := tx.Exec(q, id, device)
			if err != nil {
				return err
			}
			k, _ := r.RowsAffected()
			n += k
		}
		if n == 0 {
			return ErrNoOne
		}
		return nil
	})
}
