package store

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

// ownerSchema is migration 5: an invite made with ir invite --owner said so (Claude for the owner's
// own, gone 2026-11-27: the permissions table and this column are no longer read).
const ownerSchema = `ALTER TABLE invites ADD COLUMN owner INTEGER NOT NULL DEFAULT 0;`

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
