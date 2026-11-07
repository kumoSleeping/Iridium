package store

import "database/sql"

// pushRulesSchema is migration 9: Web Push's rules (api.PushRule), the default events (scope
// default, target "") and what a workspace (target its id, "0" sessions in none), a folder (its
// absolute path) or a session (its id) says over them: off, or on with its own events (NULL: the
// ones from above). No row: as above. push_settings' rows become workspace rules: none of its
// events is off, the defaults' are dropped.
const pushRulesSchema = `CREATE TABLE push_rules (
		scope TEXT NOT NULL,
		target TEXT NOT NULL,
		off INTEGER NOT NULL DEFAULT 0,
		needs_you INTEGER,
		error INTEGER,
		turn_end INTEGER,
		compact INTEGER,
		PRIMARY KEY (scope, target)
	);
	INSERT INTO push_rules (scope, target, off, needs_you, error, turn_end, compact)
		SELECT 'workspace', CAST(workspace AS TEXT), NOT (needs_you OR error OR turn_end OR compact),
			IIF(needs_you OR error OR turn_end OR compact, needs_you, NULL),
			IIF(needs_you OR error OR turn_end OR compact, error, NULL),
			IIF(needs_you OR error OR turn_end OR compact, turn_end, NULL),
			IIF(needs_you OR error OR turn_end OR compact, compact, NULL)
		FROM push_settings WHERE NOT (needs_you AND error AND NOT turn_end AND NOT compact);
	DROP TABLE push_settings;`

// PushEvents is what is pushed. A compaction you asked for is a turn's end; the agent's own is not
// pushed (the user, 2026-11-08): the compact column is no longer read or written.
type PushEvents struct {
	NeedsYou, Error, TurnEnd bool
}

// DefaultPushEvents: all on (the user, 2026-11-08).
var DefaultPushEvents = PushEvents{NeedsYou: true, Error: true, TurnEnd: true}

// The scopes of a push rule.
const (
	PushDefault   = "default"
	PushWorkspace = "workspace"
	PushFolder    = "folder"
	PushSession   = "session"
)

// PushRule is a scope's say: Off, or on with Events (nil: the ones from above).
type PushRule struct {
	Scope, Target string
	Off           bool
	Events        *PushEvents
}

// PushRules is every rule there is, the defaults' among them if they were set.
func (s *Store) PushRules() ([]PushRule, error) {
	rows, err := s.db.Query(`SELECT scope, target, off, needs_you, error, turn_end FROM push_rules ORDER BY scope, target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushRule
	for rows.Next() {
		var r PushRule
		var n, e, t sql.NullBool
		if err := rows.Scan(&r.Scope, &r.Target, &r.Off, &n, &e, &t); err != nil {
			return nil, err
		}
		if n.Valid {
			r.Events = &PushEvents{NeedsYou: n.Bool, Error: e.Bool, TurnEnd: t.Bool}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPushRule keeps r in place of the scope's rule before.
func (s *Store) SetPushRule(r PushRule) error {
	var n, e, t sql.NullBool
	if r.Events != nil {
		n, e, t = sql.NullBool{Bool: r.Events.NeedsYou, Valid: true}, sql.NullBool{Bool: r.Events.Error, Valid: true}, sql.NullBool{Bool: r.Events.TurnEnd, Valid: true}
	}
	_, err := s.db.Exec(`INSERT INTO push_rules (scope, target, off, needs_you, error, turn_end) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, target) DO UPDATE SET off = excluded.off, needs_you = excluded.needs_you, error = excluded.error, turn_end = excluded.turn_end`,
		r.Scope, r.Target, r.Off, n, e, t)
	return err
}

// DeletePushRule drops the scope's rule: it is as above again.
func (s *Store) DeletePushRule(scope, target string) error {
	_, err := s.db.Exec(`DELETE FROM push_rules WHERE scope = ? AND target = ?`, scope, target)
	return err
}
