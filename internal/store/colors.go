package store



// colorsSchema is migration 8: the colour tags given from a row's menu (api.Color), a session's by
// its id, a folder's by its absolute path.
const colorsSchema = `CREATE TABLE session_colors (session TEXT PRIMARY KEY, color TEXT NOT NULL);
	CREATE TABLE folder_colors (path TEXT PRIMARY KEY, color TEXT NOT NULL);`

// SetColor tags a session with a colour; "" takes it off.
func (s *Store) SetColor(session, color string) error {
	return s.setColor("session_colors", "session", session, color)
}

// SetFolderColor tags a folder with a colour; "" takes it off.
func (s *Store) SetFolderColor(path, color string) error {
	return s.setColor("folder_colors", "path", path, color)
}

func (s *Store) setColor(table, key, at, color string) error {
	if color == "" {
		_, err := s.db.Exec(`DELETE FROM `+table+` WHERE `+key+` = ?`, at)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO `+table+` (`+key+`, color) VALUES (?, ?) ON CONFLICT (`+key+`) DO UPDATE SET color = excluded.color`, at, color)
	return err
}

// FolderColors is every folder's colour tag, by path.
func (s *Store) FolderColors() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT path, color FROM folder_colors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, c string
		if err := rows.Scan(&p, &c); err != nil {
			return nil, err
		}
		out[p] = c
	}
	return out, rows.Err()
}

