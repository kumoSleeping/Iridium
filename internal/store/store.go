// Package store is Iridium's database: SQLite through a pure-Go driver, WAL, writes in BEGIN
// IMMEDIATE transactions. It holds only what lies over the sessions found on disk (plan "数据"):
// workspaces and who belongs where, pins, which sessions are shown, names, the queue of messages to send, push
// subscriptions, interactive terminals and their permissions, usage, and the plugin event outbox.
// Never the conversations themselves, nor asks.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// schema is every version's migration, in order; PRAGMA user_version says how many ran. Only
// ever appended to.
var schema = []string{
	// 1
	`CREATE TABLE workspaces (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		path TEXT NOT NULL UNIQUE,
		sort INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	-- manual: dragged there, so discovery never moves it again.
	CREATE TABLE assignments (
		session TEXT PRIMARY KEY,
		workspace INTEGER REFERENCES workspaces(id) ON DELETE CASCADE,
		manual INTEGER NOT NULL DEFAULT 0,
		at INTEGER NOT NULL
	);
	CREATE TABLE pins (session TEXT PRIMARY KEY, at INTEGER NOT NULL);
	CREATE TABLE archive (session TEXT PRIMARY KEY, at INTEGER NOT NULL);
	CREATE TABLE names (session TEXT PRIMARY KEY, name TEXT NOT NULL);
	-- Per session settings over the config's: idle_reap in seconds, 0 off, NULL the config's.
	CREATE TABLE session_settings (
		session TEXT PRIMARY KEY,
		idle_reap INTEGER,
		model TEXT,
		mode TEXT
	);
	-- Messages for agents. id: what the agent's record names it by (Claude: the uuid), so the
	-- transcript reader knows which messages Iridium wrote and from whom. state: queued, paused,
	-- sent.
	CREATE TABLE inputs (
		id TEXT PRIMARY KEY,
		session TEXT NOT NULL,
		terminal TEXT NOT NULL,
		text TEXT NOT NULL,
		state TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		sent_at INTEGER
	);
	CREATE INDEX inputs_session ON inputs(session, created_at);
	-- Interactive terminals (plan "交互终端"): panels, plugins, peers, the local ir. Placeholders.
	CREATE TABLE terminals (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE UNIQUE INDEX terminals_name ON terminals(name COLLATE NOCASE);
	CREATE TABLE permissions (
		terminal TEXT PRIMARY KEY REFERENCES terminals(id) ON DELETE CASCADE,
		rules TEXT NOT NULL
	);
	CREATE TABLE push_subscriptions (
		id INTEGER PRIMARY KEY,
		terminal TEXT NOT NULL,
		endpoint TEXT NOT NULL UNIQUE,
		keys TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	-- One row per turn and model: who asked (terminal), what it cost.
	CREATE TABLE usage (
		id INTEGER PRIMARY KEY,
		at INTEGER NOT NULL,
		terminal TEXT NOT NULL,
		session TEXT NOT NULL,
		agent TEXT NOT NULL,
		model TEXT NOT NULL,
		input INTEGER NOT NULL,
		output INTEGER NOT NULL,
		cache_read INTEGER NOT NULL,
		cache_write INTEGER NOT NULL,
		cost_usd REAL
	);
	CREATE INDEX usage_at ON usage(at);
	-- The plugin events: the only events stored, kept 24 hours for a plugin to read on after it
	-- reconnects (Last-Event-ID).
	CREATE TABLE outbox (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		plugin TEXT NOT NULL,
		chat TEXT NOT NULL,
		type TEXT NOT NULL,
		data TEXT NOT NULL,
		at INTEGER NOT NULL
	);
	CREATE INDEX outbox_plugin ON outbox(plugin, seq);`,
	// 2: session effort, "always allow" rules, push settings by workspace, plugin chat pointers.
	`ALTER TABLE session_settings ADD COLUMN effort TEXT;
	CREATE TABLE allow_rules (
		session TEXT NOT NULL,
		tool TEXT NOT NULL,
		pattern TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (session, tool, pattern)
	);
	-- workspace 0: sessions in none. No row: the defaults (needs you and errors on).
	CREATE TABLE push_settings (
		workspace INTEGER PRIMARY KEY,
		needs_you INTEGER NOT NULL,
		error INTEGER NOT NULL,
		turn_end INTEGER NOT NULL,
		compact INTEGER NOT NULL
	);
	-- What a plugin's chat window points at: its session, and what /new opens.
	CREATE TABLE chat_pointers (
		plugin TEXT NOT NULL,
		chat TEXT NOT NULL,
		session TEXT,
		agent TEXT,
		cwd TEXT,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (plugin, chat)
	);
	CREATE INDEX chat_pointers_session ON chat_pointers(session);`,
	// 3: interactive terminals for real (the gate's endpoints, invites and logins; see
	// terminals.go), who opened which session, and the tag an input was written with.
	terminalsSchema,
	// 4: the shown marker in place of the archive flag (shown.go).
	shownSchema,
}

type Store struct{ db *sql.DB }

// Open opens (and makes, or migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Version is the schema version the database is at.
func (s *Store) Version() (int, error) {
	var v int
	err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

func (s *Store) migrate() error {
	return s.Tx(func(tx *sql.Tx) error {
		var v int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			return err
		}
		if v > len(schema) {
			return fmt.Errorf("schema version %d is newer than this binary's %d", v, len(schema))
		}
		for ; v < len(schema); v++ {
			if _, err := tx.Exec(schema[v]); err != nil {
				return fmt.Errorf("migration %d: %w", v+1, err)
			}
		}
		_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v))
		return err
	})
}

// Tx runs f in a BEGIN IMMEDIATE transaction: it takes the write lock at once, so two writers
// never deadlock upgrading a read.
func (s *Store) Tx(f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func now() int64 { return time.Now().UnixMilli() }

// ---- what lies over a session ----

// Overlay is what the database says of a session. Whether it is shown is its marker's
// (shown.go), by its agent.
type Overlay struct {
	Name      string
	Pinned    bool
	Workspace int64
	Manual    bool
}

// Overlays is every session's overlay, by session id.
func (s *Store) Overlays() (map[string]Overlay, error) {
	rows, err := s.db.Query(`
		SELECT session, MAX(name), MAX(pinned), MAX(workspace), MAX(manual) FROM (
			SELECT session, name, 0 AS pinned, 0 AS workspace, 0 AS manual FROM names
			UNION ALL SELECT session, '', 1, 0, 0 FROM pins
			UNION ALL SELECT session, '', 0, IFNULL(workspace, 0), manual FROM assignments
		) GROUP BY session`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Overlay{}
	for rows.Next() {
		var id string
		var o Overlay
		if err := rows.Scan(&id, &o.Name, &o.Pinned, &o.Workspace, &o.Manual); err != nil {
			return nil, err
		}
		out[id] = o
	}
	return out, rows.Err()
}

// SetName renames a session; "" takes the name off.
func (s *Store) SetName(session, name string) error {
	if name == "" {
		_, err := s.db.Exec(`DELETE FROM names WHERE session = ?`, session)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO names (session, name) VALUES (?, ?) ON CONFLICT (session) DO UPDATE SET name = excluded.name`, session, name)
	return err
}

func (s *Store) SetPinned(session string, on bool) error { return s.flag("pins", session, on) }

func (s *Store) flag(table, session string, on bool) error {
	q := `DELETE FROM ` + table + ` WHERE session = ?`
	args := []any{session}
	if on {
		q = `INSERT INTO ` + table + ` (session, at) VALUES (?, ?) ON CONFLICT (session) DO NOTHING`
		args = append(args, now())
	}
	_, err := s.db.Exec(q, args...)
	return err
}

type Workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Store) AddWorkspace(name, path string) (int64, error) {
	r, err := s.db.Exec(`INSERT INTO workspaces (name, path, created_at) VALUES (?, ?, ?)`, name, path, now())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) Workspaces() ([]Workspace, error) {
	rows, err := s.db.Query(`SELECT id, name, path FROM workspaces ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Path); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Assign puts a session in a workspace (0: none). Not manual: only where it was not dragged.
func (s *Store) Assign(session string, workspace int64, manual bool) error {
	var ws any
	if workspace != 0 {
		ws = workspace
	}
	return s.Tx(func(tx *sql.Tx) error {
		var was bool
		err := tx.QueryRow(`SELECT manual FROM assignments WHERE session = ?`, session).Scan(&was)
		if err == nil && was && !manual {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(`INSERT INTO assignments (session, workspace, manual, at) VALUES (?, ?, ?, ?)
			ON CONFLICT (session) DO UPDATE SET workspace = excluded.workspace, manual = excluded.manual, at = excluded.at`, session, ws, manual, now())
		return err
	})
}

// Settings are a session's own, over the config's. IdleReap nil: the config's.
type Settings struct {
	IdleReap *time.Duration
	Model    string
	Mode     string
	Effort   string
}

func (s *Store) Settings(session string) (Settings, error) {
	var st Settings
	var reap sql.NullInt64
	var model, mode, effort sql.NullString
	err := s.db.QueryRow(`SELECT idle_reap, model, mode, effort FROM session_settings WHERE session = ?`, session).Scan(&reap, &model, &mode, &effort)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if reap.Valid {
		d := time.Duration(reap.Int64) * time.Second
		st.IdleReap = &d
	}
	st.Model, st.Mode, st.Effort = model.String, mode.String, effort.String
	return st, err
}

func (s *Store) SetSettings(session string, st Settings) error {
	var reap any
	if st.IdleReap != nil {
		reap = int64(*st.IdleReap / time.Second)
	}
	_, err := s.db.Exec(`INSERT INTO session_settings (session, idle_reap, model, mode, effort) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (session) DO UPDATE SET idle_reap = excluded.idle_reap, model = excluded.model, mode = excluded.mode, effort = excluded.effort`,
		session, reap, nullable(st.Model), nullable(st.Mode), nullable(st.Effort))
	return err
}

// UpdateSettings reads a session's settings, changes them with f and writes them back.
func (s *Store) UpdateSettings(session string, f func(*Settings)) error {
	st, err := s.Settings(session)
	if err != nil {
		return err
	}
	f(&st)
	return s.SetSettings(session, st)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- the queue ----

type Input struct {
	ID       string
	Session  string
	Terminal string // who sent it: the interactive terminal's ID
	Tag      string // the source it is prefixed with, "[Tag] ": the terminal's name then; "" is Terminal
	Text     string
	State    string // queued, paused, sent
	Created  int64
}

func (s *Store) AddInput(in Input) error {
	if in.Created == 0 {
		in.Created = now()
	}
	var sent any
	if in.State == "sent" {
		sent = in.Created
	}
	_, err := s.db.Exec(`INSERT INTO inputs (id, session, terminal, tag, text, state, created_at, sent_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Session, in.Terminal, in.Tag, in.Text, in.State, in.Created, sent)
	return err
}

func (s *Store) SetInputState(id, state string) error {
	var sent any
	if state == "sent" {
		sent = now()
	}
	_, err := s.db.Exec(`UPDATE inputs SET state = ?, sent_at = COALESCE(?, sent_at) WHERE id = ?`, state, sent, id)
	return err
}

// Source is who sent input id, if Iridium wrote it: the terminal's ID and the tag the message was
// prefixed with.
func (s *Store) Source(id string) (terminal, tag string, ok bool) {
	if s.db.QueryRow(`SELECT terminal, IIF(tag = '', terminal, tag) FROM inputs WHERE id = ?`, id).Scan(&terminal, &tag) != nil {
		return "", "", false
	}
	return terminal, tag, true
}

// Queued is the session's inputs not sent yet, oldest first.
func (s *Store) Queued(session string) ([]Input, error) {
	rows, err := s.db.Query(`SELECT id, session, terminal, tag, text, state, created_at FROM inputs WHERE session = ? AND state != 'sent' ORDER BY created_at, rowid`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Input
	for rows.Next() {
		var in Input
		if err := rows.Scan(&in.ID, &in.Session, &in.Terminal, &in.Tag, &in.Text, &in.State, &in.Created); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ---- usage ----

type Usage struct {
	At         int64
	Terminal   string // the interactive terminal's ID
	Session    string
	Agent      string
	Model      string
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	CostUSD    *float64
}

func (s *Store) AddUsage(u Usage) error {
	if u.At == 0 {
		u.At = now()
	}
	_, err := s.db.Exec(`INSERT INTO usage (at, terminal, session, agent, model, input, output, cache_read, cache_write, cost_usd) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.At, u.Terminal, u.Session, u.Agent, u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite, u.CostUSD)
	return err
}

// UsageSince is every usage row since at (ms), oldest first.
func (s *Store) UsageSince(at int64) ([]Usage, error) {
	rows, err := s.db.Query(`SELECT at, terminal, session, agent, model, input, output, cache_read, cache_write, cost_usd FROM usage WHERE at >= ? ORDER BY at, id`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		var u Usage
		var cost sql.NullFloat64
		if err := rows.Scan(&u.At, &u.Terminal, &u.Session, &u.Agent, &u.Model, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &cost); err != nil {
			return nil, err
		}
		if cost.Valid {
			u.CostUSD = &cost.Float64
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ---- the plugin outbox ----

type Outgoing struct {
	Seq    int64
	Plugin string
	Chat   string
	Type   string
	Data   string // JSON
	At     int64
}

// Post appends a plugin event and gives its sequence number.
func (s *Store) Post(plugin, chat, typ, data string) (int64, error) {
	r, err := s.db.Exec(`INSERT INTO outbox (plugin, chat, type, data, at) VALUES (?, ?, ?, ?, ?)`, plugin, chat, typ, data, now())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// OutboxAfter is the plugin's events after seq (its Last-Event-ID), oldest first.
func (s *Store) OutboxAfter(plugin string, seq int64) ([]Outgoing, error) {
	rows, err := s.db.Query(`SELECT seq, plugin, chat, type, data, at FROM outbox WHERE plugin = ? AND seq > ? ORDER BY seq`, plugin, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outgoing
	for rows.Next() {
		var o Outgoing
		if err := rows.Scan(&o.Seq, &o.Plugin, &o.Chat, &o.Type, &o.Data, &o.At); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// PruneOutbox drops events older than keep.
func (s *Store) PruneOutbox(keep time.Duration) error {
	_, err := s.db.Exec(`DELETE FROM outbox WHERE at < ?`, now()-keep.Milliseconds())
	return err
}
