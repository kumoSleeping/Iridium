package api

// Workspace is a folder group (Satori's guild): the folder sessions are in, made as the first goes
// there (the user, 2026-11-08: no workspaces made by hand); a session's Workspace 0 is none (未归类:
// one with no folder).
type Workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	// Only: it holds only the sessions right in its folder (/, a home, a drive's root at another
	// machine, one split), none in a folder under it.
	Only bool `json:"only,omitempty"`
}

// Guilds is the carried state "guilds": the folder groups in the order they were dragged to (those
// never dragged after them, newest last), those with no session shown too; and the folders marked
// by hand.
type Guilds struct {
	Guilds  []Workspace  `json:"guilds"`
	Folders []FolderMark `json:"folders"`
}

// FolderMark is what was put on a folder by hand, by its path as grouped (Session's cwd, or
// "<machine>:<cwd>"): its colour tag; pinned first in its group; Order, the key it was dragged to
// among those in its group (as Session.Order), else it goes by its latest session's activity.
// Satori's channel.list gives each folder sessions are in as a category channel (folder:<path>)
// with its mark in _data.
type FolderMark struct {
	Path   string  `json:"path"`
	Color  Color   `json:"color,omitempty"`
	Pinned bool    `json:"pinned,omitempty"`
	Order  float64 `json:"order,omitempty"`
}

// FolderPatch marks a folder (Satori's channel.update of its category, _data); a field left out
// stays. Color "" takes the tag off (a folder here only: an absolute path); Order 0 the key.
type FolderPatch struct {
	Color  *Color   `json:"color,omitempty"`
	Pinned *bool    `json:"pinned,omitempty"`
	Order  *float64 `json:"order,omitempty"`
}

// GuildPatch changes a folder group (PATCH /v1/internal/guilds/{id}); a field left out stays.
// Position: its place among the groups, 0 first (as the carried "guilds" lists them). Split takes
// it apart at its folder: its subfolders groups of their own.
type GuildPatch struct {
	Name     *string `json:"name,omitempty"`
	Position *int    `json:"position,omitempty"`
	Split    bool    `json:"split,omitempty"`
}

// SessionPatch changes what lies over a session; a field left out stays. Name "" takes the name
// off. Workspace moves it (0: none), and it is never moved by discovery again. Color "" takes the
// colour tag off. ByAgent: Name is the session's agent's (ir rename from a session), which does
// not replace a name the user gave (409) and is cleaned as a title line is. Order: the key it was
// dragged to among those beside it (Session.Order); 0 takes it off. Archived archives (or brings
// back) the sessions hung under it too, all the way down. Detach takes it out from under its
// launcher: it goes in its own folder's group, put there by hand, so it is Own from now on.
type SessionPatch struct {
	Name      *string  `json:"name,omitempty"`
	ByAgent   bool     `json:"byAgent,omitempty"`
	Pinned    *bool    `json:"pinned,omitempty"`
	Archived  *bool    `json:"archived,omitempty"`
	Workspace *int64   `json:"workspace,omitempty"`
	Color     *Color   `json:"color,omitempty"`
	Order     *float64 `json:"order,omitempty"`
	Detach    *bool    `json:"detach,omitempty"`
}

// Folders is one folder's subfolders for the picker: names only, no hidden ones, sorted. Parent
// is "" at the top of what may be listed.
type Folders struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent,omitempty"`
	Folders []string `json:"folders"`
}

// NewFolder makes folder Name in Path.
type NewFolder struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Machine: at another machine's executor (its terminal ID); its paths '/'-separated, as
	// GET /v1/internal/folders?machine= gives them.
	Machine string `json:"machine,omitempty"`
}

// FolderReply is the folder made.
type FolderReply struct {
	Path string `json:"path"`
}
