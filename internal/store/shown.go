package store

import (
	"database/sql"
	"errors"
	"strconv"
)

// The shown marker (the user's decision, 2026-11-05): a session is in the list only while it
// carries one, keyed by its agent and the agent's own session id. Iridium sets it when it hosts
// a session or the session shows activity after Iridium began watching; archiving takes it off
// and notes how far the transcript went, so that only growth past that brings it back. Never
// anything in the agents' own files.

// shownSchema is migration 4: the markers and a small key-value table (when watching began).
// The old archive flag becomes a hidden marker of no known agent, made exact when the session is
// next seen.
const shownSchema = `CREATE TABLE shown (
		agent TEXT NOT NULL,
		session TEXT NOT NULL,
		shown INTEGER NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		quiet_until INTEGER NOT NULL DEFAULT 0,
		at INTEGER NOT NULL,
		PRIMARY KEY (agent, session)
	);
	INSERT INTO shown (agent, session, shown, size, at) SELECT '', session, 0, -1, at FROM archive;
	DROP TABLE archive;
	CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`

// Shown is a session's marker. Hidden ones keep Size, the transcript's size when hidden, and
// QuietUntil (ms): growth before then is its agent ending, not activity. Legacy: hidden by the
// old archive flag, its agent and size not known yet.
type Shown struct {
	Shown      bool
	Size       int64
	QuietUntil int64
	Legacy     bool
}

// Visibility is every marker, by agent and session.
type Visibility map[[2]string]Shown

// Row is the marker of agent's session, ok false if none (a legacy one counts).
func (v Visibility) Row(agent, session string) (Shown, bool) {
	if r, ok := v[[2]string{agent, session}]; ok {
		return r, true
	}
	r, ok := v[[2]string{"", session}]
	return r, ok
}

// Shown says whether agent's session carries the marker.
func (v Visibility) Shown(agent, session string) bool {
	r, ok := v.Row(agent, session)
	return ok && r.Shown
}

// Visibility reads every marker.
func (s *Store) Visibility() (Visibility, error) {
	rows, err := s.db.Query(`SELECT agent, session, shown, size, quiet_until FROM shown`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := Visibility{}
	for rows.Next() {
		var a, id string
		var r Shown
		if err := rows.Scan(&a, &id, &r.Shown, &r.Size, &r.QuietUntil); err != nil {
			return nil, err
		}
		r.Legacy = a == ""
		out[[2]string{a, id}] = r
	}
	return out, rows.Err()
}

// ShownRow is the marker of agent's session; ok false if it has none.
func (s *Store) ShownRow(agent, session string) (Shown, bool, error) {
	var r Shown
	var a string
	err := s.db.QueryRow(`SELECT agent, shown, size, quiet_until FROM shown WHERE session = ? AND agent IN (?, '') ORDER BY agent DESC LIMIT 1`, session, agent).
		Scan(&a, &r.Shown, &r.Size, &r.QuietUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	r.Legacy = a == ""
	return r, err == nil, err
}

// Show sets the marker of agent's session; changed: it was not shown.
func (s *Store) Show(agent, session string) (changed bool, err error) {
	err = s.Tx(func(tx *sql.Tx) error {
		var on bool
		e := tx.QueryRow(`SELECT shown FROM shown WHERE agent = ? AND session = ?`, agent, session).Scan(&on)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		changed = !on
		if _, e := tx.Exec(`DELETE FROM shown WHERE agent = '' AND session = ?`, session); e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO shown (agent, session, shown, size, quiet_until, at) VALUES (?, ?, 1, 0, 0, ?)
			ON CONFLICT (agent, session) DO UPDATE SET shown = 1, quiet_until = 0, at = excluded.at`, agent, session, now())
		return e
	})
	return changed, err
}

// Hide takes the marker of agent's session off, noting the transcript's size; growth past it
// before quietUntil (ms, 0 for none) does not show it again.
func (s *Store) Hide(agent, session string, size, quietUntil int64) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM shown WHERE agent = '' AND session = ?`, session); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO shown (agent, session, shown, size, quiet_until, at) VALUES (?, ?, 0, ?, ?, ?)
			ON CONFLICT (agent, session) DO UPDATE SET shown = 0, size = excluded.size, quiet_until = excluded.quiet_until, at = excluded.at`, agent, session, size, quietUntil, now())
		return err
	})
}

// WatchingSince is when Iridium first watched the agents' transcripts (ms): set now, the first
// time it is asked for. What was written before and not since is not activity.
func (s *Store) WatchingSince() (int64, error) {
	var at int64
	err := s.Tx(func(tx *sql.Tx) error {
		var v string
		err := tx.QueryRow(`SELECT value FROM meta WHERE key = 'watching_since'`).Scan(&v)
		if err == nil {
			at, err = strconv.ParseInt(v, 10, 64)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		at = now()
		_, err = tx.Exec(`INSERT INTO meta (key, value) VALUES ('watching_since', ?)`, strconv.FormatInt(at, 10))
		return err
	})
	return at, err
}
