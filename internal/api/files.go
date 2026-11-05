package api

// FileRef is a file a session's transcript names (in a reply, a message, a Write call) or a
// message's attachment: GET /api/sessions/<session>/files/<ref>. A ref is only good for its
// session, and only paths the session's transcript names (or its attachments) get one.
type FileRef struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
	// Kind: image (a thumbnail at .../thumb), video, audio, or file (a download card).
	Kind  FileKind `json:"kind"`
	Thumb bool     `json:"thumb,omitempty"` // GET .../<ref>/thumb gives a thumbnail
	Mtime int64    `json:"mtime"`           // ms since the epoch
	// Sha256 (hex) where it has been worked out: always from .../info and an upload's complete.
	Sha256 string `json:"sha256,omitempty"`
}

type FileKind string

const (
	FileKindImage FileKind = "image"
	FileKindVideo FileKind = "video"
	FileKindAudio FileKind = "audio"
	FileKindFile  FileKind = "file"
)

// NewUpload starts an upload into the session's working directory (.iridium/uploads/).
type NewUpload struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Upload is an upload's progress. Received: the bytes the server has, where the next chunk's
// offset must be. Max: the largest file allowed.
type Upload struct {
	ID       string   `json:"id"`
	Session  string   `json:"session"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Received int64    `json:"received"`
	Done     bool     `json:"done"`
	Max      int64    `json:"max"`
	File     *FileRef `json:"file,omitempty"` // once done
}

// CompleteUpload ends an upload: the server checks the whole file's sha256 (hex).
type CompleteUpload struct {
	Sha256 string `json:"sha256"`
}

// AttachPath makes a local file an attachment of the session (local ir only).
type AttachPath struct {
	Path string `json:"path"`
}

// DirEntry is one entry of GET /api/files?path=<dir> (local ir only): one level.
type DirEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // ms since the epoch
	Type  string `json:"type"`  // file | dir | link | other
}

type DirListing struct {
	Path    string     `json:"path"`
	Entries []DirEntry `json:"entries"`
}

// Copy copies a file (local ir only): POST /api/files/copy streams CopyProgress lines.
type Copy struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// CopyProgress is one line of a copy's stream: progress (Done of Size), then the last line with
// Path and Sha256, or Error.
type CopyProgress struct {
	Done   int64  `json:"done"`
	Size   int64  `json:"size"`
	Path   string `json:"path,omitempty"`
	Sha256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}
