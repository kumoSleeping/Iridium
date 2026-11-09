package api

// Note is a note jotted down (便签, docs/plan.md "便签"): words to keep, an idea, a thing to do.
// In no folder (Folder 0) or in one; listed by Pos, small first. Sessions: those it was put into,
// the latest last. From: the session it was jotted in, if any. Times in ms since the epoch; Done 0
// is not done. The carried state "notes" is Notes, every note whole.
type Note struct {
	ID       string   `json:"id"`
	Folder   int64    `json:"folder,omitempty"`
	Body     string   `json:"body"`
	Pos      float64  `json:"pos"`
	Color    Color    `json:"color,omitempty"`
	Done     int64    `json:"done,omitempty"`
	From     string   `json:"from,omitempty"`
	Sessions []string `json:"sessions,omitempty"`
	Created  int64    `json:"created"`
	Updated  int64    `json:"updated"`
}

// NoteFolder is a folder of notes: one level, no folders in it.
type NoteFolder struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Pos  float64 `json:"pos"`
}

type Notes struct {
	Notes   []Note       `json:"notes"`
	Folders []NoteFolder `json:"folders"`
}

// NewNote makes a note. Pos left out: above all in its folder. ID: the page's own (a UUID), so a
// note made offline keeps it; left out, one is made.
type NewNote struct {
	ID     string   `json:"id,omitempty"`
	Body   string   `json:"body"`
	Folder int64    `json:"folder,omitempty"`
	Pos    *float64 `json:"pos,omitempty"`
	Color  Color    `json:"color,omitempty"`
	From   string   `json:"from,omitempty"`
}

// NotePatch changes a note; a field left out stays. Folder 0 takes it out of its folder; Color ""
// takes the colour off. Session: one it was put into, added to Sessions.
type NotePatch struct {
	Body    *string  `json:"body,omitempty"`
	Folder  *int64   `json:"folder,omitempty"`
	Pos     *float64 `json:"pos,omitempty"`
	Color   *Color   `json:"color,omitempty"`
	Done    *bool    `json:"done,omitempty"`
	Session string   `json:"session,omitempty"`
}

// NoteFolderPatch makes or changes a folder of notes; a field left out stays.
type NoteFolderPatch struct {
	Name *string  `json:"name,omitempty"`
	Pos  *float64 `json:"pos,omitempty"`
}
