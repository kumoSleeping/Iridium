package api

// Official extensions (plan "插件"): plugins Iridium does not build in but knows by name, to
// install from its own release and manage from 终端管理. The QQ bot (iridium-qq) is the one so far.

// Extension is an official extension as GET /api/extensions lists it.
type Extension struct {
	ID        string `json:"id"`   // qq
	Name      string `json:"name"` // what the page calls it
	Installed bool   `json:"installed"`
	// Busy: what is under way: install | bind | start | stop | remove.
	Busy  string `json:"busy,omitempty"`
	Error string `json:"error,omitempty"` // the last action's failure
	// Status is what the extension says of itself (iridium-qq status --json), once installed.
	Status *ExtensionStatus `json:"status,omitempty"`
	// Bind: a scan under way.
	Bind *ExtensionBind `json:"bind,omitempty"`
	// Online: its terminal's live connections in Iridium.
	Online int `json:"online,omitempty"`
}

// ExtensionStatus is iridium-qq status --json.
type ExtensionStatus struct {
	Version string `json:"version"`
	Home    string `json:"home"`
	Bound   bool   `json:"bound"` // it has a bot
	AppID   string `json:"appId,omitempty"`
	Name    string `json:"name"` // its terminal's name in Iridium
	Owners  int    `json:"owners"`
	Iridium string `json:"iridium,omitempty"`
	Token   bool   `json:"token"`
	// Service: systemd's ActiveState of its unit (active, inactive, failed, activating); "" for
	// no unit.
	Service string            `json:"service"`
	Enabled bool              `json:"enabled"`
	Running *ExtensionRunning `json:"running,omitempty"` // while the service is active
	Pair    string            `json:"pair,omitempty"`    // a pairing code waiting
}

// ExtensionRunning is how the running extension stands.
type ExtensionRunning struct {
	PID          int    `json:"pid"`
	QQ           bool   `json:"qq"` // connected to QQ
	QQError      string `json:"qqError,omitempty"`
	Iridium      bool   `json:"iridium"` // its event stream is open
	IridiumError string `json:"iridiumError,omitempty"`
	Bot          string `json:"bot,omitempty"` // the bot's name on QQ
	At           int64  `json:"at"`
}

// ExtensionBind is a scan under way: the page to scan, and its QR code.
type ExtensionBind struct {
	URL string `json:"url"`
	QR  string `json:"qr"` // an SVG data URL
}
