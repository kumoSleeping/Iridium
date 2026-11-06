package api

// SecretScope is which sessions may use a secret: all, those in some workspaces, or only those the
// owner's own terminals (本机, those that have Claude) opened.
type SecretScope string

const (
	ScopeAll        SecretScope = "all"
	ScopeWorkspaces SecretScope = "workspaces"
	ScopeOwner      SecretScope = "owner"
)

// SecretInfo is a secret as listed: never its value.
type SecretInfo struct {
	Name       string      `json:"name"`
	Note       string      `json:"note,omitempty"`
	Scope      SecretScope `json:"scope"`
	Workspaces []int64     `json:"workspaces"` // scope workspaces: which
	// Always: every use is confirmed on a panel, the owner's own sessions' too.
	Always    bool       `json:"always,omitempty"`
	By        string     `json:"by,omitempty"` // the terminal that stored it, by name
	CreatedAt int64      `json:"createdAt"`
	UpdatedAt int64      `json:"updatedAt"`
	Uses      int        `json:"uses"`
	LastUse   *SecretUse `json:"lastUse,omitempty"`
}

// SecretUse is one use by ir run: which session, from which terminal, what command (its opening,
// as written: {{NAME}}, never the value).
type SecretUse struct {
	Secret   string `json:"secret"`
	Session  string `json:"session,omitempty"`
	Terminal string `json:"terminal"` // by name
	Command  string `json:"command"`
	At       int64  `json:"at"`
}

// Secrets is GET /api/secrets: every secret by name, and the newest uses, newest first.
type Secrets struct {
	Secrets []SecretInfo `json:"secrets"`
	Uses    []SecretUse  `json:"uses"`
}

// SetSecret is PUT /api/secrets/{name}. Value "": one stored keeps its value (only what is said of
// it changes); from an agent, a panel types it (202 Pending). Note, Scope, Always nil or "": as
// they were (a new one: no note, all, false). Only a panel changes Scope and Always.
type SetSecret struct {
	Value      string      `json:"value,omitempty"`
	Note       *string     `json:"note,omitempty"`
	Scope      SecretScope `json:"scope,omitempty"`
	Workspaces []int64     `json:"workspaces,omitempty"`
	Always     *bool       `json:"always,omitempty"`
}

// UseSecrets is POST /api/secrets/use, for ir run alone: the session it runs in, the secrets it
// wants and the command's opening. Confirm: a secret-use confirmation that was allowed, to use it.
type UseSecrets struct {
	Session string   `json:"session,omitempty"`
	Names   []string `json:"names"`
	Command string   `json:"command"`
	Confirm string   `json:"confirm,omitempty"`
}

// SecretValues is the reply to UseSecrets: the values, by name, for the ir process alone.
type SecretValues struct {
	Values map[string]string `json:"values"`
}
