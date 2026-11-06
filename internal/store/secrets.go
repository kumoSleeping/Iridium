package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// secretsSchema is migration 10: the credentials (plan "凭据"). value: the value sealed with
// AES-256-GCM under the key file by the database (secrets.key, 0600), the nonce first, the name as
// additional data (a value moved to another row does not open). scope: all | workspaces | owner;
// workspaces: a JSON list of workspace ids for scope workspaces. always: every use is confirmed.
// by: the terminal that stored it. secret_uses: one row per use by ir run.
const secretsSchema = `CREATE TABLE secrets (
		name TEXT PRIMARY KEY,
		note TEXT NOT NULL DEFAULT '',
		scope TEXT NOT NULL DEFAULT 'all',
		workspaces TEXT NOT NULL DEFAULT '[]',
		always INTEGER NOT NULL DEFAULT 0,
		value BLOB NOT NULL,
		by TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE secret_uses (
		id INTEGER PRIMARY KEY,
		secret TEXT NOT NULL,
		session TEXT NOT NULL,
		terminal TEXT NOT NULL,
		command TEXT NOT NULL,
		at INTEGER NOT NULL
	);
	CREATE INDEX secret_uses_secret ON secret_uses(secret, at);
	CREATE INDEX secret_uses_at ON secret_uses(at);`

// Secret is a credential as stored, without its value.
type Secret struct {
	Name       string
	Note       string
	Scope      string // all | workspaces | owner
	Workspaces []int64
	Always     bool
	By         string // the terminal that stored it
	CreatedAt  int64
	UpdatedAt  int64
}

// SecretUse is one use of a secret.
type SecretUse struct {
	Secret   string
	Session  string
	Terminal string
	Command  string
	At       int64
}

// ErrNoSecret: no secret by that name.
var ErrNoSecret = errors.New("no such secret")

// secretKey is the key the values are sealed with: read from the key file, or made there the
// first time (0600, 32 random bytes).
func (s *Store) secretKey() ([]byte, error) {
	s.kmu.Lock()
	defer s.kmu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	b, err := os.ReadFile(s.keyPath)
	if errors.Is(err, os.ErrNotExist) {
		b = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, b); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(s.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(b); err != nil {
			f.Close()
			os.Remove(s.keyPath)
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("%s: not a 32-byte key", s.keyPath)
	}
	if err := os.Chmod(s.keyPath, 0o600); err != nil {
		return nil, err
	}
	s.key = b
	return b, nil
}

func (s *Store) aead() (cipher.AEAD, error) {
	k, err := s.secretKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) seal(name, value string) ([]byte, error) {
	g, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(value), []byte(name)), nil
}

func (s *Store) open(name string, sealed []byte) (string, error) {
	g, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(sealed) < g.NonceSize() {
		return "", fmt.Errorf("secret %s: sealed value too short", name)
	}
	b, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], []byte(name))
	if err != nil {
		return "", fmt.Errorf("secret %s: does not open with %s", name, s.keyPath)
	}
	return string(b), nil
}

// PutSecret stores sec with value, in place of one by its name (whose creation time it keeps).
func (s *Store) PutSecret(sec Secret, value string) error {
	sealed, err := s.seal(sec.Name, value)
	if err != nil {
		return err
	}
	return s.putSecret(sec, sealed)
}

func (s *Store) putSecret(sec Secret, sealed []byte) error {
	if sec.Scope == "" {
		sec.Scope = "all"
	}
	if sec.Workspaces == nil {
		sec.Workspaces = []int64{}
	}
	ws, _ := json.Marshal(sec.Workspaces)
	t := now()
	_, err := s.db.Exec(`INSERT INTO secrets (name, note, scope, workspaces, always, value, by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET note = excluded.note, scope = excluded.scope, workspaces = excluded.workspaces, always = excluded.always,
			value = excluded.value, by = excluded.by, updated_at = excluded.updated_at`,
		sec.Name, sec.Note, sec.Scope, string(ws), sec.Always, sealed, sec.By, t, t)
	return err
}

// SetSecretMeta changes what is said of secret name (note, scope, always), not its value.
func (s *Store) SetSecretMeta(sec Secret) error {
	if sec.Workspaces == nil {
		sec.Workspaces = []int64{}
	}
	ws, _ := json.Marshal(sec.Workspaces)
	r, err := s.db.Exec(`UPDATE secrets SET note = ?, scope = ?, workspaces = ?, always = ?, updated_at = ? WHERE name = ?`,
		sec.Note, sec.Scope, string(ws), sec.Always, now(), sec.Name)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNoSecret
	}
	return nil
}

// DeleteSecret forgets secret name and its uses.
func (s *Store) DeleteSecret(name string) error {
	return s.Tx(func(tx *sql.Tx) error {
		r, err := tx.Exec(`DELETE FROM secrets WHERE name = ?`, name)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSecret
		}
		_, err = tx.Exec(`DELETE FROM secret_uses WHERE secret = ?`, name)
		return err
	})
}

const secretCols = `name, note, scope, workspaces, always, by, created_at, updated_at`

func scanSecret(sc interface{ Scan(...any) error }, extra ...any) (Secret, error) {
	var sec Secret
	var ws string
	err := sc.Scan(append([]any{&sec.Name, &sec.Note, &sec.Scope, &ws, &sec.Always, &sec.By, &sec.CreatedAt, &sec.UpdatedAt}, extra...)...)
	if err != nil {
		return sec, err
	}
	if json.Unmarshal([]byte(ws), &sec.Workspaces) != nil || sec.Workspaces == nil {
		sec.Workspaces = []int64{}
	}
	return sec, nil
}

// Secret is secret name, without its value.
func (s *Store) Secret(name string) (Secret, error) {
	sec, err := scanSecret(s.db.QueryRow(`SELECT `+secretCols+` FROM secrets WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return sec, ErrNoSecret
	}
	return sec, err
}

// Secrets is every secret, by name, without values.
func (s *Store) Secrets() ([]Secret, error) {
	rows, err := s.db.Query(`SELECT ` + secretCols + ` FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Secret
	for rows.Next() {
		sec, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// SecretValue is secret name's value.
func (s *Store) SecretValue(name string) (string, error) {
	var sealed []byte
	err := s.db.QueryRow(`SELECT value FROM secrets WHERE name = ?`, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoSecret
	}
	if err != nil {
		return "", err
	}
	return s.open(name, sealed)
}

// SecretValues is every secret's value, by name. One that does not open is left out, and the
// first such error given with the rest.
func (s *Store) SecretValues() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT name, value FROM secrets`)
	if err != nil {
		return nil, err
	}
	type row struct {
		name   string
		sealed []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.sealed); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	out := map[string]string{}
	var first error
	for _, r := range all {
		v, err := s.open(r.name, r.sealed)
		if err != nil {
			first = cmpOr(first, err)
			continue
		}
		out[r.name] = v
	}
	return out, first
}

func cmpOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// AddSecretUse records a use.
func (s *Store) AddSecretUse(u SecretUse) error {
	if u.At == 0 {
		u.At = now()
	}
	_, err := s.db.Exec(`INSERT INTO secret_uses (secret, session, terminal, command, at) VALUES (?, ?, ?, ?, ?)`,
		u.Secret, u.Session, u.Terminal, u.Command, u.At)
	return err
}

// SecretUses is the newest uses, at most limit, newest first; secret "" is every secret's.
func (s *Store) SecretUses(secret string, limit int) ([]SecretUse, error) {
	rows, err := s.db.Query(`SELECT secret, session, terminal, command, at FROM secret_uses WHERE ? = '' OR secret = ? ORDER BY at DESC, id DESC LIMIT ?`, secret, secret, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretUse
	for rows.Next() {
		var u SecretUse
		if err := rows.Scan(&u.Secret, &u.Session, &u.Terminal, &u.Command, &u.At); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SecretUseCounts is each secret's number of uses and its last, by name.
func (s *Store) SecretUseCounts() (map[string]int, map[string]SecretUse, error) {
	rows, err := s.db.Query(`SELECT u.secret, c.n, u.session, u.terminal, u.command, u.at FROM secret_uses u
		JOIN (SELECT secret, COUNT(*) AS n, MAX(id) AS last FROM secret_uses GROUP BY secret) c ON u.id = c.last`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts, last := map[string]int{}, map[string]SecretUse{}
	for rows.Next() {
		var u SecretUse
		var n int
		if err := rows.Scan(&u.Secret, &n, &u.Session, &u.Terminal, &u.Command, &u.At); err != nil {
			return nil, nil, err
		}
		counts[u.Secret], last[u.Secret] = n, u
	}
	return counts, last, rows.Err()
}
