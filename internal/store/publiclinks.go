package store

import (
	"database/sql"
	"errors"
)

// A session's public link (docs/api.md "公开链接"): a read-only view of what was said in it, for
// anyone with the link. Only the token's hash is kept; one link per session, a new one replacing it.

// PublicLink is a session's link: Hash, its token's (gate.HashSecret); Creator, the app or login
// that made it; CreatedAt in ms.
type PublicLink struct {
	Session   string
	Hash      string
	Creator   string
	CreatedAt int64
}

// SetPublicLink makes l its session's link, in place of any it had.
func (s *Store) SetPublicLink(l PublicLink) error {
	_, err := s.db.Exec(`INSERT INTO public_links (session, hash, creator, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (session) DO UPDATE SET hash = excluded.hash, creator = excluded.creator, created_at = excluded.created_at`,
		l.Session, l.Hash, l.Creator, l.CreatedAt)
	return err
}

// PublicLinkOf is session's link; false if it has none.
func (s *Store) PublicLinkOf(session string) (PublicLink, bool, error) {
	return s.publicLink(`SELECT session, hash, creator, created_at FROM public_links WHERE session = ?`, session)
}

// PublicLinkByHash is the link whose token hashes to hash; false if none.
func (s *Store) PublicLinkByHash(hash string) (PublicLink, bool, error) {
	return s.publicLink(`SELECT session, hash, creator, created_at FROM public_links WHERE hash = ?`, hash)
}

func (s *Store) publicLink(q, arg string) (PublicLink, bool, error) {
	var l PublicLink
	err := s.db.QueryRow(q, arg).Scan(&l.Session, &l.Hash, &l.Creator, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicLink{}, false, nil
	}
	return l, err == nil, err
}

// DeletePublicLink revokes session's link; false if it had none.
func (s *Store) DeletePublicLink(session string) (bool, error) {
	r, err := s.db.Exec(`DELETE FROM public_links WHERE session = ?`, session)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
