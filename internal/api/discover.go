package api

// Workspace is a group of sessions, made from a folder. The carried state "workspaces" is
// Workspace[]; a session's Workspace 0 is none (未归类).
type Workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// NewWorkspace makes a workspace of folder Path; Name "" is the folder's name.
type NewWorkspace struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// WorkspacePatch renames a workspace.
type WorkspacePatch struct {
	Name string `json:"name"`
}

// SessionPatch changes what lies over a session; a field left out stays. Name "" takes the name
// off. Workspace moves it (0: none), and it is never moved by discovery again.
type SessionPatch struct {
	Name      *string `json:"name,omitempty"`
	Pinned    *bool   `json:"pinned,omitempty"`
	Archived  *bool   `json:"archived,omitempty"`
	Workspace *int64  `json:"workspace,omitempty"`
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
}

// FolderReply is the folder made.
type FolderReply struct {
	Path string `json:"path"`
}
