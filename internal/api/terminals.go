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

// Rules is what a terminal may do: everything, every terminal alike, but Claude (the user,
// 2026-11-08). Stored as before, the other fields of old tables are no longer read.
type Rules struct {
	// ClaudeOwner: the terminal is the owner's own (Claude's terms), so it may use Claude. Set as
	// the terminal was made, by the server only, and never changed. Without it a terminal sees
	// Claude's sessions but may not start, write to or answer one.
	ClaudeOwner *OwnerConfirm `json:"claudeOwner,omitempty"`
}

// OwnerConfirm is how, and when (ms), a terminal was found the owner's own.
type OwnerConfirm struct {
	// By: "local" (a person's ir on the machine: ir invite --owner, ir token --owner), "panel" (a person on a panel that has Claude, or one that
	// confirmed an agent's ask) or "first" (the first panel registered).
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
	// Device: what it runs on, as the badge on its messages' avatar shows; the server always sets it.
	Device Device `json:"device,omitempty"`
	// Bot: a plugin with a chat in Iridium; a session's menu offers to bring that chat to it.
	Bot bool `json:"bot,omitempty"`
}

// Device is what a terminal runs on, set in its settings (PUT /api/me/device, ir profile
// --device): a computer, the default, a laptop, a phone, or none shown (no badge).
type Device string

const (
	DeviceComputer Device = "computer"
	DeviceLaptop   Device = "laptop"
	DevicePhone    Device = "phone"
	DeviceNone     Device = "none"
)

// Valid says whether d is one of the devices.
func (d Device) Valid() bool {
	switch d {
	case DeviceComputer, DeviceLaptop, DevicePhone, DeviceNone:
		return true
	}
	return false
}

// SetDevice is PUT /api/me/device and PUT /api/terminals/{id}/device.
type SetDevice struct {
	Device Device `json:"device"`
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
// Owner (--owner): the terminal is the owner's own and gets Claude; only a person at the machine
// or on a panel that has Claude may say so. An agent's goes to a confirmation that only such a
// panel (or 本机) may allow.
type NewInvite struct {
	Name  string `json:"name,omitempty"`
	Owner bool   `json:"owner,omitempty"`
	// Into (tokens only, ir token --into): the daemon writes the token into this file (absolute,
	// 0600) and gives it to no one: not the asker, nor the panel that allows it. A plugin's setup
	// asks so, its agent never seeing the token.
	Into string `json:"into,omitempty"`
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
	Token    string   `json:"token,omitempty"`
	Into     string   `json:"into,omitempty"` // the file it went into (NewInvite.Into); no Token then
}

// Confirm is what an agent's ir asked for that only a panel may give (an invite link, a token, a
// credential's value, the use of credentials): it waits in the carried state "confirms" until a
// panel answers it. No secrets.
type Confirm struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // invite | token | secret-set | secret-use | exec-pair
	// Name: the invite's or token's name; secret-set: the secret's.
	Name      string `json:"name,omitempty"`
	Owner     bool   `json:"owner,omitempty"`   // --owner: it gets Claude; only a panel that has Claude allows it
	Into      string `json:"into,omitempty"`    // token: the file it goes into (NewInvite.Into)
	Asker     string `json:"asker"`             // the terminal that asked (本机)
	Session   string `json:"session,omitempty"` // the session whose agent asked, if known
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	// secret-set: the note it was asked with (else the stored one's), and whether one by that
	// name is stored (it is replaced).
	Note    string `json:"note,omitempty"`
	Replace bool   `json:"replace,omitempty"`
	// secret-use: the secrets, the command's opening (as written: {{NAME}}), and whether one of
	// them is confirmed every time (no "this session").
	Secrets []string `json:"secrets,omitempty"`
	Command string   `json:"command,omitempty"`
	Always  bool     `json:"always,omitempty"`
	// exec-pair: the code both sides show, and the machine that asks to join as an executor
	// (docs/exec.md "连接"); Name is the Bot it becomes.
	Code    string    `json:"code,omitempty"`
	Machine *ExecPair `json:"machine,omitempty"`
}

// Confirms is the carried state "confirms": the pending ones, oldest first. Panels only.
type Confirms struct {
	Pending []Confirm `json:"pending"`
}

// ConfirmAnswer is a panel's answer to a Confirm.
type ConfirmAnswer struct {
	Allow bool `json:"allow"`
	// Value: secret-set's value, typed on the panel; stored, never given back.
	Value string `json:"value,omitempty"`
	// ForSession: secret-use allowed for the rest of the session (till it stops).
	ForSession bool `json:"forSession,omitempty"`
	// Owner: exec-pair allowed as the owner's own machine: it runs Claude there. Only a panel that
	// has Claude says so.
	Owner bool `json:"owner,omitempty"`
}

// ExecPair is what an executor says of itself asking to pair (POST /api/exec/pair, no login).
type ExecPair struct {
	Name    string      `json:"name"`
	OS      string      `json:"os,omitempty"` // windows | darwin | linux
	Arch    string      `json:"arch,omitempty"`
	Program string      `json:"program,omitempty"`
	Agents  []ExecAgent `json:"agents,omitempty"`
}

// Machine is another machine's executor (docs/exec.md): a Bot, as it last said of itself; the
// carried state "machines" lists them, GET /api/machines too. Agents: those Iridium can run there.
type Machine struct {
	ID     string      `json:"id"` // its terminal
	Name   string      `json:"name"`
	Avatar string      `json:"avatar,omitempty"`
	OS     string      `json:"os"` // windows | darwin | linux
	Arch   string      `json:"arch,omitempty"`
	Home   string      `json:"home"`
	Sep    string      `json:"sep"`
	Agents []ExecAgent `json:"agents"`
	Online bool        `json:"online"`
	SeenAt int64       `json:"seenAt"` // when it last connected (ms)
	// NoShell: it starts only its agents, so command lines cannot run there (docs/exec.md).
	NoShell bool `json:"noShell,omitempty"`
}

// ExecAgent is an agent an executor has.
type ExecAgent struct {
	Agent   string `json:"agent"`
	Version string `json:"version,omitempty"`
}

// ExecPairing answers a pairing request: the code to show, and the request to ask after.
type ExecPairing struct {
	Code    string `json:"code"`
	Request string `json:"request"`
}

// ExecPairState is how a pairing request stands (GET /api/exec/pair/{request}): pending, allowed
// (with the TOKEN, given once), denied or expired.
type ExecPairState struct {
	Status string `json:"status"`
	Token  string `json:"token,omitempty"`
}

// ConfirmState is how a Confirm went, as the asker sees it: never a secret.
type ConfirmState struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	State     string `json:"state"` // pending | allowed | denied | expired | failed
	Name      string `json:"name,omitempty"`
	Owner     bool   `json:"owner,omitempty"`     // it was asked with --owner
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
