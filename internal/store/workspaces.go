package store

import (
	"database/sql"
	"errors"
	"strings"
)

// ErrNoWorkspace: no workspace of that id.
var ErrNoWorkspace = errors.New("no such workspace")

// ErrWorkspacePath: a workspace of that folder is there already.
var ErrWorkspacePath = errors.New("a workspace of that folder exists")

// Assignment is where a session was put: Workspace 0 is none (未归类). Manual: the user moved it,
// so discovery never moves it again.
type Assignment struct {
	Workspace int64
	Manual    bool
}

// Assignments is every session that was put somewhere, unassigned ones too: a session with a row
// here has been seen, and is never classified again.
func (s *Store) Assignments() (map[string]Assignment, error) {
	rows, err := s.db.Query(`SELECT session, IFNULL(workspace, 0), manual FROM assignments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Assignment{}
	for rows.Next() {
		var id string
		var a Assignment
		if err := rows.Scan(&id, &a.Workspace, &a.Manual); err != nil {
			return nil, err
		}
		out[id] = a
	}
	return out, rows.Err()
}

// AssignIfNew puts a session seen for the first time in a workspace (0: none). A session with a
// row already is left where it is; ok says whether this wrote one.
func (s *Store) AssignIfNew(session string, workspace int64) (bool, error) {
	var ws any
	if workspace != 0 {
		ws = workspace
	}
	r, err := s.db.Exec(`INSERT INTO assignments (session, workspace, manual, at) VALUES (?, ?, 0, ?) ON CONFLICT (session) DO NOTHING`, session, ws, now())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

// Move is the user moving a session to a workspace (0: none). It sticks: discovery never moves
// the session again.
func (s *Store) Move(session string, workspace int64) error {
	if workspace != 0 {
		if _, err := s.Workspace(workspace); err != nil {
			return err
		}
	}
	return s.Assign(session, workspace, true)
}

// Workspace is the workspace of id, or ErrNoWorkspace.
func (s *Store) Workspace(id int64) (Workspace, error) {
	w := Workspace{ID: id}
	err := s.db.QueryRow(`SELECT name, path FROM workspaces WHERE id = ?`, id).Scan(&w.Name, &w.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNoWorkspace
	}
	return w, err
}

// CreateWorkspace makes a workspace of folder path, or ErrWorkspacePath if there is one.
func (s *Store) CreateWorkspace(name, path string) (int64, error) {
	id, err := s.AddWorkspace(name, path)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return 0, ErrWorkspacePath
	}
	return id, err
}

// SetWorkspaceCreator says which terminal made workspace id.
func (s *Store) SetWorkspaceCreator(id int64, creator string) error {
	_, err := s.db.Exec(`UPDATE workspaces SET creator = NULLIF(?, '') WHERE id = ?`, creator, id)
	return err
}

// ClaimWorkspaces gives creator every workspace with none; it gives how many.
func (s *Store) ClaimWorkspaces(creator string) (int64, error) {
	r, err := s.db.Exec(`UPDATE workspaces SET creator = ? WHERE creator IS NULL`, creator)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *Store) RenameWorkspace(id int64, name string) error {
	r, err := s.db.Exec(`UPDATE workspaces SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNoWorkspace
	}
	return nil
}

// DeleteWorkspace drops a workspace; its sessions are left with none (未归类), still seen, so
// they are not classified again.
func (s *Store) DeleteWorkspace(id int64) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE assignments SET workspace = NULL WHERE workspace = ?`, id); err != nil {
			return err
		}
		r, err := tx.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoWorkspace
		}
		return nil
	})
}

// Adopt puts the given sessions in workspace, those that are in none and were never moved by
// the user; the others stay. It gives how many moved.
func (s *Store) Adopt(workspace int64, sessions []string) (int, error) {
	n := 0
	err := s.Tx(func(tx *sql.Tx) error {
		for _, id := range sessions {
			r, err := tx.Exec(`INSERT INTO assignments (session, workspace, manual, at) VALUES (?, ?, 0, ?)
				ON CONFLICT (session) DO UPDATE SET workspace = excluded.workspace, at = excluded.at
				WHERE assignments.workspace IS NULL AND assignments.manual = 0`, id, workspace, now())
			if err != nil {
				return err
			}
			k, _ := r.RowsAffected()
			n += int(k)
		}
		return nil
	})
	return n, err
}
