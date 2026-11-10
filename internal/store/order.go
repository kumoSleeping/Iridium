package store

import "database/sql"

// The list's order by hand (the user, 2026-11-09): groups' rows are dragged among themselves; a folder
// among those in its group, pinned ones apart from the rest; a session among those beside it, pinned ones apart from the rest. A
// session or folder dragged keeps the key it was dropped at in place of its last activity, so it
// stays where it was put while the others are still sorted by when they last worked.

// SetOrder sets the key session is sorted by; 0 takes it off (its last activity again).
func (s *Store) SetOrder(session string, at float64) error {
	if at == 0 {
		_, err := s.db.Exec(`DELETE FROM session_order WHERE session = ?`, session)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO session_order (session, at) VALUES (?, ?)
		ON CONFLICT (session) DO UPDATE SET at = excluded.at`, session, at)
	return err
}

// SetGroupOrder lists the groups' rows of keys (api.GroupOrder) in that order, in place of the
// order there was.
func (s *Store) SetGroupOrder(keys []string) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM group_order`); err != nil {
			return err
		}
		for i, k := range keys {
			if _, err := tx.Exec(`INSERT INTO group_order (key, sort) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, k, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// GroupOrder is the groups' rows dragged, by key, first to last.
func (s *Store) GroupOrder() ([]string, error) {
	rows, err := s.db.Query(`SELECT key FROM group_order ORDER BY sort`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// SetFolderOrder sets the key the folder of path (as grouped: remote.GroupPath) is sorted by among
// those in its group; 0 takes it off (its latest session's activity again).
func (s *Store) SetFolderOrder(path string, at float64) error {
	if at == 0 {
		_, err := s.db.Exec(`DELETE FROM folder_order WHERE path = ?`, path)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO folder_order (path, at) VALUES (?, ?)
		ON CONFLICT (path) DO UPDATE SET at = excluded.at`, path, at)
	return err
}

// FolderOrder is the key of every folder dragged, by path.
func (s *Store) FolderOrder() (map[string]float64, error) {
	rows, err := s.db.Query(`SELECT path, at FROM folder_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var p string
		var at float64
		if err := rows.Scan(&p, &at); err != nil {
			return nil, err
		}
		out[p] = at
	}
	return out, rows.Err()
}

// SetFolderPinned pins the folder of path among those in its group, or unpins it.
func (s *Store) SetFolderPinned(path string, on bool) error {
	if !on {
		_, err := s.db.Exec(`DELETE FROM folder_pins WHERE path = ?`, path)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO folder_pins (path, at) VALUES (?, ?) ON CONFLICT (path) DO NOTHING`, path, now())
	return err
}

// FolderPins is the folders pinned, by path, in the order they were pinned.
func (s *Store) FolderPins() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM folder_pins ORDER BY at, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SplitWorkspace marks workspace id split (flat: it holds only the sessions right in its folder)
// and takes the sessions of ids out of it, to none, as discovery had them, so it groups them again
// by their folders.
func (s *Store) SplitWorkspace(id int64, ids []string) error {
	return s.Tx(func(tx *sql.Tx) error {
		r, err := tx.Exec(`UPDATE workspaces SET flat = 1 WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoWorkspace
		}
		for _, sid := range ids {
			if _, err := tx.Exec(`UPDATE assignments SET workspace = NULL, manual = 0, at = ? WHERE session = ? AND workspace = ?`, now(), sid, id); err != nil {
				return err
			}
		}
		return nil
	})
}
