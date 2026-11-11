package api

// FileRef is a file a session's transcript names (in a reply, a message, a Write call) or a
// message's attachment: GET /v1/internal/sessions/<session>/files/<ref>. A ref is only good for its
// session, and only paths the session's transcript names (or its attachments) get one. A file
// link's (FileLink): Ref is its id, good in any session, and Path "ir:<id>", as the reply has it.
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
	// W, H: an image's size in pixels, upright (its EXIF orientation applied), where its header
	// tells: the page keeps its place before it has loaded, nothing moving as it comes.
	W int `json:"w,omitempty"`
	H int `json:"h,omitempty"`
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

// FileLink is POST /v1/internal/links' answer (local ir only, ir attach <path>): the file, kept by
// Iridium under a random id, and how a reply puts it in its words.
type FileLink struct {
	File FileRef `json:"file"`
	Link string  `json:"link"` // ir:<id>
	// Markdown is the link as a reply writes it: ![name](ir:id) for a picture, a video or a sound,
	// which shows where it is; [name](ir:id) for any other file, to download.
	Markdown string `json:"markdown"`
}

// DirEntry is one entry of GET /v1/internal/files?path=<dir> (local ir only): one level.
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

// Copy copies a file (local ir only): POST /v1/internal/files/copy streams CopyProgress lines.
type Copy struct {
	From string `json:"from"`
	To   string `json:"to"`
	// FromMachine, ToMachine: that side is at another machine's executor (a Bot's ID or name),
	// its path as that machine writes it; "": this one. Written there whole, so at most 4 MB.
	FromMachine string `json:"fromMachine,omitempty"`
	ToMachine   string `json:"toMachine,omitempty"`
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

// CLIRun is POST /v1/internal/cli: ir at another machine, run by an agent hosted there (its session's own
// TOKEN; docs/exec.md "远端的 ir"): its arguments and its working directory there.
type CLIRun struct {
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

// CLIOutput is one line of POST /v1/internal/cli's answer: what ir wrote, as it wrote it, and last its
// exit code.
type CLIOutput struct {
	Out  string `json:"out,omitempty"`
	Err  string `json:"err,omitempty"`
	Exit *int   `json:"exit,omitempty"`
}
