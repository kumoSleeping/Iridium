package api

// UpdateState is the carried state "update" and GET /api/update: whether a newer Iridium is out
// (plan "更新"). The page puts a dot on the settings gear while Available.
type UpdateState struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Available bool   `json:"available"`
	Source    bool   `json:"source"`              // an update source is configured
	CheckedAt int64  `json:"checkedAt,omitempty"` // ms
	// Phase: idle, checking, downloading, restarting or failed (Error says why).
	Phase string `json:"phase"`
	Error string `json:"error,omitempty"`
}

// ApplyUpdate is POST /api/update. Force installs the latest even when it is not newer (a dev
// build is never older than anything).
type ApplyUpdate struct {
	Force bool `json:"force,omitempty"`
}
