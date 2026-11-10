package satori

import (
	"encoding/json"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Satori's resources (satorijs/docs@50fee9ef), as Iridium fills them in: docs/satori.md.

// Opcodes of the WebSocket's signals. Iridium's own, if any, are 6 and up (decision 26).
const (
	OpEvent    = 0
	OpPing     = 1
	OpPong     = 2
	OpIdentify = 3
	OpReady    = 4
	OpMeta     = 5
	// Iridium's own (decision 26): WATCH (to it: {keys}) names the states a stream carries,
	// STATE (from it: {type: "state"|"draft", data}) carries them. Not EVENTs: not kept, no sn.
	OpWatch = 6
	OpState = 7
)

// Login statuses.
const (
	StatusOffline = 0
	StatusOnline  = 1
)

// Channel types.
const (
	ChannelText = 0
)

// Signal is one WebSocket frame.
type Signal struct {
	Op   int             `json:"op"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Identify is IDENTIFY's body.
type Identify struct {
	Token string `json:"token,omitempty"`
	SN    int64  `json:"sn,omitempty"`
	// Watch: Iridium's own: the states to carry from the start (as WATCH).
	Watch []string `json:"_watch,omitempty"`
}

// Watch is WATCH's body.
type Watch struct {
	Keys []string `json:"keys"`
}

// State is STATE's body: a state (api.StateEvent) or a draft (api.DraftEvent).
type State struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Ready is READY's body.
type Ready struct {
	Logins    []Login  `json:"logins"`
	ProxyURLs []string `json:"proxy_urls"`
}

// Meta is POST /v1/meta's reply.
type Meta = Ready

// Login is one login of the app: platform is the app's ID, user.id the login's.
type Login struct {
	SN       int64    `json:"sn"`
	Platform string   `json:"platform,omitempty"`
	User     *User    `json:"user,omitempty"`
	Status   *int     `json:"status,omitempty"`
	Adapter  string   `json:"adapter,omitempty"`
	Features []string `json:"features,omitempty"`
}

// User is an agent (is_bot) or someone who spoke: a login, an app, the host.
type User struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Nick   string `json:"nick,omitempty"`
	Avatar string `json:"avatar,omitempty"`
	IsBot  bool   `json:"is_bot,omitempty"`
}

// Channel is a session.
type Channel struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	Name     string `json:"name,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
}

// Guild is a folder group (the web's workspace), or Loose for the sessions in none.
type Guild struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

// Message is a block of a session's transcript (id: its place, "<offset>.<index>"), something
// said live (the same id once it is in the transcript), or an ask ("ask:<session>:<request>").
type Message struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Channel   *Channel `json:"channel,omitempty"`
	Guild     *Guild   `json:"guild,omitempty"`
	User      *User    `json:"user,omitempty"`
	CreatedAt int64    `json:"created_at,omitempty"`
	UpdatedAt int64    `json:"updated_at,omitempty"`
	// Data: Iridium's own (Satori's _data): message.create's send result.
	Data any `json:"_data,omitempty"`
}

// Button is an interaction/button event's button.
type Button struct {
	ID string `json:"id"`
}

// Event is an EVENT's body.
type Event struct {
	SN        int64    `json:"sn"`
	Type      string   `json:"type"`
	Timestamp int64    `json:"timestamp"`
	Login     *Login   `json:"login,omitempty"`
	Channel   *Channel `json:"channel,omitempty"`
	Guild     *Guild   `json:"guild,omitempty"`
	Message   *Message `json:"message,omitempty"`
	User      *User    `json:"user,omitempty"`
	Operator  *User    `json:"operator,omitempty"`
	Button    *Button  `json:"button,omitempty"`
	// Iridium's own (Satori's extension fields): _type "iridium", _data an EventData.
	XType string     `json:"_type,omitempty"`
	XData *EventData `json:"_data,omitempty"`
}

// EventData is what Iridium adds to an event: what the message is (reply, progress, ask, error,
// user), and the app or login of ir_web that began the turn, if known. ir.share's: kind share and
// the Share.
type EventData struct {
	Kind   string     `json:"kind,omitempty"`
	Origin string     `json:"origin,omitempty"`
	Share  *api.Share `json:"share,omitempty"`
}

// List is a paged list.
type List[T any] struct {
	Data []T    `json:"data"`
	Next string `json:"next,omitempty"`
}

// BidiList is a list paged both ways.
type BidiList[T any] struct {
	Data []T    `json:"data"`
	Prev string `json:"prev,omitempty"`
	Next string `json:"next,omitempty"`
}
