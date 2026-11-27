package api

// Apps and their logins (0.2, docs/next/k_glossary.md "应用"): an app connects with its TOKEN (a
// QQ bridge, a dsh plugin, an executor), ir_web is the built-in one whose logins are browsers with a
// passkey, and the host is the machine Iridium runs on: listed with them, never managed. See
// docs/api.md.

// HostID is the host's storage ID; it is shown as 主机 (host).
const HostID = "local"

// WebApp is the built-in app ir_web's ID: there only with a public origin.
const WebApp = "ir_web"

// App is an app as listed, or the host (Host) or ir_web (Web) beside them. No secrets.
type App struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`   // computer (it paired as one, saying its system), else what it is shown as (bot…)
	Avatar string `json:"avatar,omitempty"` // URL for <img src>
	// Host: the host, listed with the apps but not managed. Web: ir_web, the built-in app.
	Host bool `json:"host,omitempty"`
	// Named: the host has a name and face of its own (Me.Named), drawn instead of Iridium's.
	Named  bool `json:"named,omitempty"`
	Web    bool `json:"web,omitempty"`
	Logins int  `json:"logins,omitempty"` // ir_web: its logins
	// Online: it can be used now, one meaning for every app: a computer's executor connected, a
	// Bot's connection (Connections), ir_web's login online in some browser; the host always.
	// Written out false too.
	Online      bool  `json:"online"`
	Connections int   `json:"connections,omitempty"` // its live connections (a Bot's)
	CreatedAt   int64 `json:"createdAt,omitempty"`
	LastSeenAt  int64 `json:"lastSeenAt,omitempty"`
	// Perms: what an app was given as it paired (nil: all, as one paired before 0.2). Not the
	// host's or ir_web's.
	Perms     *Perms `json:"perms,omitempty"`
	UsedToday int64  `json:"usedToday"` // tokens of the turns it started today
	// Device: what the host runs on, as the badge on its messages' avatar shows; a browser's own
	// (Login.Device). Not another app's: a computer or a Bot has no device (k_glossary "电脑").
	Device Device `json:"device,omitempty"`
	// Computer: a computer (docs/next/k_glossary.md "电脑"): the host's own, or what an app that
	// runs agents or command lines (an executor: ir join, a dsh plugin) last said in its hello;
	// nil for any other app.
	Computer *Computer `json:"computer,omitempty"`
	// Owner: whose computer it is, the login of ir_web that allowed its pairing (or set it later,
	// PUT /v1/internal/apps/{id}/owner); nil for one paired at the host or before 0.2.60, and for
	// any app not a computer.
	Owner *AppOwner `json:"owner,omitempty"`
}

// AppOwner is a computer's owner: a login of ir_web, by its ID and its name now.
type AppOwner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SetOwner is PUT /v1/internal/apps/{id}/owner: the computer is Login's (a login of ir_web, by its
// ID or name; "" the caller's own), or nobody's (Clear).
type SetOwner struct {
	Login string `json:"login,omitempty"`
	Clear bool   `json:"clear,omitempty"`
}

// Computer is a computer: the host's, or an app's as its executor last said of itself
// (docs/exec.md "握手"), kept in the store, so one away is listed too. GET /v1/internal/apps lists
// it with the app, to every caller. Routes are /v1/internal/computers/{id}/… (machines/{id} still
// answers); older code calls it a machine.
type Computer struct {
	OS   string `json:"os"` // windows | darwin | linux
	Arch string `json:"arch,omitempty"`
	Home string `json:"home"`
	Sep  string `json:"sep"`
	// Agents: those Iridium runs there, as it reported them; Off, turned off in its app's page
	// (PUT /v1/internal/apps/{id}/agents): not started there, not offered for new sessions.
	Agents []ExecAgent `json:"agents"`
	// Online: its executor is connected; listed as its app's (App.Online), not here.
	Online bool  `json:"-"`
	SeenAt int64 `json:"seenAt"` // when it last connected (ms)
	// Shell: command lines run there, a person's on the page ($) and agents' (ir sh); else it
	// starts only its agents (docs/exec.md). Its owner's switch.
	Shell bool `json:"shell,omitempty"`
	// Roots: the folders it serves files under (its own paths); none: all but its keys and logins,
	// or not known (Grant false).
	Roots []string `json:"roots,omitempty"`
	// Grant: it takes a grant (POST /v1/internal/machines/{id}/grant): Shell and Roots widened from
	// here, confirmed at its terminal.
	Grant bool `json:"grant,omitempty"`
	// Latency: the connection's last round trip (ms), by its heartbeat; 0 not known.
	Latency int64 `json:"latency,omitempty"`
	// Program: what connects it, as its hello says (ir join and its version).
	Program string `json:"program,omitempty"`
	// Under: the same computer as app Under's, the first paired there (HostID for one on the host):
	// listed next to it; "" the first or only one. By the machine its hello said.
	Under string `json:"under,omitempty"`
}

// TypeComputer is the type of an app that paired as a computer (PairRequest.OS said).
const TypeComputer = "computer"

// AppAgents is PUT /v1/internal/apps/{id}/agents: the agents an app carries that are turned off there.
// Any it reports later is on till turned off.
type AppAgents struct {
	Off []string `json:"off"`
}

// Login is a login of an app as listed: ir_web's, a browser with a passkey; another app's, as it
// spoke (Satori-User-ID), waiting till approved (ApprovedAt 0). No secrets.
type Login struct {
	App      string `json:"app"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar,omitempty"`
	Passkeys int    `json:"passkeys,omitempty"`
	Sessions int    `json:"sessions,omitempty"` // browsers logged in
	// Online: open in some browser now, Connections of them; false written out too.
	Online      bool   `json:"online"`
	Connections int    `json:"connections,omitempty"`
	CreatedAt   int64  `json:"createdAt,omitempty"`
	LastSeenAt  int64  `json:"lastSeenAt,omitempty"`
	ApprovedAt  int64  `json:"approvedAt,omitempty"` // 0: waits to be approved
	UsedToday   int64  `json:"usedToday"`
	Device      Device `json:"device,omitempty"`
	// Perms: what it was given (nil: all); what it may use is this within its app's.
	Perms *Perms `json:"perms,omitempty"`
}

// Device is what an app or login runs on, set in its settings (PUT /v1/internal/me/device, ir apps
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

// SetDevice is PUT /v1/internal/me/device and PUT /v1/internal/apps/{id}/device.
type SetDevice struct {
	Device Device `json:"device"`
}

// Passkey is a passkey of a login of ir_web: one device of it (docs/web.md "Passkey"). No key
// material. ID is its credential ID, base64url.
type Passkey struct {
	ID   string `json:"id"`
	Name string `json:"name"` // its provider (iCloud 钥匙串), else the browser it was made in
	// Device: what it shows as; Auto, not set by hand: by the browser it was last used in.
	Device     Device `json:"device"`
	Auto       bool   `json:"auto,omitempty"`
	Current    bool   `json:"current,omitempty"` // the browser asking is logged in with it
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

// Passkeys is GET /v1/internal/apps/ir_web/logins/{login}/passkeys: its passkeys, oldest first,
// and its links to add one not used yet.
type Passkeys struct {
	Passkeys []Passkey     `json:"passkeys"`
	Links    []PasskeyLink `json:"links"`
}

// PasskeyLink is a link to add a passkey to a login, not used yet (its secret is not listed).
type PasskeyLink struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// PasskeyDevice is PUT …/passkeys/{id}/device: a device, or "" by the browser it is used in.
type PasskeyDevice struct {
	Device Device `json:"device"`
}

// PendingInvite is a slot for a login of ir_web not taken yet.
type PendingInvite struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Setup     bool   `json:"setup,omitempty"` // the first-run link in the state directory
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// Apps is GET /v1/internal/apps (ir apps): the host first, ir_web (with a public origin), the apps; the
// logins of ir_web; the invites not taken. Public (draft 二「消息」): any caller gets the apps
// (names, types, avatars, online, computers) and the approved logins; their perms and
// usage, the logins waiting and the invites only one that manages (Full).
type Apps struct {
	Apps    []App           `json:"apps"`
	Logins  []Login         `json:"logins"`
	Invites []PendingInvite `json:"invites"`
	Full    bool            `json:"full,omitempty"`
}

// Me is GET /v1/internal/me: who calls. App: the app (HostID for the host, WebApp for a browser); Login:
// the login of it, for ir_web the browser's. ID is the one its usage and rules are kept by.
type Me struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	App    string `json:"app"`
	Login  string `json:"login,omitempty"`
	Avatar string `json:"avatar,omitempty"`
	Host   bool   `json:"host,omitempty"`
	// Named: the host has a name and face of its own (the desktop app's user), shown instead of
	// Iridium's.
	Named     bool   `json:"named,omitempty"`
	Web       bool   `json:"web,omitempty"` // a login of ir_web
	UsedToday int64  `json:"usedToday"`
	Device    Device `json:"device,omitempty"`
	Public    bool   `json:"public"` // a public origin is set: passkeys are on
}

// NewInvite asks for a slot for a new login of ir_web. An agent's goes to a confirmation that one
// who may approve (or the host) allows.
type NewInvite struct {
	Name string `json:"name,omitempty"`
	// Perms: what the login that takes it may use (nil: all); set as the slot is made.
	Perms *Perms `json:"perms,omitempty"`
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
// places it may use, each ["*"] for all. Claude is an agent like the others.
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
	// NeedMachineGrant: a computer turned down an agent's request (command lines off there, a
	// path outside its folders): allowed, the computer asks the person at its terminal.
	NeedMachineGrant NeedKind = "machine_grant"
	// NeedAsk: a session's question, approval or plan (Card), for whoever may use the session;
	// answered through the card (POST /v1/internal/button), not POST /v1/internal/needs/{id}.
	NeedAsk NeedKind = "ask"
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
	// machine_grant: the computer (its app's ID; Name its name), and what it would give: command
	// lines (Shell), or the folder Path (its own path).
	Machine string `json:"machine,omitempty"`
	Shell   bool   `json:"shell,omitempty"`
	Path    string `json:"path,omitempty"`
	// App: app_update, the app by that name now (its system, last seen, whether online: an update
	// while it is online may be someone else's); app_change, login_join: the app asking.
	App *App `json:"app,omitempty"`
	// Wants: app_join, app_change: what it asks for; the answer may give less, never more.
	// app_change: Has, what it has now.
	Wants *Perms `json:"wants,omitempty"`
	Has   *Perms `json:"has,omitempty"`
	// Login: login_join, the login's ID within App.
	Login string `json:"login,omitempty"`
	// Card: ask, the ask as a card (its id "ask:<session>:<request>" is the need's). Origin: ask,
	// the app or login of ir_web that began the turn, if known.
	Card   *Card  `json:"card,omitempty"`
	Origin string `json:"origin,omitempty"`
}

// Needs is the carried state "needs" (GET /v1/internal/needs): the pending ones, oldest first, as the
// caller may see them: applications for a person on ir_web or at the host, asks of the sessions it
// may use.
type Needs struct {
	Pending []Need `json:"pending"`
}

// MachineGrant is POST /v1/internal/machines/{id}/grant: command lines on (asked at that
// computer) or off (Shell; nil: as they are), and more folders it serves files under (Roots, its
// own whole paths; asked there).
type MachineGrant struct {
	Shell *bool    `json:"shell,omitempty"`
	Roots []string `json:"roots,omitempty"`
}

// NeedAnswer is a person's answer to a Need (POST /v1/internal/needs/{id}).
type NeedAnswer struct {
	Allow bool `json:"allow"`
	// Value: secret_set's value, typed by the person; stored, never given back.
	Value string `json:"value,omitempty"`
	// ForSession: secret_use allowed for the rest of the session (till it stops).
	ForSession bool `json:"forSession,omitempty"`
	// Name: app_join, app_update allowed as a new app by this name in place of the one asked for.
	Name string `json:"name,omitempty"`
	// Perms: app_join, app_change, what it gets: within Need.Wants. nil: as asked.
	Perms *Perms `json:"perms,omitempty"`
	// AgentsOff: app_join, the agents it carries (Need.Pair's) turned off there from the start.
	AgentsOff []string `json:"agentsOff,omitempty"`
}

// PairRequest is what an app says of itself asking to pair (POST /v1/internal/pair; no login): its name,
// what it is shown as, and what it wants. An executor (ir join) says its machine too. Shell is
// what it means to say in its hello (Computer's), so the card tells whether command lines would run
// there; an older one says NoShell and AgentShell instead (shell: AgentShell without NoShell),
// which the Iridium turns into Shell. Home and MachineID, as its hello will say them: one with
// agents gets its home as its scope unless it asks for another (decision 17); one on the host
// (its MachineID the host's) carries none.
type PairRequest struct {
	Name       string      `json:"name"`
	Type       string      `json:"type,omitempty"` // bot, script…: what an app is shown as; one that says OS is a computer
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
	// Login: the login it speaks as (Satori-User-ID), approved with it; nil: none (ir join).
	Login *PairLogin `json:"login,omitempty"`
}

// PairLogin is the login a pairing app speaks as. Name: ID when not given.
type PairLogin struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ExecAgent is an agent an executor has. Off: turned off at that app (Computer's only).
// Sync: non-nil when the agent supports "sync new sessions" on that computer.
// PathNeeded/Path: manual install path fallback on the host computer when the host app
// is neither connected nor auto-discovered.
type ExecAgent struct {
	Agent      string `json:"agent"`
	Version    string `json:"version,omitempty"`
	Off        bool   `json:"off,omitempty"`
	Sync       *bool  `json:"sync,omitempty"`
	PathNeeded bool   `json:"pathNeeded,omitempty"`
	Path       string `json:"path,omitempty"`
}

// ComputerAgentConfig is PUT /v1/internal/computers/{id}/{agent}/config.
type ComputerAgentConfig struct {
	Sync *bool   `json:"sync,omitempty"`
	Path *string `json:"path,omitempty"`
}

// ComputerAgentState answers PUT /v1/internal/computers/{id}/{agent}/config.
type ComputerAgentState struct {
	Sync       *bool  `json:"sync,omitempty"`
	PathNeeded bool   `json:"pathNeeded,omitempty"`
	Path       string `json:"path,omitempty"`
}

// ExternalSession is an unsynced session found on demand in an external host app
// (GET /v1/internal/external/sessions) and passed to POST /v1/internal/external/adopt.
type ExternalSession struct {
	ID        string `json:"id"`
	Agent     Agent  `json:"agent"`
	Machine   string `json:"machine,omitempty"` // "" = host
	Cwd       string `json:"cwd,omitempty"`
	Title     string `json:"title,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Archived  bool   `json:"archived,omitempty"`
}

// DshWorkspace is a folder a new dsh session at a machine may open in (GET
// /v1/internal/machines/{id}/dsh/workspaces): one of dsh's workspaces there, as DSH Desktop's sidebar
// has them, else a folder its sessions were in. Path is '/'-separated, as GET /v1/internal/folders gives.
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

// PairState is how a pairing request stands (GET /v1/internal/pair/{request}): pending, allowed (with the
// TOKEN, given once), denied or expired.
type PairState struct {
	Status string `json:"status"`
	Token  string `json:"token,omitempty"`
}

// PairWindow is whether apps may ask to pair now (GET /v1/internal/pairing, the carried state "pairing"):
// a switch, on till turned off (PUT /v1/internal/pairing), kept across restarts.
type PairWindow struct {
	Open bool `json:"open"`
}

// NeedState is how a Need went, as the asker sees it: never a secret.
type NeedState struct {
	ID        string   `json:"id"`
	Kind      NeedKind `json:"kind"`
	State     string   `json:"state"` // pending | allowed | denied | expired | failed
	Name      string   `json:"name,omitempty"`
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

// SetPublic sets public.origin ("" turns the public way in off). The gate restarts with it.
type SetPublic struct {
	Origin string `json:"origin"`
}

// PublicState is GET /v1/internal/public.
type PublicState struct {
	Origin   string `json:"origin,omitempty"`
	Passkeys bool   `json:"passkeys"` // some login of ir_web has a passkey
}
