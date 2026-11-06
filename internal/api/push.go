package api

// PushState is the Web Push settings: the total switch (off by default), the VAPID public key a
// page subscribes with, the events pushed where no rule says otherwise, and the rules.
type PushState struct {
	Enabled   bool       `json:"enabled"`
	PublicKey string     `json:"publicKey"` // base64url, uncompressed P-256 point
	Defaults  PushEvents `json:"defaults"`
	Rules     []PushRule `json:"rules"`
}

// PushEvents are the events pushed. Default: NeedsYou and Error on, the rest off.
type PushEvents struct {
	NeedsYou bool `json:"needsYou"`
	Error    bool `json:"error"`
	TurnEnd  bool `json:"turnEnd"`
	Compact  bool `json:"compact"`
}

// PushScope is what a push rule is on.
type PushScope string

const (
	PushScopeWorkspace PushScope = "workspace"
	PushScopeFolder    PushScope = "folder"
	PushScopeSession   PushScope = "session"
)

// PushMode is a push rule's say: on or off; inherit (PUT, or ""): as above, the rule dropped.
type PushMode string

const (
	PushModeOn      PushMode = "on"
	PushModeOff     PushMode = "off"
	PushModeInherit PushMode = "inherit"
)

// PushRule is what a workspace, a folder or a session says over the defaults. A session's event
// goes by the nearest rule of its session, its folder (its cwd), its workspace: off pushes nothing;
// on pushes the events of the nearest of them with events of their own, else the defaults.
type PushRule struct {
	Scope  PushScope   `json:"scope"`
	Target string      `json:"target"` // workspace: its id ("0": sessions in none); folder: its absolute path; session: its id
	Mode   PushMode    `json:"mode"`
	Events *PushEvents `json:"events,omitempty"` // with on: its own; none: the ones from above
}

type PushSwitch struct {
	Enabled bool `json:"enabled"`
}

// PushSubscription is a browser's PushSubscription.toJSON().
type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PushUnsubscribe drops the subscription at Endpoint.
type PushUnsubscribe struct {
	Endpoint string `json:"endpoint"`
}

// PushMessage is a push's payload, as the service worker gets it.
type PushMessage struct {
	Kind    string `json:"kind"` // needs_you | error | turn_end | compact | test
	Session string `json:"session,omitempty"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Tag     string `json:"tag,omitempty"`
}
