// Package store is Iridium's database: SQLite through a pure-Go driver, WAL, writes in BEGIN
// IMMEDIATE transactions. It holds only what lies over the sessions found on disk (plan "数据"):
// workspaces and who belongs where, pins, colour tags, which sessions are shown, names, the queue of messages to send, push
// subscriptions, interactive terminals and their permissions, usage, the plugin event outbox, and
// the credentials, sealed (secrets.go). Never the conversations themselves, nor asks.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// 5: owner invites, and Claude for the first panel once (terminals.go).
	ownerSchema,
	// 6: a session's ultracode (Claude); NULL: never set, the user's own settings hold.
	`ALTER TABLE session_settings ADD COLUMN ultracode INTEGER;`,
	// 7: the device a terminal is on (terminals.go).
	devicesSchema,
	// 8: colour tags on sessions and folders (colors.go).
	colorsSchema,
	// 9: push rules on workspaces, folders and sessions in place of push settings by workspace
	// (pushrules.go).
	pushRulesSchema,
	// 10: credentials and their uses (secrets.go).
	secretsSchema,
	// 11: auto: a name Iridium gave (the folder's), standing only till the session has a title.
	`ALTER TABLE names ADD COLUMN auto INTEGER NOT NULL DEFAULT 0;`,
	// 12: a session's agent preset (dsh's tools and prompt); NULL: the agent's default.
	`ALTER TABLE session_settings ADD COLUMN preset TEXT;`,
	// 13: who made a workspace (a terminal's ID); unused since workspaces are folder groups
	// (2026-11-08), kept as the schema only grows.
	`ALTER TABLE workspaces ADD COLUMN creator TEXT;`,
	// 14: when a chat window last said something with a session in it (more.go: Seen), for what
	// reaches the user through a bot.
	`CREATE TABLE chat_seen (
		plugin TEXT NOT NULL,
		chat TEXT NOT NULL,
		session TEXT NOT NULL,
		at INTEGER NOT NULL,
		PRIMARY KEY (plugin, chat, session)
	);`,
	// 15: what each executor last said of itself (machines.go).
	machinesSchema,
	// 16: a workspace hidden from the page (the user, 2026-11-08): listed only while a session in
	// it runs elsewhere (DSH Desktop).
	`ALTER TABLE workspaces ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;`,
	// 17: no placeholder names (the user, 2026-11-08): auto marks a name the session's agent gave
	// (ir rename) from now on.
	`DELETE FROM names WHERE auto = 1;`,
	// 18: notes and their folders (notes.go).
	notesSchema,
	// 19: the root folder a note serves (the user, 2026-11-09): the composer lists those of its
	// session's; "" (older notes) every one's.
	`ALTER TABLE notes ADD COLUMN root TEXT NOT NULL DEFAULT '';`,
}

// steps run in Go after the migration of their version, in the same transaction.
var steps = map[int]func(*sql.Tx) error{5: claudeForFirstPanel}

type Store struct {
	db *sql.DB
	// keyPath is the secrets' key file, by the database (secrets.go); key, once read or made.
	keyPath string
	kmu     sync.Mutex
	key     []byte
}

// Open opens (and makes, or migrates) the database at path. The file and its -wal and -shm are
// the owner's alone (0600): they hold the terminals' token hashes and passkeys.
func Open(path string) (*Store, error) {
	if err := private(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, keyPath: filepath.Join(filepath.Dir(path), "secrets.key")}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	return s, nil
}

// private makes the database file 0600 before SQLite opens it (SQLite gives the -wal and -shm it
// makes the database's mode), and brings an older one, and its -wal and -shm, down to 0600.
func private(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
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
			if step := steps[v+1]; step != nil {
				if err := step(tx); err != nil {
					return fmt.Errorf("migration %d: %w", v+1, err)
				}
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
	Name string
	// AgentNamed: Name is the one the session's agent gave (SetAgentName), not the user's.
	AgentNamed bool
	Pinned     bool
	Workspace  int64
	Manual     bool
	Color      string
}

// Overlays is every session's overlay, by session id.
func (s *Store) Overlays() (map[string]Overlay, error) {
	rows, err := s.db.Query(`
		SELECT session, MAX(name), MAX(auto), MAX(pinned), MAX(workspace), MAX(manual), MAX(color) FROM (
			SELECT session, name, auto, 0 AS pinned, 0 AS workspace, 0 AS manual, '' AS color FROM names
			UNION ALL SELECT session, '', 0, 1, 0, 0, '' FROM pins
			UNION ALL SELECT session, '', 0, 0, IFNULL(workspace, 0), manual, '' FROM assignments
			UNION ALL SELECT session, '', 0, 0, 0, 0, color FROM session_colors
		) GROUP BY session`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Overlay{}
	for rows.Next() {
		var id string
		var o Overlay
		if err := rows.Scan(&id, &o.Name, &o.AgentNamed, &o.Pinned, &o.Workspace, &o.Manual, &o.Color); err != nil {
			return nil, err
		}
		out[id] = o
	}
	return out, rows.Err()
}

// SetName renames a session as the user does; "" takes the name off.
func (s *Store) SetName(session, name string) error {
	if name == "" {
		_, err := s.db.Exec(`DELETE FROM names WHERE session = ?`, session)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO names (session, name, auto) VALUES (?, ?, 0)
		ON CONFLICT (session) DO UPDATE SET name = excluded.name, auto = 0`, session, name)
	return err
}

// ErrUserNamed: the session has a name the user gave, which its agent's does not replace.
var ErrUserNamed = errors.New("用户起的名，不改")

// SetAgentName gives a session the name its agent chose (ir rename): over its title, under a name
// the user gave (ErrUserNamed).
func (s *Store) SetAgentName(session, name string) error {
	r, err := s.db.Exec(`INSERT INTO names (session, name, auto) VALUES (?, ?, 1)
		ON CONFLICT (session) DO UPDATE SET name = excluded.name WHERE names.auto = 1`, session, name)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err == nil && n == 0 {
		return ErrUserNamed
	}
	return nil
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
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}

func (s *Store) AddWorkspace(name, path string) (int64, error) {
	r, err := s.db.Exec(`INSERT INTO workspaces (name, path, created_at) VALUES (?, ?, ?)`, name, path, now())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) Workspaces() ([]Workspace, error) {
	rows, err := s.db.Query(`SELECT id, name, path, hidden FROM workspaces ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Path, &w.Hidden); err != nil {
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

// Settings are a session's own, over the config's. IdleReap nil: the config's. Ultracode nil:
// never set. Preset: the agent preset (dsh); empty: the agent's default.
type Settings struct {
	IdleReap  *time.Duration
	Model     string
	Mode      string
	Effort    string
	Ultracode *bool
	Preset    string
}

func (s *Store) Settings(session string) (Settings, error) {
	var st Settings
	var reap sql.NullInt64
	var model, mode, effort, preset sql.NullString
	var ultra sql.NullBool
	err := s.db.QueryRow(`SELECT idle_reap, model, mode, effort, ultracode, preset FROM session_settings WHERE session = ?`, session).Scan(&reap, &model, &mode, &effort, &ultra, &preset)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if reap.Valid {
		d := time.Duration(reap.Int64) * time.Second
		st.IdleReap = &d
	}
	st.Model, st.Mode, st.Effort, st.Preset = model.String, mode.String, effort.String, preset.String
	if ultra.Valid {
		st.Ultracode = &ultra.Bool
	}
	return st, err
}

func (s *Store) SetSettings(session string, st Settings) error {
	var reap any
	if st.IdleReap != nil {
		reap = int64(*st.IdleReap / time.Second)
	}
	var ultra any
	if st.Ultracode != nil {
		ultra = *st.Ultracode
	}
	_, err := s.db.Exec(`INSERT INTO session_settings (session, idle_reap, model, mode, effort, ultracode, preset) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session) DO UPDATE SET idle_reap = excluded.idle_reap, model = excluded.model, mode = excluded.mode, effort = excluded.effort, ultracode = excluded.ultracode, preset = excluded.preset`,
		session, reap, nullable(st.Model), nullable(st.Mode), nullable(st.Effort), ultra, nullable(st.Preset))
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

// UsageDay is the usage of one local day, terminal, agent and model.
type UsageDay struct {
	Day        string // "2006-01-02", local time
	Terminal   string // "" when who began the turns is not known
	Agent      string
	Model      string
	Turns      int // rows: one per turn and model
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	CostUSD    float64 // of the rows that have one
}

// UsageByDay sums the usage since at (ms) by local day, terminal, agent and model, oldest day
// first; terminals given, only theirs.
func (s *Store) UsageByDay(at int64, terminals ...string) ([]UsageDay, error) {
	return s.UsageBy(at, func(t time.Time) string { return t.Format(time.DateOnly) }, terminals...)
}

// UsageBy is UsageByDay with the rows put in bucket's keys (UsageDay.Day) rather than days.
func (s *Store) UsageBy(at int64, bucket func(time.Time) string, terminals ...string) ([]UsageDay, error) {
	q := `SELECT at, terminal, agent, model, input, output, cache_read, cache_write, cost_usd FROM usage WHERE at >= ?`
	args := []any{at}
	if len(terminals) > 0 {
		q += ` AND terminal IN (?` + strings.Repeat(`, ?`, len(terminals)-1) + `)`
		for _, t := range terminals {
			args = append(args, t)
		}
	}
	rows, err := s.db.Query(q+` ORDER BY at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ day, terminal, agent, model string }
	index := map[key]int{}
	var out []UsageDay
	for rows.Next() {
		var ms int64
		var k key
		var in, o, cr, cw int64
		var cost sql.NullFloat64
		if err := rows.Scan(&ms, &k.terminal, &k.agent, &k.model, &in, &o, &cr, &cw, &cost); err != nil {
			return nil, err
		}
		k.day = bucket(time.UnixMilli(ms))
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, UsageDay{Day: k.day, Terminal: k.terminal, Agent: k.agent, Model: k.model})
		}
		d := &out[i]
		d.Turns++
		d.Input += in
		d.Output += o
		d.CacheRead += cr
		d.CacheWrite += cw
		if cost.Valid {
			d.CostUSD += cost.Float64
		}
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
