package api

// ShareWhat is what a Share is of.
type ShareWhat string

const (
	ShareOfSession ShareWhat = "session"
	ShareOfReply   ShareWhat = "reply" // one block of a session: Session and At
	ShareOfPlace   ShareWhat = "place" // a folder: Place
	ShareOfFile    ShareWhat = "file"  // a file: Session, and Ref or Path
)

// Share is something shared with one login (draft 二「消息」 分享; POST /api/share, ir share): a
// session, a reply, a place or a file, who shared it and a note. Iridium checks the login may use
// what is shared (a session's agent and scope; a place's or a file's scope); what it does with it
// is its app's: a login of ir_web finds it among its needs (and a Web Push), another app's gets a
// Satori event (internal, _type ir.share).
type Share struct {
	What    ShareWhat `json:"what"`
	Session string    `json:"session,omitempty"`
	At      string    `json:"at,omitempty"`    // reply: the block's place
	Place   string    `json:"place,omitempty"` // place: "应用名:路径"
	Ref     string    `json:"ref,omitempty"`   // file: its ref in Session
	Path    string    `json:"path,omitempty"`  // file: its path (Iridium fills it from Ref); place: the folder's
	Note    string    `json:"note,omitempty"`
	// Filled by Iridium: Machine, the app the place, file or session is at ("" the host's); Title,
	// the session's name or title, a file's name or the reply's opening; From, the app or login of
	// ir_web that shared it, FromName its name, FromSession the session whose agent did (ir share);
	// At (ms) when.
	Machine     string `json:"machine,omitempty"`
	Title       string `json:"title,omitempty"`
	From        string `json:"from,omitempty"`
	FromName    string `json:"fromName,omitempty"`
	FromSession string `json:"fromSession,omitempty"`
	SharedAt    int64  `json:"sharedAt,omitempty"`
}

// ShareRequest is POST /api/share: what (Share's what and its refs; the rest Iridium fills) and to
// whom, "应用/登录" (an app's name or ID; ir_web for a login of the web). Caller: the session ir
// runs in, as ir send has it.
type ShareRequest struct {
	What    ShareWhat `json:"what"`
	Session string    `json:"session,omitempty"`
	At      string    `json:"at,omitempty"`
	Place   string    `json:"place,omitempty"`
	Ref     string    `json:"ref,omitempty"`
	Path    string    `json:"path,omitempty"`
	Note    string    `json:"note,omitempty"`
	To      string    `json:"to"`
	Caller  string    `json:"caller,omitempty"`
}

// ShareReply is where a share went: the app and login (IDs, and the login's name), and for a login
// of ir_web how many of its browsers were pushed to.
type ShareReply struct {
	App    string `json:"app"`
	Login  string `json:"login"`
	Name   string `json:"name,omitempty"`
	Pushed int    `json:"pushed,omitempty"`
}

// ShareTargets is GET /api/share/targets (what as ShareRequest's, in the query): the apps whose
// logins may use it, each with those logins; the caller itself left out.
type ShareTargets struct {
	Apps []ShareApp `json:"apps"`
}

type ShareApp struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Logins []ShareLogin `json:"logins"`
}

type ShareLogin struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}
