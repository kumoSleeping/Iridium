package api

// Usage is GET /api/usage (ir usage): the tokens used in the last days, today the last, one row
// per local day, terminal, agent and model that used any. A terminal that does not manage sees
// only its own.
type Usage struct {
	Days []string   `json:"days"` // "2006-01-02", oldest first, today last; by hour "2006-01-02T15"
	Rows []UsageRow `json:"rows"`
	// Terminals: the rows' terminals still there, by ID, to name them (a removed one is not).
	Terminals map[string]UsageTerminal `json:"terminals"`
}

// UsageRow is one day's use by one terminal of one agent's model. Turns counts each turn once per
// model it used.
type UsageRow struct {
	Day        string `json:"day"`
	Terminal   string `json:"terminal"` // its ID; "" when who began the turns is not known
	Agent      Agent  `json:"agent"`
	Model      string `json:"model"`
	Turns      int    `json:"turns"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	// CostUSD: what the agent said the turns cost (Claude's); 0 when it says nothing.
	CostUSD float64 `json:"costUsd,omitempty"`
}

// UsageTerminal names a terminal in Usage.
type UsageTerminal struct {
	Name   string       `json:"name"`
	Kind   TerminalKind `json:"kind"`
	Avatar string       `json:"avatar,omitempty"`
	Local  bool         `json:"local,omitempty"`
}
