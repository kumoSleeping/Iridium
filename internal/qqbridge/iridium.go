package qqbridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Iridium is the plugin's side of Iridium's plugin API (docs/api.md "插件"), as a channel
// terminal by its TOKEN.
type Iridium struct {
	URL   string
	Token func() string // read again on every call: setup may put a new one in place
	HTTP  *http.Client
}

// ErrToken: Iridium does not take the TOKEN (revoked, or none yet).
var ErrToken = errors.New("iridium refused the TOKEN (revoked? run iridium-qq setup)")

// APIError is Iridium's no.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("iridium: HTTP %d %s", e.Status, e.Message) }

func (ir *Iridium) client() *http.Client {
	if ir.HTTP != nil {
		return ir.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (ir *Iridium) request(ctx context.Context, method, path string, body io.Reader, ctype string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(ir.URL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if t := ir.Token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := ir.client().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized {
		res.Body.Close()
		return nil, ErrToken
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		if json.Unmarshal(b, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return nil, &APIError{Status: res.StatusCode, Message: e.Error}
	}
	return res, nil
}

func (ir *Iridium) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	ctype := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ctype = bytes.NewReader(b), "application/json"
	}
	res, err := ir.request(ctx, method, path, body, ctype)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func chatPath(chat string) string { return "/api/chat/" + url.PathEscape(chat) }

// Input sends a message (or an Iridium command) from chat.
func (ir *Iridium) Input(ctx context.Context, chat string, in ChatInput) (ChatInputReply, error) {
	var out ChatInputReply
	err := ir.call(ctx, "POST", chatPath(chat)+"/input", in, &out)
	return out, err
}

// Answer answers a card.
func (ir *Iridium) Answer(ctx context.Context, a api.CardAnswer) (CardReply, error) {
	var out CardReply
	err := ir.call(ctx, "POST", "/api/answer", a, &out)
	return out, err
}

// Upload puts data into the chat's session as name: its ref, for Input's Attachments.
func (ir *Iridium) Upload(ctx context.Context, chat, name string, data []byte) (string, error) {
	var u api.Upload
	if err := ir.call(ctx, "POST", chatPath(chat)+"/uploads", api.NewUpload{Name: name, Size: int64(len(data))}, &u); err != nil {
		return "", err
	}
	res, err := ir.request(ctx, "PUT", "/api/uploads/"+url.PathEscape(u.ID)+"?offset=0", bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return "", err
	}
	res.Body.Close()
	sum := sha256.Sum256(data)
	if err := ir.call(ctx, "POST", "/api/uploads/"+url.PathEscape(u.ID)+"/complete", api.CompleteUpload{Sha256: hex.EncodeToString(sum[:])}, &u); err != nil {
		return "", err
	}
	if u.File == nil {
		return "", errors.New("iridium: the upload gave no file")
	}
	return u.File.Ref, nil
}

// File fetches a reply's file, at most limit bytes.
func (ir *Iridium) File(ctx context.Context, chat, ref string, limit int64) ([]byte, error) {
	res, err := ir.request(ctx, "GET", chatPath(chat)+"/files/"+url.PathEscape(ref), nil, "")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("larger than %d MiB", limit>>20)
	}
	return b, err
}

// Me is this terminal as Iridium has it: its name now, whatever it was made as.
func (ir *Iridium) Me(ctx context.Context) (api.Me, error) {
	var m api.Me
	err := ir.call(ctx, "GET", "/api/me", nil, &m)
	if err == nil && m.Name == "" {
		err = errors.New("iridium: /api/me: no terminal")
	}
	return m, err
}

// SyncName asks Iridium for the terminal's name and keeps it in the config under home (a rename
// on the page reaches the plugin so); the name, else the config's.
func SyncName(ctx context.Context, ir *Iridium, home string) string {
	c, ok, _ := LoadConfig(home)
	if !ok {
		return c.Name
	}
	m, err := ir.Me(ctx)
	if err != nil || m.Name == c.Name {
		return c.Name
	}
	c.Name = m.Name
	_ = Write(home, ConfigFile, &c)
	return c.Name
}

// SetAvatar sets this terminal's avatar.
func (ir *Iridium) SetAvatar(ctx context.Context, image []byte) error {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, err := mw.CreateFormFile("avatar", "avatar")
	if err == nil {
		_, err = fw.Write(image)
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		return err
	}
	res, err := ir.request(ctx, "PUT", "/api/me/profile", &b, mw.FormDataContentType())
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// Event is one plugin event.
type Event struct {
	ID   int64
	Kind string // progress, reply, card, card_done, error, status
	Data ChatEvent
}

// Events reads the plugin's event stream from the one after last (-1: from now on), handing each
// to f, till the stream ends or f fails. connected is called once the stream is open.
func (ir *Iridium) Events(ctx context.Context, last int64, connected func(), f func(Event) error) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(ir.URL, "/")+"/api/events", nil)
	if err != nil {
		return err
	}
	if t := ir.Token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	req.Header.Set("Accept", "text/event-stream")
	if last >= 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(last, 10))
	}
	hc := &http.Client{} // no timeout: the stream stays open
	if ir.HTTP != nil {
		hc = &http.Client{Transport: ir.HTTP.Transport}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return ErrToken
	}
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Message: "events"}
	}
	if connected != nil {
		connected()
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var e Event
	var data []byte
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id:"):
			e.ID, _ = strconv.ParseInt(strings.TrimSpace(line[3:]), 10, 64)
		case strings.HasPrefix(line, "event:"):
			e.Kind = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " ")...)
		case line == "":
			if e.Kind != "" && len(data) > 0 {
				if err := json.Unmarshal(data, &e.Data); err == nil {
					if err := f(e); err != nil {
						return err
					}
				}
			}
			e, data = Event{}, nil
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
