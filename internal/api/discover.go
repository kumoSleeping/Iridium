package api

// Workspace is a folder group: the folder sessions are in, made as the first goes there (the user,
// 2026-11-08: no workspaces made by hand). The carried state "workspaces" is Workspace[], those
// with no session shown too; a session's Workspace 0 is none (未归类: one with no folder).
type Workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	// Hidden: hidden from the page (a group's menu): its sessions are not listed, searched or in
	// 归档, but while one of them runs elsewhere (open in DSH Desktop).
	Hidden bool `json:"hidden,omitempty"`
}

// WorkspaceOrder lists groups in the order they were dragged to (PUT /api/workspaces/order): IDs
// first to last, before any left out.
type WorkspaceOrder struct {
	IDs []int64 `json:"ids"`
}

// FolderPlaces is where the folders in the list were put, by path as grouped (Session's cwd, or
// "<machine>:<cwd>"): the carried state "folderPlaces". Order: the key each folder dragged is
// sorted by among those in its group (as Session.Order); one not in it, by its latest session's
// activity. Pinned: those pinned, first in their group.
type FolderPlaces struct {
	Order  map[string]float64 `json:"order"`
	Pinned []string           `json:"pinned"`
}

// FolderPlace puts a folder (PATCH /api/folders/place); a field left out stays. Order 0 takes its
// key off.
type FolderPlace struct {
	Path   string   `json:"path"`
	Order  *float64 `json:"order,omitempty"`
	Pinned *bool    `json:"pinned,omitempty"`
}

// WorkspacePatch renames a group, hides it or shows it again; a field left out stays.
type WorkspacePatch struct {
	Name   *string `json:"name,omitempty"`
	Hidden *bool   `json:"hidden,omitempty"`
}

// SessionPatch changes what lies over a session; a field left out stays. Name "" takes the name
// off. Workspace moves it (0: none), and it is never moved by discovery again. Color "" takes the
// colour tag off. ByAgent: Name is the session's agent's (ir rename from a session), which does
// not replace a name the user gave (409) and is cleaned as a title line is. Order: the key it was
// dragged to among those beside it (Session.Order); 0 takes it off.
type SessionPatch struct {
	Name      *string  `json:"name,omitempty"`
	ByAgent   bool     `json:"byAgent,omitempty"`
	Pinned    *bool    `json:"pinned,omitempty"`
	Archived  *bool    `json:"archived,omitempty"`
	Workspace *int64   `json:"workspace,omitempty"`
	Color     *Color   `json:"color,omitempty"`
	Order     *float64 `json:"order,omitempty"`
}

// FolderColor tags folder Path (absolute) with a colour; Color "" takes it off. The carried state
// "colors" is every folder's, by path (FolderColors).
type FolderColor struct {
	Path  string `json:"path"`
	Color Color  `json:"color"`
}

// FolderColors is the colour tags of folders, by absolute path.
type FolderColors map[string]Color

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
	// GET /api/folders?machine= gives them.
	Machine string `json:"machine,omitempty"`
}

// FolderReply is the folder made.
type FolderReply struct {
	Path string `json:"path"`
}
