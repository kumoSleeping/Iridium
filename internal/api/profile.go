package api

// Profiles, roles and memory (docs/channels.md "资料", "角色", "记忆").

// The kinds of card.
const (
	CardKindAgent   = "agent"
	CardKindPerson  = "person"
	CardKindApp     = "app"
	CardKindChannel = "channel"
)

// Profile is the card of a member or of a channel. ID: an agent's member ("agent:<session>"), a
// person's or an app's user id, a channel's id. Name: what it is called by (an agent's nick, a
// login's name, a channel's name). Avatar, Background: where the image is fetched ("/v1/…"), or
// an emoji, or a colour ("#2f4f6f"); "" none. Persona: an agent's own words on how it works,
// in its system prompt after its intro (otae, 2026-11-21). Model: what an agent runs as. Role:
// the role its session was started as.
type Profile struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	Intro      string `json:"intro,omitempty"`
	Persona    string `json:"persona,omitempty"`
	Avatar     string `json:"avatar,omitempty"`
	Background string `json:"background,omitempty"`
	Session    string `json:"session,omitempty"`
	Agent      Agent  `json:"agent,omitempty"`
	Model      string `json:"model,omitempty"`
	Status     Status `json:"status,omitempty"`
	Role       string `json:"role,omitempty"`
	// Mine: the caller may change it.
	Mine bool `json:"mine,omitempty"`
}

// ProfilePatch changes a profile; a nil field stays. Nick and Name are the same thing (an
// agent's is its nick, a channel's its name). Avatar, Background: an image (a data: URL or
// base64; for the host's own ir, a file's path), an emoji, a colour ("#2f4f6f"), or "" for none.
// Persona: an agent's alone.
type ProfilePatch struct {
	Nick       *string `json:"nick,omitempty"`
	Name       *string `json:"name,omitempty"`
	Intro      *string `json:"intro,omitempty"`
	Persona    *string `json:"persona,omitempty"`
	Avatar     *string `json:"avatar,omitempty"`
	Background *string `json:"background,omitempty"`
}

// IntroMax is how many characters an intro may have; PersonaMax, a persona; MemoryMax, a memory.
const (
	IntroMax   = 200
	PersonaMax = 5000
	MemoryMax  = 300
)

// Self is what an agent's ir is told of itself (GET internal/me): its card, the channels it is
// in, the contexts it had before it was cleared, what it chose to remember. A person gets the
// card alone.
type Self struct {
	Profile `tstype:",extends"`
	Channels []MeChannel `json:"channels,omitempty"`
	Before   []MeBefore  `json:"before,omitempty"`
	Memory   []Memory    `json:"memory,omitempty"`
}

// MeChannel is a channel the agent is in: when it last spoke there (ms; 0 never), the others
// who spoke around then, the head of what it said.
type MeChannel struct {
	Channel string   `json:"channel"`
	Name    string   `json:"name,omitempty"`
	Own     bool     `json:"own,omitempty"`
	At      int64    `json:"at,omitempty"`
	With    []string `json:"with,omitempty"`
	Last    string   `json:"last,omitempty"`
}

// MeBefore is a context the agent had before: the session it was, when it was cleared (ms),
// how many turns it had, the head of its first message.
type MeBefore struct {
	Session string `json:"session"`
	At      int64  `json:"at"`
	Turns   int    `json:"turns,omitempty"`
	First   string `json:"first,omitempty"`
}

// Memory is something an agent chose to remember (POST internal/remember). Session, Channel,
// Seq: where it is from: the session that wrote it, the channel it was working for and how far
// that channel had come.
type Memory struct {
	ID      int64  `json:"id"`
	Text    string `json:"text"`
	Session string `json:"session,omitempty"`
	Channel string `json:"channel,omitempty"`
	Name    string `json:"name,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
	At      int64  `json:"at"`
}

// Remember is a memory to keep.
type Remember struct {
	Text string `json:"text"`
}

// Recall asks what the records say of Q (POST internal/recall): the messages of the channels
// the agent is in, its earlier contexts' channels, its memory. All: every channel it sees.
type Recall struct {
	Q     string `json:"q"`
	All   bool   `json:"all,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// RecallHit is one thing found: Kind "message" (Channel, Seq: read it whole with Get) or
// "memory" (ID). Head: its first words.
type RecallHit struct {
	Kind    string `json:"kind"`
	Channel string `json:"channel,omitempty"`
	Name    string `json:"name,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
	ID      int64  `json:"id,omitempty"`
	Who     string `json:"who,omitempty"`
	At      int64  `json:"at"`
	Head    string `json:"head"`
	Get     string `json:"get,omitempty"`
}

// MemberCard is a member looked up (GET internal/members/{who}): its card, the channels the
// caller shares with it, the last things it said there.
type MemberCard struct {
	Profile `tstype:",extends"`
	Channels []MeChannel `json:"channels,omitempty"`
	Said     []RecallHit `json:"said,omitempty"`
}

// Role is an agent kept to start sessions as: its name (the nick its sessions take), what it
// runs (Agent, its default Model and dsh's Preset, at Machine), its card (Intro, Persona, Avatar,
// Background as Profile's). ModelName: the model as shown. No effort and no mode: they are the
// task's, chosen as a session starts (otae, 2026-11-21).
type Role struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Agent      Agent  `json:"agent"`
	Model      string `json:"model,omitempty"`
	ModelName  string `json:"modelName,omitempty"`
	Preset     string `json:"preset,omitempty"`
	Machine    string `json:"machine,omitempty"`
	Intro      string `json:"intro,omitempty"`
	Persona    string `json:"persona,omitempty"`
	Avatar     string `json:"avatar,omitempty"`
	Background string `json:"background,omitempty"`
	Memories   int    `json:"memories,omitempty"`
}

// HostPersona is a persona an agent's host has where it runs, to take as a persona of
// Iridium's (GET internal/personas; docs/exec.md agent.personas): all of it from the host, none
// when no host gives any. Machine: where (""; this computer), MachineName as shown; Text, the
// whole of it; From, where it is from, as the host names it.
type HostPersona struct {
	Machine     string `json:"machine,omitempty"`
	MachineName string `json:"machineName,omitempty"`
	Agent       Agent  `json:"agent"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Text        string `json:"text"`
	From        string `json:"from,omitempty"`
}

// NewRole keeps a role: from Session (its agent, model, preset and card as they are now; the
// session is the role's from then on), else from the fields given (Name and Agent at least).
type NewRole struct {
	Session string `json:"session,omitempty"`
	Role `tstype:",extends"`
}

// RolePatch changes a role; a nil field stays. Avatar, Background as ProfilePatch's. Session:
// take the card from this session, as it is now (its name, intro, persona, face, background;
// not its model or preset: those change here alone).
type RolePatch struct {
	Name       *string  `json:"name,omitempty"`
	Model      *string  `json:"model,omitempty"`
	Preset     *string  `json:"preset,omitempty"`
	Intro      *string  `json:"intro,omitempty"`
	Persona    *string  `json:"persona,omitempty"`
	Avatar     *string  `json:"avatar,omitempty"`
	Background *string  `json:"background,omitempty"`
	Sort       *float64 `json:"sort,omitempty"`
	Session    string   `json:"session,omitempty"`
}
