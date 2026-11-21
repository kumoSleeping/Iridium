package store

import (
	"database/sql"
	"strings"
)

// Reactions and messages taken back (docs/channels.md "表情" "撤回"). channel_reactions: who put
// which emoji on which message of a channel. channel_recalls: the messages of a channel taken
// back, by whom (its name) and when; the message itself stays a gone row (its number, its ref,
// no words), so what it was read in from is not taken in again.
const reactionsSchema = `CREATE TABLE IF NOT EXISTS channel_reactions (
		channel TEXT NOT NULL,
		seq     INTEGER NOT NULL,
		emoji   TEXT NOT NULL,
		member  TEXT NOT NULL,
		name    TEXT NOT NULL DEFAULT '',
		at      INTEGER NOT NULL,
		PRIMARY KEY (channel, seq, emoji, member)
	);
	CREATE TABLE IF NOT EXISTS channel_recalls (
		channel TEXT NOT NULL,
		seq     INTEGER NOT NULL,
		by      TEXT NOT NULL DEFAULT '',
		at      INTEGER NOT NULL,
		PRIMARY KEY (channel, seq)
	);`

// Reaction is one member's emoji on message Seq of Channel: Member as a message's author is
// kept (a login's id, "agent:<session>"), Name what it went by then.
type Reaction struct {
	Channel string
	Seq     int64
	Emoji   string
	Member  string
	Name    string
	At      int64
}

// AddReaction puts r on its message; added false if that member had that emoji there already.
func (s *Store) AddReaction(r Reaction) (added bool, err error) {
	if r.At == 0 {
		r.At = now()
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO channel_reactions (channel, seq, emoji, member, name, at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.Channel, r.Seq, r.Emoji, r.Member, r.Name, r.At)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RemoveReaction takes member's emoji off message seq of channel; false if it had none.
func (s *Store) RemoveReaction(channel string, seq int64, emoji, member string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM channel_reactions WHERE channel = ? AND seq = ? AND emoji = ? AND member = ?`, channel, seq, emoji, member)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClearReactions takes every reaction off message seq of channel (emoji "": of every emoji):
// those it took.
func (s *Store) ClearReactions(channel string, seq int64, emoji string) ([]Reaction, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := reactionRows(tx.Query(`SELECT channel, seq, emoji, member, name, at FROM channel_reactions WHERE channel = ? AND seq = ? AND (? = '' OR emoji = ?) ORDER BY at, rowid`, channel, seq, emoji, emoji))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM channel_reactions WHERE channel = ? AND seq = ? AND (? = '' OR emoji = ?)`, channel, seq, emoji, emoji); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// Reactions are the reactions on the messages seqs of channel, by message, each message's in the
// order they were put on.
func (s *Store) Reactions(channel string, seqs []int64) (map[int64][]Reaction, error) {
	out := map[int64][]Reaction{}
	if len(seqs) == 0 {
		return out, nil
	}
	lo, hi := seqs[0], seqs[0]
	for _, n := range seqs {
		lo, hi = min(lo, n), max(hi, n)
	}
	want := map[int64]bool{}
	for _, n := range seqs {
		want[n] = true
	}
	rows, err := reactionRows(s.db.Query(`SELECT channel, seq, emoji, member, name, at FROM channel_reactions WHERE channel = ? AND seq BETWEEN ? AND ? ORDER BY at, rowid`, channel, lo, hi))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if want[r.Seq] {
			out[r.Seq] = append(out[r.Seq], r)
		}
	}
	return out, nil
}

func reactionRows(rows *sql.Rows, err error) ([]Reaction, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reaction
	for rows.Next() {
		var r Reaction
		if err := rows.Scan(&r.Channel, &r.Seq, &r.Emoji, &r.Member, &r.Name, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Recall is a message of a channel taken back: by whom (its name), when.
type Recall struct {
	Channel string
	Seq     int64
	By      string
	At      int64
}

// RecallChannelMessage takes message seq of channel back for everyone (docs/channels.md "撤回"):
// its words, its data and its reactions go, the marks of those it named that point at it, and it
// is no more found; its number and where it is from (ref) stay. by: who took it back, by name.
// false if there is no such message, or it is gone already.
func (s *Store) RecallChannelMessage(channel string, seq int64, by string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRow(`SELECT rowid FROM channel_messages WHERE channel = ? AND seq = ? AND gone = 0`, channel, seq).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	at := now()
	for _, q := range []struct {
		q    string
		args []any
	}{
		{`UPDATE channel_messages SET gone = 1, text = '', data = '', edited_at = ? WHERE rowid = ?`, []any{at, id}},
		{`DELETE FROM channel_fts WHERE rowid = ?`, []any{id}},
		{`DELETE FROM channel_reactions WHERE channel = ? AND seq = ?`, []any{channel, seq}},
		{`DELETE FROM channel_ats WHERE channel = ? AND seq = ?`, []any{channel, seq}},
		{`INSERT OR REPLACE INTO channel_recalls (channel, seq, by, at) VALUES (?, ?, ?, ?)`, []any{channel, seq, by, at}},
	} {
		if _, err := tx.Exec(q.q, q.args...); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// Recalls are the messages of channel taken back after at (ms), the earliest first.
func (s *Store) Recalls(channel string, after int64) ([]Recall, error) {
	rows, err := s.db.Query(`SELECT channel, seq, by, at FROM channel_recalls WHERE channel = ? AND at > ? ORDER BY at, seq`, channel, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recall
	for rows.Next() {
		var r Recall
		if err := rows.Scan(&r.Channel, &r.Seq, &r.By, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecalledRefs are what the messages of channel taken back were from (their refs: an input's id,
// a block of an agent's record "b:<session>:<at>"), and when the latest of them was said (ms;
// 0 none): a record read again leaves them out.
func (s *Store) RecalledRefs(channel string) (refs map[string]bool, latest int64, err error) {
	rows, err := s.db.Query(`SELECT m.ref, m.at FROM channel_recalls r JOIN channel_messages m ON m.channel = r.channel AND m.seq = r.seq WHERE r.channel = ?`, channel)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	refs = map[string]bool{}
	for rows.Next() {
		var ref string
		var at int64
		if err := rows.Scan(&ref, &at); err != nil {
			return nil, 0, err
		}
		if ref != "" {
			refs[ref] = true
		}
		latest = max(latest, at)
	}
	return refs, latest, rows.Err()
}

// RecalledChannels are the channels some message was taken back in.
func (s *Store) RecalledChannels() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT channel FROM channel_recalls`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		out[ch] = true
	}
	return out, rows.Err()
}

// LastGivenAt is when session's agent was last given something of channel (an injection: a
// message, what its ir was answered with there); 0 never, or none kept.
func (s *Store) LastGivenAt(session, channel string) int64 {
	var at int64
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(at), 0) FROM injections WHERE session = ? AND channel = ?`, session, channel).Scan(&at)
	return at
}

// SeqsByRef are the numbers of the messages of channel (not gone) that are from refs, by ref.
func (s *Store) SeqsByRef(channel string, refs []string) (map[string]int64, error) {
	out := map[string]int64{}
	for len(refs) > 0 {
		part := refs[:min(len(refs), 400)]
		refs = refs[len(part):]
		args := []any{channel}
		marks := make([]string, len(part))
		for i, r := range part {
			marks[i], args = "?", append(args, r)
		}
		rows, err := s.db.Query(`SELECT ref, seq FROM channel_messages WHERE channel = ? AND gone = 0 AND ref IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ref string
			var seq int64
			if err := rows.Scan(&ref, &seq); err != nil {
				rows.Close()
				return nil, err
			}
			out[ref] = seq
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
