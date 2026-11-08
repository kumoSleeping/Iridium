package api

// Workspace is a folder group: the folder sessions are in, made as the first goes there (the user,
// 2026-11-08: no workspaces made by hand). The carried state "workspaces" is Workspace[], those
// with no session shown too; a session's Workspace 0 is none (未归类: one with no folder).
type Workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// WorkspacePatch renames a group.
type WorkspacePatch struct {
	Name string `json:"name"`
}

// SessionPatch changes what lies over a session; a field left out stays. Name "" takes the name
// off. Workspace moves it (0: none), and it is never moved by discovery again. Color "" takes the
// colour tag off.
type SessionPatch struct {
	Name      *string `json:"name,omitempty"`
	Pinned    *bool   `json:"pinned,omitempty"`
	Archived  *bool   `json:"archived,omitempty"`
	Workspace *int64  `json:"workspace,omitempty"`
	Color     *Color  `json:"color,omitempty"`
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
