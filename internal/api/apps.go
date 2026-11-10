package api

// Apps and their logins (0.2, docs/next/k_glossary.md "应用"): an app connects with its TOKEN (a
// QQ bridge, a dsh plugin, an executor), ir_web is the built-in one whose logins are browsers with a
// passkey, and the host is the machine Iridium runs on: listed with them, never managed. See
// docs/api.md.

// HostID is the host's storage ID; it is shown as 主机 (host).
const HostID = "local"

// WebApp is the built-in app ir_web's ID: there only with a public origin.
const WebApp = "ir_web"

// Rules is what an app or login may do: everything, all alike, but Claude (the user, 2026-11-08).
// Stored as before, the other fields of old tables are no longer read. S2 puts permissions in place.
type Rules struct {
	// ClaudeOwner: it is the owner's own (Claude's terms), so it may use Claude. Set as it was
	// made, by the server only, and never changed. Without it, it sees Claude's sessions but may
	// not start, write to or answer one.
	ClaudeOwner *OwnerConfirm `json:"claudeOwner,omitempty"`
}

// OwnerConfirm is how, and when (ms), an app or login was found the owner's own.
type OwnerConfirm struct {
	// By: "local" (a person's ir at the host), "panel" (a person on a login of ir_web that has
	// Claude, or one that confirmed an agent's ask) or "first" (the first login of ir_web).
	By string `json:"by"`
	At int64  `json:"at"`
}

// App is an app as listed, or the host (Host) or ir_web (Web) beside them. No secrets.
type App struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`   // what it is shown as (computer…); display only
	Avatar string `json:"avatar,omitempty"` // URL for <img src>
	// Host: the host, listed with the apps but not managed. Web: ir_web, the built-in app.
	Host       bool  `json:"host,omitempty"`
	Web        bool  `json:"web,omitempty"`
	Logins     int   `json:"logins,omitempty"` // ir_web: its logins
	Online     int   `json:"online,omitempty"` // live connections
	CreatedAt  int64 `json:"createdAt,omitempty"`
	LastSeenAt int64 `json:"lastSeenAt,omitempty"`
	Rules      Rules `json:"rules"`
	UsedToday  int64 `json:"usedToday"` // tokens of the turns it started today
	// Device: what it runs on, as the badge on its messages' avatar shows; the server always sets it.
	Device Device `json:"device,omitempty"`
	// Follows: the logins of ir_web (IDs) it follows: what a session asks while no chat points at
	// it goes to the apps that follow the login that began its turn, and to no other. Until S4.
	Follows []string `json:"follows,omitempty"`
	// Chat: its chat layer in Iridium, until S4 removes it: "" none (a session's menu offers nothing),
	// "idle" its event stream open but no chat yet (offered, greyed), "on" a chat a session's menu
	// may bring to that session.
	Chat AppChat `json:"chat,omitempty"`
}

// AppChat is App.Chat.
type AppChat string

const (
	AppChatIdle AppChat = "idle"
	AppChatOn   AppChat = "on"
)

// Login is a login of an app as listed: for now ir_web's, a browser with a passkey. No secrets.
type Login struct {
	App        string `json:"app"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar,omitempty"`
	Passkeys   int    `json:"passkeys,omitempty"`
	Sessions   int    `json:"sessions,omitempty"` // browsers logged in
	Online     int    `json:"online,omitempty"`
	CreatedAt  int64  `json:"createdAt,omitempty"`
	LastSeenAt int64  `json:"lastSeenAt,omitempty"`
	ApprovedAt int64  `json:"approvedAt,omitempty"` // 0: waits to be approved
	Rules      Rules  `json:"rules"`
	UsedToday  int64  `json:"usedToday"`
	Device     Device `json:"device,omitempty"`
}

// SetFollows is PUT /api/apps/{id}/follows: the logins of ir_web the app follows from now on.
type SetFollows struct {
	Panels []string `json:"panels"`
}

// Device is what an app or login runs on, set in its settings (PUT /api/me/device, ir apps
// device): a computer, the default, a laptop, a phone, or none shown (no badge).
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

// SetDevice is PUT /api/me/device and PUT /api/apps/{id}/device.
type SetDevice struct {
	Device Device `json:"device"`
}

// PendingInvite is a slot for a login of ir_web not taken yet.
type PendingInvite struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Setup     bool   `json:"setup,omitempty"`  // the first-run link in the state directory
	Claude    bool   `json:"claude,omitempty"` // the login gets Claude
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// Apps is GET /api/apps (ir apps): the host first, ir_web (with a public origin), the apps; the
// logins of ir_web; the invites not taken.
type Apps struct {
	Apps    []App           `json:"apps"`
	Logins  []Login         `json:"logins"`
	Invites []PendingInvite `json:"invites"`
}

// Me is GET /api/me: who calls. App: the app (HostID for the host, WebApp for a browser); Login:
// the login of it, for ir_web the browser's. ID is the one its usage and rules are kept by.
type Me struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	App       string `json:"app"`
	Login     string `json:"login,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	Host      bool   `json:"host,omitempty"`
	Web       bool   `json:"web,omitempty"` // a login of ir_web
	Rules     Rules  `json:"rules"`
	UsedToday int64  `json:"usedToday"`
	Device    Device `json:"device,omitempty"`
	Public    bool   `json:"public"` // a public origin is set: passkeys are on
}

// NewInvite asks for a slot for a new login of ir_web (ir invite [name] [--claude]). Claude: the
// login gets Claude, the owner's own; only a person at the host or on a login of ir_web that has
// Claude may say so. An agent's goes to a confirmation that only such a login (or the host) allows.
type NewInvite struct {
	Name   string `json:"name,omitempty"`
	Claude bool   `json:"claude,omitempty"`
}

// Invite is a new invite of ir_web. URL holds the secret: only a login of ir_web (or a person's
// ir at the host) sees it.
type Invite struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expiresAt"`
	// QR is URL as a QR code, an SVG data URL for <img src>.
	QR string `json:"qr,omitempty"`
}

// Token is an app's new TOKEN, as a pairing allowed gives it to the app alone.
type Token struct {
	App   App    `json:"app"`
	Token string `json:"token,omitempty"`
}

// Confirm is what an agent's ir asked for that only a person may give (an invite link, a
// credential's value, the use of credentials), or an app asking to pair: it waits in the carried
// state "confirms" until a login of ir_web (or the host) answers it. No secrets.
type Confirm struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // invite | secret-set | secret-use | exec-pair | bot-pair
	// Name: the invite's name; secret-set: the secret's.
	Name      string `json:"name,omitempty"`
	Owner     bool   `json:"owner,omitempty"`   // it gets Claude; only one that has Claude allows it
	Asker     string `json:"asker"`             // who asked (the app or login, or 主机)
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
	// (docs/exec.md "连接"); Name is the Bot it becomes. bot-pair: the same, a Bot that asks
	// (POST /api/pair), Machine saying its program. Replace there: a Bot by Name is there already,
	// which the answer may hand over (ConfirmAnswer.Replace).
	Code    string    `json:"code,omitempty"`
	Machine *ExecPair `json:"machine,omitempty"`
}

// Confirms is the carried state "confirms": the pending ones, oldest first. Panels only.
type Confirms struct {
	Pending []Confirm `json:"pending"`
}

// ConfirmAnswer is a person's answer to a Confirm.
type ConfirmAnswer struct {
	Allow bool `json:"allow"`
	// Value: secret-set's value, typed on the panel; stored, never given back.
	Value string `json:"value,omitempty"`
	// ForSession: secret-use allowed for the rest of the session (till it stops).
	ForSession bool `json:"forSession,omitempty"`
	// Owner: exec-pair allowed as the owner's own machine: it runs Claude there; bot-pair: the Bot
	// has Claude. Only a panel that has Claude says so.
	Owner bool `json:"owner,omitempty"`
	// Replace: a pairing allowed as the Bot by its name that is there already (Confirm.Replace):
	// the same terminal, its usage and Claude as they were, a new TOKEN, the old one void.
	Replace bool `json:"replace,omitempty"`
}

// ExecPair is what an executor or a Bot says of itself asking to pair (POST /api/exec/pair,
// POST /api/pair; no login). A Bot says its name and program only. NoShell and AgentShell are what
// it means to say in its hello (Machine's), so the card tells whether command lines would run there.
type ExecPair struct {
	Name       string      `json:"name"`
	OS         string      `json:"os,omitempty"` // windows | darwin | linux
	Arch       string      `json:"arch,omitempty"`
	Program    string      `json:"program,omitempty"`
	Agents     []ExecAgent `json:"agents,omitempty"`
	NoShell    bool        `json:"noShell,omitempty"`
	AgentShell bool        `json:"agentShell,omitempty"`
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
	// Latency: the connection's last round trip (ms), by its heartbeat; 0 not known.
	Latency int64 `json:"latency,omitempty"`
	// AgentShell: agents here may run command lines there (ir sh).
	AgentShell bool `json:"agentShell,omitempty"`
}

// ExecAgent is an agent an executor has.
type ExecAgent struct {
	Agent   string `json:"agent"`
	Version string `json:"version,omitempty"`
}

// DshWorkspace is a folder a new dsh session at a machine may open in (GET
// /api/machines/{id}/dsh/workspaces): one of dsh's workspaces there, as DSH Desktop's sidebar
// has them, else a folder its sessions were in. Path is '/'-separated, as GET /api/folders gives.
// Sessions: how many of its sessions Iridium lists (not archived, not kept from it), subagents aside.
type DshWorkspace struct {
	Title     string `json:"title"`
	Path      string `json:"path"`
	UpdatedAt int64  `json:"updatedAt,omitempty"` // ms
	Sessions  int    `json:"sessions"`
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

// ConfirmReply answers the one that allowed a Confirm: the secret is for it alone.
type ConfirmReply struct {
	ConfirmState `tstype:",extends"`
	Invite       *Invite `json:"invite,omitempty"`
}

// Pending is the reply to an agent's ir invite: a person must allow it.
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
	Passkeys bool   `json:"passkeys"` // some login of ir_web has a passkey
}
