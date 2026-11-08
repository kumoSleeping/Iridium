package store

import "database/sql"

// machinesSchema is migration 15: what each executor (docs/exec.md) last said of itself when it
// connected (JSON, the API's Machine without its state), so the page lists it while it is away.
const machinesSchema = `CREATE TABLE machines (
		terminal TEXT PRIMARY KEY,
		info TEXT NOT NULL,
		at INTEGER NOT NULL
	);`

// SetMachine stores what terminal's executor said of itself.
func (s *Store) SetMachine(terminal string, info []byte) error {
	_, err := s.db.Exec(`INSERT INTO machines (terminal, info, at) VALUES (?, ?, ?) ON CONFLICT (terminal) DO UPDATE SET info = excluded.info, at = excluded.at`, terminal, string(info), now())
	return err
}

// MachineRow is a stored executor: its terminal, what it said, when (ms).
type MachineRow struct {
	Terminal string
	Info     []byte
	At       int64
}

// Machines is every executor that has connected, by terminal.
func (s *Store) Machines() ([]MachineRow, error) {
	rows, err := s.db.Query(`SELECT terminal, info, at FROM machines`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MachineRow
	for rows.Next() {
		var r MachineRow
		var info string
		if err := rows.Scan(&r.Terminal, &info, &r.At); err != nil {
			return nil, err
		}
		r.Info = []byte(info)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ForgetMachine forgets terminal's executor (its terminal was removed).
func (s *Store) ForgetMachine(terminal string) error {
	_, err := s.db.Exec(`DELETE FROM machines WHERE terminal = ?`, terminal)
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}
