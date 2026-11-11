package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/kumoSleeping/iridium/internal/gate"
)

// An app's logins other than ir_web's (decision 12): a login an app speaks as the first time is
// kept waiting (approved_at NULL) till a person approves it; removed, it asks again as it next
// speaks. ir_web's are the gate's (apps.go).

// AppLogin is a login of an app other than ir_web.
type AppLogin struct {
	App        string
	ID         string
	Name       string
	CreatedAt  time.Time
	ApprovedAt time.Time // zero: waiting
	LastActive time.Time
}

// Login is login id of app, if there is one.
func (s *Store) Login(app, id string) (AppLogin, bool, error) {
	l := AppLogin{App: app, ID: id}
	var created, active int64
	var approved sql.NullInt64
	err := s.db.QueryRow(`SELECT name, created_at, approved_at, last_active FROM logins WHERE app = ? AND id = ?`, app, id).
		Scan(&l.Name, &created, &approved, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}
	l.CreatedAt, l.LastActive = fromNanos(created), fromNanos(active)
	if approved.Valid {
		l.ApprovedAt = fromNanos(approved.Int64)
	}
	return l, true, nil
}

// Logins lists the logins of the apps other than ir_web, waiting ones too, oldest first.
func (s *Store) Logins() ([]AppLogin, error) {
	rows, err := s.db.Query(`SELECT app, id, name, created_at, IFNULL(approved_at, 0), last_active FROM logins
		WHERE app <> ? ORDER BY created_at, app, id`, gate.WebApp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppLogin
	for rows.Next() {
		var l AppLogin
		var created, approved, active int64
		if err := rows.Scan(&l.App, &l.ID, &l.Name, &created, &approved, &active); err != nil {
			return nil, err
		}
		l.CreatedAt, l.ApprovedAt, l.LastActive = fromNanos(created), fromNanos(approved), fromNanos(active)
		out = append(out, l)
	}
	return out, rows.Err()
}

// AddLogin keeps login id of app waiting, unless the app has max waiting already (false). One
// there already stays as it is (true).
func (s *Store) AddLogin(app, id, name string, max int, now time.Time) (bool, error) {
	added := false
	err := s.Tx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM logins WHERE app = ? AND approved_at IS NULL AND id <> ?`, app, id).Scan(&n); err != nil {
			return err
		}
		var there int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM logins WHERE app = ? AND id = ?`, app, id).Scan(&there); err != nil {
			return err
		}
		if there > 0 {
			added = true
			return nil
		}
		if n >= max {
			return nil
		}
		_, err := tx.Exec(`INSERT INTO logins (app, id, name, created_at) VALUES (?, ?, ?, ?)`, app, id, name, nanos(now))
		added = err == nil
		return err
	})
	return added, err
}

// AddApprovedLogin keeps login id of app approved, as a person allowed it with its app (pairing):
// true if it is new; one there already stays as it is.
func (s *Store) AddApprovedLogin(app, id, name string, now time.Time) (bool, error) {
	if app == gate.WebApp {
		return false, nil
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO logins (app, id, name, created_at, approved_at) VALUES (?, ?, ?, ?, ?)`, app, id, name, nanos(now), nanos(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ApproveLogin approves login id of app: false if there is none.
func (s *Store) ApproveLogin(app, id string, now time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE logins SET approved_at = ? WHERE app = ? AND id = ? AND app <> ?`, nanos(now), app, id, gate.WebApp)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteLogin removes login id of app (not ir_web's: those are the gate's): false if there was none.
func (s *Store) DeleteLogin(app, id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM logins WHERE app = ? AND id = ? AND app <> ?`, app, id, gate.WebApp)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// TouchLogin sets when login id of app last spoke.
func (s *Store) TouchLogin(app, id string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE logins SET last_active = ? WHERE app = ? AND id = ?`, nanos(now), app, id)
	return err
}

// AppPerms is what app id was given (the server's JSON), "" for all: an app paired before 0.2.
func (s *Store) AppPerms(id string) ([]byte, error) {
	var p string
	err := s.db.QueryRow(`SELECT perms FROM apps WHERE id = ?`, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) || p == "" {
		return nil, nil
	}
	return []byte(p), err
}

// SetAppPerms sets what app id was given.
func (s *Store) SetAppPerms(id string, perms []byte) error {
	_, err := s.db.Exec(`UPDATE apps SET perms = ? WHERE id = ?`, string(perms), id)
	return err
}

// LoginPermsOf is what login id of app was given (the server's JSON), "" for all.
func (s *Store) LoginPermsOf(app, id string) ([]byte, error) {
	var p string
	err := s.db.QueryRow(`SELECT perms FROM logins WHERE app = ? AND id = ?`, app, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) || p == "" {
		return nil, nil
	}
	return []byte(p), err
}

// SetLoginPermsOf sets what login id of app was given ("" for all): false if there is none.
func (s *Store) SetLoginPermsOf(app, id string, perms []byte) (bool, error) {
	res, err := s.db.Exec(`UPDATE logins SET perms = ? WHERE app = ? AND id = ?`, string(perms), app, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// LoginPerms is what login id of ir_web was given (the server's JSON), "" for all.
func (s *Store) LoginPerms(id string) ([]byte, error) {
	var p string
	err := s.db.QueryRow(`SELECT perms FROM logins WHERE app = ? AND id = ?`, gate.WebApp, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) || p == "" {
		return nil, nil
	}
	return []byte(p), err
}

// SetLoginPerms sets what login id of ir_web was given ("" for all).
func (s *Store) SetLoginPerms(id string, perms []byte) error {
	_, err := s.db.Exec(`UPDATE logins SET perms = ? WHERE app = ? AND id = ?`, string(perms), gate.WebApp, id)
	return err
}
