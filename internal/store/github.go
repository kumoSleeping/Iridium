package store

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
)

// GitHub bindings (docs/api.md "GitHub"): a login's GitHub account and its token, kept in the meta
// table under "github:<member>", the token sealed as the secrets' values are.

const githubKey = "github:"

// GitHub is a login's binding: Member, the login; the account's Login, ID, Avatar; At (ms).
type GitHub struct {
	Member string `json:"-"`
	Login  string `json:"login"`
	ID     int64  `json:"id"`
	Avatar string `json:"avatar,omitempty"`
	At     int64  `json:"at"`
	Sealed string `json:"sealed"`
}

// PutGitHub binds member to g's account with token, in place of one it had.
func (s *Store) PutGitHub(g GitHub, token string) error {
	sealed, err := s.seal(githubKey+g.Member, token)
	if err != nil {
		return err
	}
	g.Sealed = base64.StdEncoding.EncodeToString(sealed)
	b, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return s.Keep(githubKey+g.Member, string(b))
}

// GitHubs are the bindings, by member.
func (s *Store) GitHubs() ([]GitHub, error) {
	kept, err := s.KeptUnder(githubKey)
	if err != nil {
		return nil, err
	}
	out := []GitHub{}
	for k, v := range kept {
		var g GitHub
		if v == "" || json.Unmarshal([]byte(v), &g) != nil {
			continue
		}
		g.Member = strings.TrimPrefix(k, githubKey)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out, nil
}

// GitHubToken is member's binding and its token; ok false if it has none.
func (s *Store) GitHubToken(member string) (GitHub, string, bool, error) {
	v := s.Kept(githubKey + member)
	if v == "" {
		return GitHub{}, "", false, nil
	}
	var g GitHub
	if err := json.Unmarshal([]byte(v), &g); err != nil {
		return GitHub{}, "", false, err
	}
	g.Member = member
	sealed, err := base64.StdEncoding.DecodeString(g.Sealed)
	if err != nil {
		return GitHub{}, "", false, err
	}
	tok, err := s.open(githubKey+member, sealed)
	if err != nil {
		return GitHub{}, "", false, err
	}
	return g, tok, true, nil
}

// GitHubTokens are the bound tokens, by account login (to be masked).
func (s *Store) GitHubTokens() map[string]string {
	all, _ := s.GitHubs()
	out := map[string]string{}
	for _, g := range all {
		if _, tok, ok, err := s.GitHubToken(g.Member); ok && err == nil {
			out["GITHUB_"+strings.ToUpper(strings.ReplaceAll(g.Login, "-", "_"))] = tok
		}
	}
	return out
}

// DeleteGitHub unbinds member.
func (s *Store) DeleteGitHub(member string) error {
	_, err := s.db.Exec(`DELETE FROM meta WHERE key = ?`, githubKey+member)
	return err
}
