package api

// PublicLink is GET/POST /v1/internal/sessions/{id}/public: the session's public read-only link.
// Ready: the public address is set and the Passkey gate on, without which no link works. On: a
// link is there. URL: the link itself, only in the answer to the POST that made it (only its
// token's hash is kept). By: the app or login that made it; CreatedAt in ms.
type PublicLink struct {
	Ready     bool   `json:"ready"`
	On        bool   `json:"on"`
	URL       string `json:"url,omitempty"`
	By        string `json:"by,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
}

// PublicPage is GET /p/{token}/api/messages: what was said in the session the link shows, a
// page of it, oldest first. ?before= the page before Before's offset; ?after= what came after
// End's. Only people's and the agent's words, credentials masked: no thinking, tools or system
// lines. Before: "" when there is nothing earlier.
type PublicPage struct {
	Title    string          `json:"title"`
	Agent    Agent           `json:"agent"`
	Status   Status          `json:"status"`
	Messages []PublicMessage `json:"messages"`
	Before   string          `json:"before,omitempty"`
	End      int64           `json:"end"`
}

// PublicMessage is one thing said: by a person (Role "user"; From, the name Iridium wrote it
// with, if any) or the agent ("agent"); its words, and the pictures it gave, each at its URL
// under the link.
type PublicMessage struct {
	At       string        `json:"at"`
	Time     int64         `json:"time,omitempty"`
	Role     string        `json:"role"`
	From     string        `json:"from,omitempty"`
	Text     string        `json:"text"`
	Pictures []PublicImage `json:"pictures,omitempty"`
}

// PublicImage is a picture of a message: its URL under the link, name and size in pixels.
type PublicImage struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
	W    int    `json:"w,omitempty"`
	H    int    `json:"h,omitempty"`
}
