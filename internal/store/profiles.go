package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Profiles, roles and memory (docs/channels.md "资料", "角色", "记忆").

// Profile keys: a session's agent and a channel share "c:<id>" (a session's channel has its id).
func ChannelProfileKey(id string) string { return "c:" + id }

// RoomProfileKey is the profile key of a session's own channel as a channel: its intro, face
// and background, apart from its agent's (ChannelProfileKey; kumo, 2026-11-16).
func RoomProfileKey(id string) string { return "g:" + id }

// PersonProfileKey is the profile key of a person or an app, by its member ("<app>/<login>", "<app>").
func PersonProfileKey(member string) string { return "u:" + member }

// RoleProfileKey is the profile key of role id.
func RoleProfileKey(id string) string { return "r:" + id }

// BackgroundKey is where the background image of the profile under key is kept (avatars).
func BackgroundKey(key string) string { return "b:" + key }

// Profile is what is kept of a profile beside its name and its images: Intro, a few words on
// who it is; Persona, an agent's own words on how it works, for its system prompt alone;
// Avatar, Background: an emoji or a colour ("#2f4f6f") in place of an image, "" for
// the image, or none.
type Profile struct {
	Intro      string
	Persona    string
	Avatar     string
	Background string
}

// Profile is the profile kept under key; zero if none.
func (s *Store) Profile(key string) Profile {
	var p Profile
	_ = s.db.QueryRow(`SELECT intro, persona, avatar, background FROM profiles WHERE key = ?`, key).Scan(&p.Intro, &p.Persona, &p.Avatar, &p.Background)
	return p
}

// Profiles are the profiles whose keys begin with prefix, by key.
func (s *Store) Profiles(prefix string) (map[string]Profile, error) {
	rows, err := s.db.Query(`SELECT key, intro, persona, avatar, background FROM profiles WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Profile{}
	for rows.Next() {
		var k string
		var p Profile
		if err := rows.Scan(&k, &p.Intro, &p.Persona, &p.Avatar, &p.Background); err != nil {
			return nil, err
		}
		out[k] = p
	}
	return out, rows.Err()
}

// SetProfile keeps p under key, in place of what was there; a zero p takes the row away.
func (s *Store) SetProfile(key string, p Profile) error {
	if p == (Profile{}) {
		_, err := s.db.Exec(`DELETE FROM profiles WHERE key = ?`, key)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO profiles (key, intro, persona, avatar, background, at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET intro = excluded.intro, persona = excluded.persona, avatar = excluded.avatar, background = excluded.background, at = excluded.at`,
		key, p.Intro, p.Persona, p.Avatar, p.Background, now())
	return err
}

// CopyProfile makes what is kept under key from (the profile, the face, the background) to's too.
func (s *Store) CopyProfile(from, to string) error {
	return s.Tx(func(tx *sql.Tx) error {
		for _, q := range []struct {
			q    string
			args []any
		}{
			{`DELETE FROM profiles WHERE key = ?`, []any{to}},
			{`INSERT INTO profiles (key, intro, persona, avatar, background, at) SELECT ?, intro, persona, avatar, background, ? FROM profiles WHERE key = ?`, []any{to, now(), from}},
			{`DELETE FROM avatars WHERE key IN (?, ?)`, []any{to, BackgroundKey(to)}},
			{`INSERT INTO avatars (key, image, version, at) SELECT ?, image, version, ? FROM avatars WHERE key = ?`, []any{to, now(), from}},
			{`INSERT INTO avatars (key, image, version, at) SELECT ?, image, version, ? FROM avatars WHERE key = ?`, []any{BackgroundKey(to), now(), BackgroundKey(from)}},
		} {
			if _, err := tx.Exec(q.q, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// DropProfile takes away what is kept under key: the profile, the face, the background.
func (s *Store) DropProfile(key string) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM profiles WHERE key = ?`, key); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM avatars WHERE key IN (?, ?)`, key, BackgroundKey(key))
		return err
	})
}

// AvatarVersions are the versions of the images whose keys begin with prefix, by key.
func (s *Store) AvatarVersions(prefix string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, version FROM avatars WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ---- the sessions before a cleared one ----

// Now is the session session goes on as now: the one it was cleared into, and on through each
// cleared after (MoveAgent); session itself if it never was.
func (s *Store) Now(session string) string {
	seen := map[string]bool{session: true}
	for range 64 {
		var next string
		if s.db.QueryRow(`SELECT session FROM session_before WHERE before = ? ORDER BY at DESC LIMIT 1`, session).Scan(&next) != nil || seen[next] {
			break
		}
		seen[next] = true
		session = next
	}
	return session
}

// Before are the sessions session went on from, the latest first: the one it was cleared from,
// the one that was, at most limit of them.
func (s *Store) Before(session string, limit int) []Cleared {
	var out []Cleared
	seen := map[string]bool{session: true}
	for len(out) < limit {
		var c Cleared
		if s.db.QueryRow(`SELECT before, at FROM session_before WHERE session = ?`, session).Scan(&c.Session, &c.At) != nil || seen[c.Session] {
			break
		}
		seen[c.Session] = true
		out = append(out, c)
		session = c.Session
	}
	return out
}

// Cleared is a session another went on from, and when (ms).
type Cleared struct {
	Session string
	At      int64
}

// ---- who was named ----

// ChannelAt is the first message of Channel that names Reader (a login, "<app>/<login>") and
// that it has not read. Everyone: it names all the members, not the reader alone.
type ChannelAt struct {
	Channel  string
	Reader   string
	Seq      int64
	Everyone bool
}

// AddChannelAt notes that message seq of channel names reader, unless an earlier one that it
// has not read is noted already; one that named everyone gives way to one that names the reader.
func (s *Store) AddChannelAt(channel, reader string, seq int64, everyone bool) error {
	_, err := s.db.Exec(`INSERT INTO channel_ats (channel, reader, seq, everyone) VALUES (?, ?, ?, ?)
		ON CONFLICT (channel, reader) DO UPDATE SET everyone = 0 WHERE channel_ats.everyone = 1 AND excluded.everyone = 0`,
		channel, reader, seq, everyone)
	return err
}

// ClearChannelAts forgets what named reader in channel up to message seq: it has read them.
// It reports whether there was something.
func (s *Store) ClearChannelAts(channel, reader string, seq int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM channel_ats WHERE channel = ? AND reader = ? AND seq <= ?`, channel, reader, seq)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ChannelAts are what names reader and it has not read, in every channel.
func (s *Store) ChannelAts(reader string) ([]ChannelAt, error) {
	rows, err := s.db.Query(`SELECT channel, reader, seq, everyone FROM channel_ats WHERE reader = ?`, reader)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelAt
	for rows.Next() {
		var a ChannelAt
		if err := rows.Scan(&a.Channel, &a.Reader, &a.Seq, &a.Everyone); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- what an agent was told of the profiles ----

// ProfileTold is what session's agent was last told of the profiles in channel (JSON); "" if
// nothing yet.
func (s *Store) ProfileTold(session, channel string) string {
	var v string
	_ = s.db.QueryRow(`SELECT told FROM profile_told WHERE session = ? AND channel = ?`, session, channel).Scan(&v)
	return v
}

// SetProfileTold keeps told as what session's agent was last told of the profiles in channel.
func (s *Store) SetProfileTold(session, channel, told string) error {
	_, err := s.db.Exec(`INSERT INTO profile_told (session, channel, told) VALUES (?, ?, ?)
		ON CONFLICT (session, channel) DO UPDATE SET told = excluded.told`, session, channel, told)
	return err
}

// ---- roles ----

// Role is an agent kept to start sessions as: Name is its nick; its intro, persona, face and
// background are the profile under RoleProfileKey(ID). Model and Preset (dsh's) are what its
// sessions start with unless told otherwise; no effort, no mode: those are the task's.
type Role struct {
	ID      string
	Name    string
	Agent   string
	Model   string
	Preset  string
	Machine string
	Sort    float64
	Created int64
}

// ErrRoleName: another role has the name.
var ErrRoleName = errors.New("another role has the name")

// PutRole writes r, in place of the one with its id; ErrRoleName if another has its name.
func (s *Store) PutRole(r Role) error {
	if r.Created == 0 {
		r.Created = now()
	}
	_, err := s.db.Exec(`INSERT INTO roles (id, name, agent, model, preset, machine, sort, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, agent = excluded.agent, model = excluded.model,
			preset = excluded.preset, machine = excluded.machine, sort = excluded.sort`,
		r.ID, r.Name, r.Agent, r.Model, r.Preset, r.Machine, r.Sort, r.Created)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrRoleName
	}
	return err
}

// Roles are the roles, in their order.
func (s *Store) Roles() ([]Role, error) {
	rows, err := s.db.Query(`SELECT id, name, agent, model, preset, machine, sort, created_at FROM roles ORDER BY sort, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Agent, &r.Model, &r.Preset, &r.Machine, &r.Sort, &r.Created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRole takes role id away, with its profile and its memory; the sessions started as it
// are no role's from then on.
func (s *Store) DeleteRole(id string) error {
	if err := s.DropProfile(RoleProfileKey(id)); err != nil {
		return err
	}
	return s.Tx(func(tx *sql.Tx) error {
		for _, q := range []string{`DELETE FROM roles WHERE id = ?`, `DELETE FROM session_roles WHERE role = ?`} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM memories WHERE owner = ?`, RoleProfileKey(id))
		return err
	})
}

// RoleOf is the role session was started as; "" if none.
func (s *Store) RoleOf(session string) string {
	var v string
	_ = s.db.QueryRow(`SELECT role FROM session_roles WHERE session = ?`, session).Scan(&v)
	return v
}

// SetRoleOf makes role the one session is of ("": none). What the session remembered as its
// own becomes the role's.
func (s *Store) SetRoleOf(session, role string) error {
	if role == "" {
		_, err := s.db.Exec(`DELETE FROM session_roles WHERE session = ?`, session)
		return err
	}
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO session_roles (session, role) VALUES (?, ?) ON CONFLICT (session) DO UPDATE SET role = excluded.role`, session, role); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE memories SET owner = ? WHERE owner = ?`, RoleProfileKey(role), ChannelProfileKey(session))
		return err
	})
}

// ---- memory ----

// Memory is something an agent chose to remember: Owner, whose it is (a role's key or a
// session's); Session, the session that wrote it; Channel, Seq: the channel it was working for
// then and how far that channel had come: where the memory is from.
type Memory struct {
	ID      int64
	Owner   string
	Text    string
	Session string
	Channel string
	Seq     int64
	At      int64
}

// MemoryOwner is whose what session remembers is: its role's, else its own.
func (s *Store) MemoryOwner(session string) string {
	if r := s.RoleOf(session); r != "" {
		return RoleProfileKey(r)
	}
	return ChannelProfileKey(session)
}

// AddMemory keeps m (its owner, text and where it is from); its id.
func (s *Store) AddMemory(m Memory) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO memories (owner, text, session, channel, seq, at) VALUES (?, ?, ?, ?, ?, ?)`, m.Owner, m.Text, m.Session, m.Channel, m.Seq, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Memories are what owner remembers, the latest first, at most limit (0: all).
func (s *Store) Memories(owner string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.Query(`SELECT id, owner, text, session, channel, seq, at FROM memories WHERE owner = ? ORDER BY id DESC LIMIT ?`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Owner, &m.Text, &m.Session, &m.Channel, &m.Seq, &m.At); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMemory forgets owner's memory id; false if it has none such.
func (s *Store) DeleteMemory(owner string, id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM memories WHERE owner = ? AND id = ?`, owner, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
