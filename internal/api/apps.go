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
	// Perms: what an app was given as it paired (nil: all, as one paired before 0.2). Not the
	// host's or ir_web's.
	Perms     *Perms `json:"perms,omitempty"`
	UsedToday int64  `json:"usedToday"` // tokens of the turns it started today
	// Device: what it runs on, as the badge on its messages' avatar shows; the server always sets it.
	Device Device `json:"device,omitempty"`
	// Computer: an app that runs agents or command lines (an executor, ir join): its computer, as
	// it last said in its hello; nil for any other.
	Computer *Computer `json:"computer,omitempty"`
}

// Computer is an app's computer as its executor last said of itself (docs/exec.md "握手"): kept
// in the store, so one away is listed too. GET /api/apps lists it with the app, to every caller.
type Computer struct {
	OS   string `json:"os"` // windows | darwin | linux
	Arch string `json:"arch,omitempty"`
	Home string `json:"home"`
	Sep  string `json:"sep"`
	// Agents: those Iridium runs there, as it reported them; Off, turned off in its app's page
	// (PUT /api/apps/{id}/agents): not started there, not offered for new sessions.
	Agents []ExecAgent `json:"agents"`
	Online bool        `json:"online"` // its executor is connected
	SeenAt int64       `json:"seenAt"` // when it last connected (ms)
	// Shell: command lines run there, a person's on the page ($) and agents' (ir sh); else it
	// starts only its agents (docs/exec.md). Its owner's switch.
	Shell bool `json:"shell,omitempty"`
	// Latency: the connection's last round trip (ms), by its heartbeat; 0 not known.
	Latency int64 `json:"latency,omitempty"`
}

// AppAgents is PUT /api/apps/{id}/agents: the agents an app carries that are turned off there.
// Any it reports later is on till turned off.
type AppAgents struct {
	Off []string `json:"off"`
}

// Login is a login of an app as listed: ir_web's, a browser with a passkey; another app's, as it
// spoke (Satori-User-ID), waiting till approved (ApprovedAt 0). No secrets.
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
	// Perms: what it was given (nil: all); what it may use is this within its app's.
	Perms *Perms `json:"perms,omitempty"`
}

// Notice is POST /api/apps/{id}/logins/{login}/notice (ir send 应用/登录): Text shown to the
// login, no agent woken. Caller: the session it is from (ir's own, as Send's), if any.
type Notice struct {
	Text   string `json:"text"`
	Caller string `json:"caller,omitempty"`
}

// NoticeReply is where a notice went: the login (its ID and name) and how many of its browsers
// were pushed to.
type NoticeReply struct {
	App   string `json:"app"`
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
	Sent  int    `json:"sent"`
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
// logins of ir_web; the invites not taken. Public (draft 二「消息」): any caller gets the apps
// (names, types, avatars, online, computers) and the approved logins; their rules, perms and
// usage, the logins waiting and the invites only one that manages (Full).
type Apps struct {
	Apps    []App           `json:"apps"`
	Logins  []Login         `json:"logins"`
	Invites []PendingInvite `json:"invites"`
	Full    bool            `json:"full,omitempty"`
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

// Perms is what an app or login asks for or is given (0.2 "权限"): the agents it may use and the
// places it may use, each ["*"] for all. Plain data till S2 checks it; Claude stays Rules'.
type Perms struct {
	Agents []string `json:"agents"`
	Scope  []string `json:"scope"`
}

// PermsAll is the word for all in Perms' lists.
const PermsAll = "*"

// NeedKind is what a Need waits for a person to decide.
type NeedKind string

const (
	NeedAppJoin   NeedKind = "app_join"   // an app asks to pair
	NeedAppUpdate NeedKind = "app_update" // an app asks to pair by the name of one there: a new TOKEN for it
	NeedAppChange NeedKind = "app_change" // an app asks for other permissions
	NeedLoginJoin NeedKind = "login_join" // a login an app spoke as the first time
	NeedInvite    NeedKind = "invite"     // an agent's ir invite
	NeedSecretSet NeedKind = "secret_set" // an agent's ir secret set: a person types the value
	NeedSecretUse NeedKind = "secret_use" // an agent's ir run with credentials
)

// Need is what waits for a person (docs/next/k_glossary.md "需要"): an app's or a login's request,
// or what an agent's ir asked for that only a person may give. It waits in the carried state
// "needs" till the host or a login of ir_web that may approve answers it. No secrets.
type Need struct {
	ID   string   `json:"id"`
	Kind NeedKind `json:"kind"`
	// Name: the invite's name; secret_set: the secret's; app_join: the name the app asks for;
	// app_update, app_change: the app's; login_join: the login's.
	Name      string `json:"name,omitempty"`
	Owner     bool   `json:"owner,omitempty"`   // it gets Claude; only one that has Claude allows it
	Asker     string `json:"asker"`             // who asked (the app or login, or 主机)
	Session   string `json:"session,omitempty"` // the session whose agent asked, if known
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	// secret_set: the note it was asked with (else the stored one's), and whether one by that
	// name is stored (it is replaced).
	Note    string `json:"note,omitempty"`
	Replace bool   `json:"replace,omitempty"`
	// secret_use: the secrets, the command's opening (as written: {{NAME}}), and whether one of
	// them is confirmed every time (no "this session").
	Secrets []string `json:"secrets,omitempty"`
	Command string   `json:"command,omitempty"`
	Always  bool     `json:"always,omitempty"`
	// app_join, app_update: the code both sides show, and what the app says of itself.
	Code string       `json:"code,omitempty"`
	Pair *PairRequest `json:"pair,omitempty"`
	// App: app_update, the app by that name now (its system, last seen, whether online: an update
	// while it is online may be someone else's); app_change, login_join: the app asking.
	App *App `json:"app,omitempty"`
	// Wants: app_join, app_change: what it asks for; the answer may give less, never more.
	// app_change: Has, what it has now.
	Wants *Perms `json:"wants,omitempty"`
	Has   *Perms `json:"has,omitempty"`
	// Login: login_join, the login's ID within App.
	Login string `json:"login,omitempty"`
}

// Needs is the carried state "needs" (GET /api/needs): the pending ones, oldest first.
type Needs struct {
	Pending []Need `json:"pending"`
}

// NeedAnswer is a person's answer to a Need (POST /api/needs/{id}).
type NeedAnswer struct {
	Allow bool `json:"allow"`
	// Value: secret_set's value, typed by the person; stored, never given back.
	Value string `json:"value,omitempty"`
	// ForSession: secret_use allowed for the rest of the session (till it stops).
	ForSession bool `json:"forSession,omitempty"`
	// Owner: app_join allowed as the owner's own: the app has Claude. Only one that has Claude says so.
	Owner bool `json:"owner,omitempty"`
	// Name: app_join, app_update allowed as a new app by this name in place of the one asked for.
	Name string `json:"name,omitempty"`
	// Perms: app_join, app_change, what it gets: within Need.Wants. nil: as asked.
	Perms *Perms `json:"perms,omitempty"`
	// AgentsOff: app_join, the agents it carries (Need.Pair's) turned off there from the start.
	AgentsOff []string `json:"agentsOff,omitempty"`
}

// PairRequest is what an app says of itself asking to pair (POST /api/pair; no login): its name,
// what it is shown as, and what it wants. An executor (ir join) says its machine too. Shell is
// what it means to say in its hello (Computer's), so the card tells whether command lines would run
// there; an older one says NoShell and AgentShell instead (shell: AgentShell without NoShell),
// which the Iridium turns into Shell. Home and MachineID, as its hello will say them: one with
// agents gets its home as its scope unless it asks for another (decision 17); one on the host
// (its MachineID the host's) carries none.
type PairRequest struct {
	Name       string      `json:"name"`
	Type       string      `json:"type,omitempty"` // computer, bot, script…: display only
	OS         string      `json:"os,omitempty"`   // windows | darwin | linux
	Arch       string      `json:"arch,omitempty"`
	Program    string      `json:"program,omitempty"`
	Agents     []ExecAgent `json:"agents,omitempty"`
	Shell      *bool       `json:"shell,omitempty"`
	NoShell    bool        `json:"noShell,omitempty"`
	AgentShell bool        `json:"agentShell,omitempty"`
	Home       string      `json:"home,omitempty"`
	MachineID  string      `json:"machineId,omitempty"`
	Wants      *Perms      `json:"wants,omitempty"` // nil: all; one carrying agents, its home
}

// ExecAgent is an agent an executor has. Off: turned off at that app (Computer's only).
type ExecAgent struct {
	Agent   string `json:"agent"`
	Version string `json:"version,omitempty"`
	Off     bool   `json:"off,omitempty"`
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

// Pairing answers a pairing request: the code to show, and the request to ask after.
type Pairing struct {
	Code    string `json:"code"`
	Request string `json:"request"`
}

// PairState is how a pairing request stands (GET /api/pair/{request}): pending, allowed (with the
// TOKEN, given once), denied or expired.
type PairState struct {
	Status string `json:"status"`
	Token  string `json:"token,omitempty"`
}

// PairWindow is whether apps may ask to pair now (GET /api/pairing, the carried state "pairing"):
// open till Until (ms), else closed (0). PUT /api/pairing opens it for Minutes (10 if 0, at most
// 60), or closes it.
type PairWindow struct {
	Open    bool  `json:"open"`
	Until   int64 `json:"until,omitempty"`
	Minutes int   `json:"minutes,omitempty"`
}

// NeedState is how a Need went, as the asker sees it: never a secret.
type NeedState struct {
	ID        string   `json:"id"`
	Kind      NeedKind `json:"kind"`
	State     string   `json:"state"` // pending | allowed | denied | expired | failed
	Name      string   `json:"name,omitempty"`
	Owner     bool     `json:"owner,omitempty"`     // it was asked with --claude
	ExpiresAt int64    `json:"expiresAt,omitempty"` // allowed invite: until when the link works
	Error     string   `json:"error,omitempty"`
}

// NeedReply answers the one that allowed a Need: the secret is for it alone.
type NeedReply struct {
	NeedState `tstype:",extends"`
	Invite    *Invite `json:"invite,omitempty"`
}

// Pending is the reply to what must wait for a person (an agent's ir invite, an app's change): 202.
type Pending struct {
	Need Need `json:"need"`
}

// CatalogueEntry is an official app Iridium lists (GET /api/catalogue): what it is and how to
// install it, by system. Iridium installs nothing itself.
type CatalogueEntry struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Type        string        `json:"type"` // as its app is shown once paired
	Description Text          `json:"description"`
	Install     []InstallStep `json:"install"`
	URL         string        `json:"url,omitempty"` // where to read more
}

// InstallStep is a command that installs a catalogue entry on OS (windows | darwin | linux; "" any).
type InstallStep struct {
	OS      string `json:"os,omitempty"`
	Command string `json:"command"`
}

// Text is a short text in Chinese and English.
type Text struct {
	Zh string `json:"zh"`
	En string `json:"en"`
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
