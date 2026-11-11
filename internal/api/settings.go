package api

// Settings is GET /v1/internal/settings: the config file's fields the settings page edits (plan "运行",
// "推送", "网页"). PUT /v1/internal/settings takes a SettingsPatch and answers the whole again.
type Settings struct {
	// IdleReap: a hosted agent idle this long is stopped (its session stays), as "1h"; "0" never.
	IdleReap string `json:"idleReap"`
	// CrashResume and QuotaResume: go on with a turn after the agent crashed, or its quota came back.
	CrashResume bool `json:"crashResume"`
	QuotaResume bool `json:"quotaResume"`
	// QuotaHidden: the quota bars not shown, each "<agent>:<account>:<group>:<model>".
	QuotaHidden []string `json:"quotaHidden"`
	// Modes: the approval mode a new session of each agent starts in; an agent left out starts in
	// its own default.
	Modes map[string]string `json:"modes"`
	// Push: Web Push's total switch (off by default); GET /v1/internal/web/push has the rest.
	Push bool `json:"push"`
	// EnterInTurn: what Enter does with words while a turn runs: "steer" (the default) puts them
	// into the turn, Ctrl or ⌘+Enter queues them; "queue" the other way round.
	EnterInTurn string `json:"enterInTurn"`
	// Lang: the user's language, as a web page's settings last set it ("zh", "en"; "" not yet):
	// hosted agents are told it from their next start.
	Lang string `json:"lang"`
	// AutoUpdate: Iridium looks for a newer version by itself and downloads it ahead (on by
	// default); off, only 检查更新 looks (config update.manual).
	AutoUpdate bool `json:"autoUpdate"`
}

// SettingsPatch changes what it gives and nothing else. A mode "" goes back to the agent's own.
type SettingsPatch struct {
	IdleReap    *string           `json:"idleReap,omitempty"`
	CrashResume *bool             `json:"crashResume,omitempty"`
	QuotaResume *bool             `json:"quotaResume,omitempty"`
	QuotaHidden *[]string         `json:"quotaHidden,omitempty"`
	Modes       map[string]string `json:"modes,omitempty"`
	Push        *bool             `json:"push,omitempty"`
	EnterInTurn *string           `json:"enterInTurn,omitempty"`
	Lang        *string           `json:"lang,omitempty"`
	AutoUpdate  *bool             `json:"autoUpdate,omitempty"`
}
