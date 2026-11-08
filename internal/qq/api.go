// Package qq is the QQ Open Platform as the QQ plugin (cmd/iridium-qq) uses it: the app access
// token, the REST calls for private chats (send, upload, answer a button press), the WebSocket
// gateway, and the QR flow that makes a bot. Protocol facts are from the official Node SDK
// (@tencent-connect/qqbot-nodejs) and Rhodium's probes (its docs/architecture.md §8).
package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAPI   = "https://api.sgroup.qq.com"
	DefaultToken = "https://bots.qq.com"
	// MaxUpload is the largest file one upload takes (the SDK's MAX_UPLOAD_SIZE).
	MaxUpload = 20 << 20
	// MaxText is how long one message's text is kept: phone-screen sized (Rhodium's 2000).
	MaxText = 2000
)

// Message types (msg_type) and media file types (file_type).
const (
	MsgText     = 0
	MsgMarkdown = 2
	MsgMedia    = 7

	FileImage = 1
	FileVideo = 2
	FileVoice = 3
	FileFile  = 4
)

// Client calls the platform as one bot.
type Client struct {
	AppID, Secret string
	API, TokenURL string // "" for the defaults
	MenuAPI       string // "" for DefaultMenuAPI
	HTTP          *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Error is the platform's no: an HTTP status and, when it said, its business code.
type Error struct {
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("qq: %d %s (HTTP %d)", e.Code, e.Message, e.Status)
	}
	return fmt.Sprintf("qq: HTTP %d %s", e.Status, e.Message)
}

// Refused reports whether the platform surely did not take the request: it said why (a business
// code or a 4xx). A timeout or a 5xx leaves that open.
func Refused(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code != 0 || (e.Status >= 400 && e.Status < 500))
}

// Deduped is QQ's answer to a msg_id + msg_seq it took already.
const Deduped = 40054005

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return DefaultAPI
}

// Token is the app access token, fetched again a minute before it expires.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires.Add(-time.Minute)) {
		return c.token, nil
	}
	base := c.TokenURL
	if base == "" {
		base = DefaultToken
	}
	body, _ := json.Marshal(map[string]string{"appId": c.AppID, "clientSecret": c.Secret})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/app/getAppAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out struct {
		Token   string          `json:"access_token"`
		Expires json.RawMessage `json:"expires_in"` // a string, or a number
		Code    int             `json:"code"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Token == "" {
		if out.Message == "" {
			out.Message = strings.TrimSpace(string(b))
			if len(out.Message) > 200 {
				out.Message = out.Message[:200]
			}
		}
		return "", &Error{Status: res.StatusCode, Code: out.Code, Message: "access token: " + out.Message}
	}
	secs, _ := strconv.Atoi(strings.Trim(string(out.Expires), `"`))
	if secs <= 0 {
		secs = 7200
	}
	c.token, c.expires = out.Token, time.Now().Add(time.Duration(secs)*time.Second)
	return c.token, nil
}

// Do calls path with body as JSON; out (if not nil) gets the answer.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	return c.doAt(ctx, c.api(), method, path, body, out)
}

func (c *Client) doAt(ctx context.Context, base, method, path string, body, out any) error {
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "QQBot "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	var e struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	if res.StatusCode >= 300 || e.Code != 0 {
		if e.Message == "" {
			e.Message = http.StatusText(res.StatusCode)
		}
		return &Error{Status: res.StatusCode, Code: e.Code, Message: e.Message}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Message is a message to a private chat. MsgID (a passive reply to that message) with MsgSeq,
// which QQ dedupes resends on; without MsgID it is an active message.
type Message struct {
	Type     int       `json:"msg_type"`
	Content  string    `json:"content,omitempty"`
	Markdown *Markdown `json:"markdown,omitempty"`
	Keyboard *Keyboard `json:"keyboard,omitempty"`
	Media    *Media    `json:"media,omitempty"`
	MsgID    string    `json:"msg_id,omitempty"`
	MsgSeq   int       `json:"msg_seq,omitempty"`
}

type Markdown struct {
	Content string `json:"content"`
}

type Media struct {
	FileInfo string `json:"file_info"`
}

// Keyboard is the buttons under a Markdown message: at most 5 rows of 5.
type Keyboard struct {
	Content KeyboardContent `json:"content"`
}

type KeyboardContent struct {
	Rows []Row `json:"rows"`
}

type Row struct {
	Buttons []Button `json:"buttons"`
}

type Button struct {
	ID         string       `json:"id"`
	RenderData RenderData   `json:"render_data"`
	Action     ButtonAction `json:"action"`
}

type RenderData struct {
	Label        string `json:"label"`
	VisitedLabel string `json:"visited_label"`
	Style        int    `json:"style"` // 0 grey, 1 blue
}

type ButtonAction struct {
	Type       int              `json:"type"` // 1 callback, 2 command
	Permission ButtonPermission `json:"permission"`
	Data       string           `json:"data"`
	Enter      bool             `json:"enter,omitempty"`
}

type ButtonPermission struct {
	Type int `json:"type"` // 2 anyone
}

// Sent is the platform's answer to a message.
type Sent struct {
	ID string `json:"id"`
}

// Send sends m to the private chat of openid.
func (c *Client) Send(ctx context.Context, openid string, m Message) (Sent, error) {
	var out Sent
	err := c.Do(ctx, "POST", "/v2/users/"+openid+"/messages", m, &out)
	return out, err
}

// Upload puts a file for the private chat of openid (not sent by itself): its file_info, for a
// media message. data at most MaxUpload; name only for FileFile.
func (c *Client) Upload(ctx context.Context, openid string, fileType int, data []byte, name string) (string, error) {
	if len(data) > MaxUpload {
		return "", fmt.Errorf("qq: %d bytes, more than the %d MiB QQ takes", len(data), MaxUpload>>20)
	}
	body := map[string]any{"file_type": fileType, "srv_send_msg": false, "file_data": data} // []byte is base64 in JSON
	if fileType == FileFile && name != "" {
		body["file_name"] = name
	}
	var out struct {
		FileInfo string `json:"file_info"`
	}
	if err := c.Do(ctx, "POST", "/v2/users/"+openid+"/files", body, &out); err != nil {
		return "", err
	}
	if out.FileInfo == "" {
		return "", errors.New("qq: the upload gave no file_info")
	}
	return out.FileInfo, nil
}

// Ack answers a button press: QQ shows it failed unless answered within 3 s.
func (c *Client) Ack(ctx context.Context, interaction string) error {
	return c.Do(ctx, "PUT", "/interactions/"+interaction, map[string]int{"code": 0}, nil)
}

// Me is the bot's own profile.
type Me struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

func (c *Client) Me(ctx context.Context) (Me, error) {
	var m Me
	err := c.Do(ctx, "GET", "/users/@me", nil, &m)
	return m, err
}

// Download fetches an attachment's URL (QQ gives some as //host/path), at most limit bytes.
func Download(ctx context.Context, hc *http.Client, url string, limit int64) ([]byte, error) {
	if strings.HasPrefix(url, "//") {
		url = "https:" + url
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("larger than %d MiB", limit>>20)
	}
	return b, err
}

// DefaultMenuAPI is where the menu and panel APIs are (not the default API's domain).
const DefaultMenuAPI = "https://api.bot.qq.com"

// MenuItem is a button of the bot's custom menu (the row under private chats): it sends Command,
// or, with Sub, folds up to 5 such. At most 10, names at most 10 wide (a Chinese character 2).
type MenuItem struct {
	Name    string
	Command string
	Sub     []MenuItem
}

func (c *Client) menuAPI() string {
	if c.MenuAPI != "" {
		return strings.TrimRight(c.MenuAPI, "/")
	}
	return DefaultMenuAPI
}

// SetMenu puts items in place of the bot's custom menu, all private chats' one.
func (c *Client) SetMenu(ctx context.Context, items []MenuItem) error {
	send := func(m MenuItem) map[string]any {
		return map[string]any{"type": "send_message", "name": m.Name, "send_message": m.Command}
	}
	var out []map[string]any
	for _, m := range items {
		if len(m.Sub) == 0 {
			out = append(out, send(m))
			continue
		}
		var sub []map[string]any
		for _, s := range m.Sub {
			sub = append(sub, send(s))
		}
		out = append(out, map[string]any{"type": "menu", "name": m.Name, "sub_menu_items": sub})
	}
	return c.doAt(ctx, c.menuAPI(), "PUT", "/v2/menu", map[string]any{"menu": map[string]any{"items": out}}, nil)
}

// PanelItem is a command of the panel typing / brings up; Desc at most 30 wide.
type PanelItem struct {
	Name, Desc string
}

// SetPanel puts the private chats' / panel marked remark in place, made if missing. One marked
// as one of over (another service's, for a bot that came from it) is taken over instead; the bot's
// other panels are left alone. A bot has at most 20 panels; one holds at most 20 items.
func (c *Client) SetPanel(ctx context.Context, remark string, over []string, items []PanelItem) error {
	var list struct {
		Records []struct {
			ID    string `json:"panel_id"`
			Panel struct {
				Remark string `json:"remark"`
			} `json:"panel"`
		} `json:"records"`
	}
	if err := c.doAt(ctx, c.menuAPI(), "GET", "/v2/panels?scope=c2c&limit=50", nil, &list); err != nil {
		return err
	}
	var cmds []map[string]string
	for _, it := range items {
		cmds = append(cmds, map[string]string{"type": "command", "name": it.Name, "desc": width(it.Desc, 30)})
	}
	body := map[string]any{"remark": remark, "items": cmds}
	id := ""
	for _, r := range list.Records {
		if r.Panel.Remark == remark {
			id = r.ID
			break
		}
	}
	if id == "" {
		for _, r := range list.Records {
			for _, o := range over {
				if r.Panel.Remark == o && id == "" {
					id = r.ID
				}
			}
		}
	}
	if id != "" {
		return c.doAt(ctx, c.menuAPI(), "PUT", "/v2/panels/"+id, map[string]any{"panel": body}, nil)
	}
	return c.doAt(ctx, c.menuAPI(), "POST", "/v2/panels", map[string]any{"scope": "c2c", "target_type": "all", "panel": body}, nil)
}

// width cuts s to max columns, a non-ASCII character counting 2, with an ellipsis.
func width(s string, max int) string {
	w := func(r rune) int {
		if r < 128 {
			return 1
		}
		return 2
	}
	total := 0
	for _, r := range s {
		total += w(r)
	}
	if total <= max {
		return s
	}
	used := 2
	var b strings.Builder
	for _, r := range s {
		if used+w(r) > max {
			break
		}
		used += w(r)
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String()) + "…"
}
