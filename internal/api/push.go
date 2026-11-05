package api

// PushState is the Web Push settings: the total switch (off by default), the VAPID public key a
// page subscribes with, and each workspace's events (workspace 0: sessions in none).
type PushState struct {
	Enabled    bool            `json:"enabled"`
	PublicKey  string          `json:"publicKey"` // base64url, uncompressed P-256 point
	Workspaces []PushWorkspace `json:"workspaces"`
}

// PushEvents are the events a workspace pushes. Default: NeedsYou and Error on, the rest off.
type PushEvents struct {
	NeedsYou bool `json:"needsYou"`
	Error    bool `json:"error"`
	TurnEnd  bool `json:"turnEnd"`
	Compact  bool `json:"compact"`
}

type PushWorkspace struct {
	Workspace  int64 `json:"workspace"`
	PushEvents `tstype:",extends"`
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
