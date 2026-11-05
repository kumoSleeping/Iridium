package api

// Interactive terminals (交互终端, plan "交互终端"): web panels, channels (plugins, peers) and the
// built-in 本机. See docs/api.md "交互终端".

// TerminalKind: panel (a web panel, passkey) or channel (a plugin or peer, token; also 本机).
type TerminalKind string

const (
	TerminalPanel   TerminalKind = "panel"
	TerminalChannel TerminalKind = "channel"
)

// LocalTerminal is the built-in terminal's ID: the local ir, and the web page while no public
// origin is set.
const LocalTerminal = "local"

// Rules is a terminal's permission table. The built-in 本机's is all of it and cannot change.
type Rules struct {
	// Agents it may use (and whose sessions it sees). claude needs ClaudeOwner.
	Agents []Agent `json:"agents"`
	// ClaudeOwner: the terminal is the owner's own (Claude's terms), as said on the machine. Only
	// the server writes it; the web may take Claude away but never give it.
	ClaudeOwner *OwnerConfirm `json:"claudeOwner,omitempty"`
	// AllDirs: every directory; else only under Dirs or in Workspaces.
	AllDirs    bool     `json:"allDirs"`
	Dirs       []string `json:"dirs"`
	Workspaces []int64  `json:"workspaces"`
	// SeeOthers: it sees the sessions other terminals opened, not only its own.
	SeeOthers bool `json:"seeOthers"`
	FilesIn   bool `json:"filesIn"`  // may copy files in (uploads)
	FilesOut  bool `json:"filesOut"` // may copy files out (downloads)
	Answer    bool `json:"answer"`   // may answer questions and approvals
	// MustApprove: what it starts runs in an approving mode; it cannot send to a session that
	// skips approvals.
	MustApprove bool `json:"mustApprove"`
	// DailyTokens caps the tokens (input, output and cache, all models) of the turns it starts in a
	// day, local time; 0 is no cap.
	DailyTokens int64 `json:"dailyTokens"`
	// AllCommands: every slash command; else only Commands (e.g. "/compact").
	AllCommands bool     `json:"allCommands"`
	Commands    []string `json:"commands"`
}

// OwnerConfirm is how, and when (ms), a terminal was found the owner's own.
type OwnerConfirm struct {
	// By: "local" (a person's ir on the machine: ir invite --owner, ir token --owner,
	// ir access --claude on) or "first" (the first panel registered).
	By string `json:"by"`
	At int64  `json:"at"`
}

// Terminal is an interactive terminal as listed. No secrets.
type Terminal struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Kind       TerminalKind `json:"kind"`
	Avatar     string       `json:"avatar,omitempty"` // URL for <img src>
	Local      bool         `json:"local,omitempty"`  // the built-in 本机
	Passkeys   int          `json:"passkeys,omitempty"`
	Logins     int          `json:"logins,omitempty"` // logged-in browsers
	Online     int          `json:"online,omitempty"` // live connections
	CreatedAt  int64        `json:"createdAt,omitempty"`
	LastSeenAt int64        `json:"lastSeenAt,omitempty"`
	Rules      Rules        `json:"rules"`
	UsedToday  int64        `json:"usedToday"` // tokens of the turns it started today
}

// PendingInvite is a panel invite not used yet.
type PendingInvite struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Setup     bool   `json:"setup,omitempty"` // the first-run link in the state directory
	Owner     bool   `json:"owner,omitempty"` // ir invite --owner: the panel gets Claude
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// Terminals is GET /api/terminals (ir access).
type Terminals struct {
	Terminals []Terminal      `json:"terminals"`
	Invites   []PendingInvite `json:"invites"`
}

// Me is GET /api/me: the calling terminal. Panel: it may approve, widen and see tokens.
type Me struct {
	Terminal `tstype:",extends"`
	Panel    bool `json:"panel"`
	Public   bool `json:"public"` // a public origin is set: passkeys are on
}

// NewInvite asks for a panel invite (ir invite [name]) or a channel token (ir token <name>).
// Owner (--owner): the terminal is the owner's own and gets Claude; only a person's ir on the
// machine may say so.
type NewInvite struct {
	Name  string `json:"name,omitempty"`
	Owner bool   `json:"owner,omitempty"`
}

// Invite is a new panel invite. URL holds the secret: only a panel (or ir at a terminal) sees it.
type Invite struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expiresAt"`
	// QR is URL as a QR code, an SVG data URL for <img src>.
	QR string `json:"qr,omitempty"`
}

// Token is a channel's new token: shown once. Asking again replaces it.
type Token struct {
	Terminal Terminal `json:"terminal"`
	Token    string   `json:"token"`
}

// SetRules changes a terminal's permission table. Adding claude is for a person's ir on the
// machine only (ir access <name> --claude on); anyone that manages may take it away.
type SetRules struct {
	Rules Rules `json:"rules"`
}

// Confirm is what an agent's ir asked for that only a panel may give (an invite link, a token):
// it waits in the carried state "confirms" until a panel answers it. No secrets.
type Confirm struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // invite | token
	Name      string `json:"name,omitempty"`
	Asker     string `json:"asker"`             // the terminal that asked (本机)
	Session   string `json:"session,omitempty"` // the session whose agent asked, if known
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Confirms is the carried state "confirms": the pending ones, oldest first. Panels only.
type Confirms struct {
	Pending []Confirm `json:"pending"`
}

// ConfirmAnswer is a panel's answer to a Confirm.
type ConfirmAnswer struct {
	Allow bool `json:"allow"`
}

// ConfirmState is how a Confirm went, as the asker sees it: never a secret.
type ConfirmState struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	State     string `json:"state"` // pending | allowed | denied | expired | failed
	Name      string `json:"name,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"` // allowed invite: until when the link works
	Error     string `json:"error,omitempty"`
}

// ConfirmReply answers the panel that allowed a Confirm: the secret is for it alone.
type ConfirmReply struct {
	ConfirmState `tstype:",extends"`
	Invite       *Invite `json:"invite,omitempty"`
	Token        *Token  `json:"token,omitempty"`
}

// Pending is the reply to an agent's ir invite or ir token: a panel must allow it.
type Pending struct {
	Confirm Confirm `json:"confirm"`
}

// SetPublic sets public.origin ("" turns the public way in off). The gate restarts with it.
type SetPublic struct {
	Origin string `json:"origin"`
}

// PublicState is GET /api/public.
type PublicState struct {
	Origin   string `json:"origin,omitempty"`
	Passkeys bool   `json:"passkeys"` // some panel has a passkey
}
