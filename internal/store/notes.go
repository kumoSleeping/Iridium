package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// notesSchema is migration 18: notes (api.Note) and their folders. sessions: a JSON array of the
// sessions a note was put into. A folder deleted leaves its notes in none.
const notesSchema = `CREATE TABLE note_folders (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		pos REAL NOT NULL
	);
	CREATE TABLE notes (
		id TEXT PRIMARY KEY,
		folder INTEGER NOT NULL DEFAULT 0,
		body TEXT NOT NULL,
		pos REAL NOT NULL,
		color TEXT NOT NULL DEFAULT '',
		done_at INTEGER NOT NULL DEFAULT 0,
		from_session TEXT NOT NULL DEFAULT '',
		sessions TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`

// ErrNoNote: no such note, or folder of notes.
var ErrNoNote = errors.New("no such note")

// Notes is every note and folder, by Pos.
func (s *Store) Notes() (api.Notes, error) {
	out := api.Notes{Notes: []api.Note{}, Folders: []api.NoteFolder{}}
	rows, err := s.db.Query(`SELECT id, folder, body, pos, color, done_at, from_session, sessions, created_at, updated_at FROM notes ORDER BY pos, created_at`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n api.Note
		var sessions string
		if err := rows.Scan(&n.ID, &n.Folder, &n.Body, &n.Pos, &n.Color, &n.Done, &n.From, &sessions, &n.Created, &n.Updated); err != nil {
			return out, err
		}
		_ = json.Unmarshal([]byte(sessions), &n.Sessions)
		out.Notes = append(out.Notes, n)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	frows, err := s.db.Query(`SELECT id, name, pos FROM note_folders ORDER BY pos, id`)
	if err != nil {
		return out, err
	}
	defer frows.Close()
	for frows.Next() {
		var f api.NoteFolder
		if err := frows.Scan(&f.ID, &f.Name, &f.Pos); err != nil {
			return out, err
		}
		out.Folders = append(out.Folders, f)
	}
	return out, frows.Err()
}

// Note is note id.
func (s *Store) Note(id string) (api.Note, error) {
	var n api.Note
	var sessions string
	err := s.db.QueryRow(`SELECT id, folder, body, pos, color, done_at, from_session, sessions, created_at, updated_at FROM notes WHERE id = ?`, id).
		Scan(&n.ID, &n.Folder, &n.Body, &n.Pos, &n.Color, &n.Done, &n.From, &sessions, &n.Created, &n.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNoNote
	}
	_ = json.Unmarshal([]byte(sessions), &n.Sessions)
	return n, err
}

// AddNote makes note n (its ID given); Pos nil puts it above every note in its folder. A note of
// that ID already there is left as it is (a page's offline note sent twice).
func (s *Store) AddNote(n api.NewNote) (api.Note, error) {
	now := time.Now().UnixMilli()
	pos := 0.0
	if n.Pos != nil {
		pos = *n.Pos
	} else {
		var least sql.NullFloat64
		if err := s.db.QueryRow(`SELECT MIN(pos) FROM notes WHERE folder = ?`, n.Folder).Scan(&least); err != nil {
			return api.Note{}, err
		}
		if least.Valid {
			pos = least.Float64 - 1
		}
	}
	if _, err := s.db.Exec(`INSERT INTO notes (id, folder, body, pos, color, from_session, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		n.ID, n.Folder, n.Body, pos, string(n.Color), n.From, now, now); err != nil {
		return api.Note{}, err
	}
	return s.Note(n.ID)
}

// PatchNote changes note id as p says.
func (s *Store) PatchNote(id string, p api.NotePatch) (api.Note, error) {
	n, err := s.Note(id)
	if err != nil {
		return n, err
	}
	if p.Body != nil {
		n.Body = *p.Body
	}
	if p.Folder != nil {
		n.Folder = *p.Folder
	}
	if p.Pos != nil {
		n.Pos = *p.Pos
	}
	if p.Color != nil {
		n.Color = *p.Color
	}
	now := time.Now().UnixMilli()
	if p.Done != nil {
		switch {
		case *p.Done && n.Done == 0:
			n.Done = now
		case !*p.Done:
			n.Done = 0
		}
	}
	if p.Session != "" {
		kept := n.Sessions[:0:0]
		for _, x := range n.Sessions {
			if x != p.Session {
				kept = append(kept, x)
			}
		}
		n.Sessions = append(kept, p.Session)
	}
	sessions, _ := json.Marshal(n.Sessions)
	if n.Sessions == nil {
		sessions = []byte("[]")
	}
	n.Updated = now
	_, err = s.db.Exec(`UPDATE notes SET folder = ?, body = ?, pos = ?, color = ?, done_at = ?, sessions = ?, updated_at = ? WHERE id = ?`,
		n.Folder, n.Body, n.Pos, string(n.Color), n.Done, string(sessions), n.Updated, id)
	return n, err
}

// DeleteNote deletes note id; none is no error.
func (s *Store) DeleteNote(id string) error {
	_, err := s.db.Exec(`DELETE FROM notes WHERE id = ?`, id)
	return err
}

// AddNoteFolder makes a folder of notes named name, after every other.
func (s *Store) AddNoteFolder(name string) (api.NoteFolder, error) {
	var most sql.NullFloat64
	if err := s.db.QueryRow(`SELECT MAX(pos) FROM note_folders`).Scan(&most); err != nil {
		return api.NoteFolder{}, err
	}
	f := api.NoteFolder{Name: name}
	if most.Valid {
		f.Pos = most.Float64 + 1
	}
	r, err := s.db.Exec(`INSERT INTO note_folders (name, pos) VALUES (?, ?)`, f.Name, f.Pos)
	if err != nil {
		return f, err
	}
	f.ID, err = r.LastInsertId()
	return f, err
}

// PatchNoteFolder renames or moves folder id.
func (s *Store) PatchNoteFolder(id int64, p api.NoteFolderPatch) error {
	var f api.NoteFolder
	err := s.db.QueryRow(`SELECT id, name, pos FROM note_folders WHERE id = ?`, id).Scan(&f.ID, &f.Name, &f.Pos)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoNote
	}
	if err != nil {
		return err
	}
	if p.Name != nil {
		f.Name = *p.Name
	}
	if p.Pos != nil {
		f.Pos = *p.Pos
	}
	_, err = s.db.Exec(`UPDATE note_folders SET name = ?, pos = ? WHERE id = ?`, f.Name, f.Pos, id)
	return err
}

// DeleteNoteFolder deletes folder id, its notes left in none.
func (s *Store) DeleteNoteFolder(id int64) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE notes SET folder = 0 WHERE folder = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM note_folders WHERE id = ?`, id)
		return err
	})
}
