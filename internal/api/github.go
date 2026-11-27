package api

// GitHubBinding is a person's GitHub account (docs/api.md "GitHub"): Member, the login it is
// bound to (a login of ir_web, or "local" the host); Name, what that login goes by; Login, ID,
// Avatar, the account's; At, when it was bound (ms). Never the token.
type GitHubBinding struct {
	Member string `json:"member"`
	Name   string `json:"name,omitempty"`
	Login  string `json:"login"`
	ID     int64  `json:"id"`
	Avatar string `json:"avatar,omitempty"`
	At     int64  `json:"at"`
}

// GitHubList is GET /v1/internal/github: the bindings; Mine, the caller's own login (whose a
// bind or unbind is); Turn, whose git and gh an agent's turn now runs as ("" none: the machine's
// own).
type GitHubList struct {
	Bindings []GitHubBinding `json:"bindings"`
	Mine     string          `json:"mine,omitempty"`
	Turn     string          `json:"turn,omitempty"`
}

// SetGitHub is PUT /v1/internal/github: the caller's login's token, checked with GitHub first.
type SetGitHub struct {
	Token string `json:"token"`
}

// GitHubToken is GET /v1/internal/github/token, for an agent's git and gh alone (ir github):
// the token of the turn's login (or of User, asked by name), and its account; none bound: 204.
type GitHubToken struct {
	Member string `json:"member"`
	Login  string `json:"login"`
	Token  string `json:"token"`
}
