package store

import "database/sql"

// Follows is every Bot's panels, by Bot.
func (s *Store) Follows() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT bot, panel FROM bot_follows ORDER BY bot, panel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var bot, panel string
		if err := rows.Scan(&bot, &panel); err != nil {
			return nil, err
		}
		out[bot] = append(out[bot], panel)
	}
	return out, rows.Err()
}

// Followers is the Bots that follow panel.
func (s *Store) Followers(panel string) ([]string, error) {
	rows, err := s.db.Query(`SELECT bot FROM bot_follows WHERE panel = ? ORDER BY bot`, panel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var bot string
		if err := rows.Scan(&bot); err != nil {
			return nil, err
		}
		out = append(out, bot)
	}
	return out, rows.Err()
}

// SetFollows puts panels in place of the ones bot follows.
func (s *Store) SetFollows(bot string, panels []string) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM bot_follows WHERE bot = ?`, bot); err != nil {
			return err
		}
		for _, p := range panels {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO bot_follows (bot, panel) VALUES (?, ?)`, bot, p); err != nil {
				return err
			}
		}
		return nil
	})
}
