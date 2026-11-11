package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// The sessions through Satori in the tests: started (channel.create), listed (channel.list),
// changed (channel.update), messages sent (message.create), cards answered (internal/button), as
// one of l.do's callers speaking as its first login (meta).

// sat calls Satori's method as as: the status, the answer into out (a *string takes it raw).
func (l *live) sat(as, method string, body, out any) int {
	l.t.Helper()
	var m satori.Meta
	l.do(as, "POST", "/v1/meta", map[string]any{}, &m)
	if len(m.Logins) == 0 && strings.HasPrefix(as, "ir_") {
		// An app with no login yet: one, "main", approved at the host.
		l.asLogin(as, "main", "GET", "/v1/internal/me", nil, nil)
		l.want(200, "socket", "POST", "/v1/internal/needs/"+l.pending().ID, api.NeedAnswer{Allow: true}, nil)
		l.do(as, "POST", "/v1/meta", map[string]any{}, &m)
	}
	if len(m.Logins) == 0 || m.Logins[0].User == nil {
		l.t.Fatalf("meta as %.8s: %+v", as, m)
	}
	lg := m.Logins[0]
	if body == nil {
		body = map[string]any{}
	}
	return l.doWith(as, "POST", "/v1/"+method, body, out, http.Header{satori.HeaderPlatform: {lg.Platform}, satori.HeaderUser: {lg.User.ID}})
}

// wantSat is sat, failing unless it answers code.
func (l *live) wantSat(code int, as, method string, body, out any) {
	l.t.Helper()
	var raw string
	if got := l.sat(as, method, body, &raw); got != code {
		l.t.Fatalf("%s as %.8s: %d, want %d: %s", method, as, got, code, raw)
	}
	if p, ok := out.(*string); ok {
		*p = raw
	} else if out != nil {
		json.Unmarshal([]byte(raw), out)
	}
}

// createBody is n as channel.create takes it.
func createBody(n api.NewSession) map[string]any {
	b := map[string]any{}
	if n.Workspace != 0 {
		b["guild_id"] = n.Workspace
	}
	if n.Caller != "" {
		b["data"] = map[string]any{"parent_id": n.Caller}
	}
	n.Workspace, n.Caller = 0, ""
	b["_data"] = n
	return b
}

// create starts session n as as: its id; code what channel.create answered.
func (l *live) create(code int, as string, n api.NewSession) string {
	l.t.Helper()
	var ch satori.Channel
	l.wantSat(code, as, "channel.create", createBody(n), &ch)
	return ch.ID
}

// sessions are the sessions as sees, archived ones and subagents too (channel.list _data.all).
func (l *live) sessions(as string) []api.Session {
	l.t.Helper()
	var out []api.Session
	next := ""
	for {
		body := map[string]any{"_data": map[string]any{"all": true}}
		if next != "" {
			body["next"] = next
		}
		var page satori.List[satori.Channel]
		l.wantSat(200, as, "channel.list", body, &page)
		for _, c := range page.Data {
			if c.Type == satori.ChannelText && c.Data != nil {
				out = append(out, *c.Data)
			}
		}
		if next = page.Next; next == "" {
			return out
		}
	}
}

// patchBody is p as channel.update takes it for session id.
func patchBody(id string, p api.SessionPatch) map[string]any {
	b := map[string]any{"channel_id": id}
	if p.Name != nil {
		b["data"] = map[string]any{"name": *p.Name}
	}
	if p.Detach != nil && *p.Detach {
		d, _ := b["data"].(map[string]any)
		if d == nil {
			d = map[string]any{}
		}
		d["parent_id"] = nil
		b["data"] = d
	}
	x := map[string]any{}
	for k, v := range map[string]any{"archived": p.Archived, "pinned": p.Pinned, "color": p.Color, "order": p.Order, "workspace": p.Workspace} {
		if !isNilPtr(v) {
			x[k] = v
		}
	}
	if len(x) > 0 {
		b["_data"] = x
	}
	return b
}

func isNilPtr(v any) bool {
	switch p := v.(type) {
	case *bool:
		return p == nil
	case *api.Color:
		return p == nil
	case *float64:
		return p == nil
	case *int64:
		return p == nil
	}
	return v == nil
}

// patch changes session id as as (channel.update): the status.
func (l *live) patch(as, id string, p api.SessionPatch) int {
	l.t.Helper()
	return l.sat(as, "channel.update", patchBody(id, p), nil)
}

// send sends text to session id as as (message.create, plain words): the status, what it did.
func (l *live) send(as, id string, req api.Send) (int, api.SendReply) {
	l.t.Helper()
	d := map[string]any{"text": req.Text}
	if req.Steer {
		d["steer"] = true
	} else {
		d["steer"] = false
	}
	if len(req.Attachments) > 0 {
		d["attachments"] = req.Attachments
	}
	if req.Caller != "" {
		d["caller"] = req.Caller
	}
	var ms []struct {
		Data api.SendReply `json:"_data"`
	}
	code := l.sat(as, "message.create", map[string]any{"channel_id": id, "_data": d}, &ms)
	if len(ms) == 0 {
		return code, api.SendReply{}
	}
	return code, ms[len(ms)-1].Data
}

// answer answers a card as as (internal/button, the card whole): the status.
func (l *live) answer(as string, a api.CardAnswer) int {
	l.t.Helper()
	return l.sat(as, "internal/button", map[string]any{"id": a.Card, "option": a.Option, "choices": a.Choices, "words": a.Words, "message": a.Message}, nil)
}

// satAt is sat for a test without a live: a Satori method at base, as the page (local/local).
func satAt(t *testing.T, c *http.Client, base, method string, body, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+"/v1/"+method, bytes.NewReader(b))
	req.Header.Set(satori.HeaderPlatform, "local")
	req.Header.Set(satori.HeaderUser, "local")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if p, ok := out.(*string); ok {
			*p = string(raw)
		} else {
			json.Unmarshal(raw, out)
		}
	}
	return resp.StatusCode
}

// createAt starts session n at base as the page: its id.
func createAt(t *testing.T, c *http.Client, base string, n api.NewSession) (string, int) {
	t.Helper()
	var ch satori.Channel
	code := satAt(t, c, base, "channel.create", createBody(n), &ch)
	return ch.ID, code
}

// sessionsAt are the sessions at base as the page sees them.
func sessionsAt(t *testing.T, c *http.Client, base string) []api.Session {
	t.Helper()
	var page satori.List[satori.Channel]
	satAt(t, c, base, "channel.list", map[string]any{"_data": map[string]any{"all": true}}, &page)
	var out []api.Session
	for _, ch := range page.Data {
		if ch.Type == satori.ChannelText && ch.Data != nil {
			out = append(out, *ch.Data)
		}
	}
	return out
}

// page is the latest page of session id's transcript as as sees it (message.list _data.blocks).
func (l *live) page(as, id string) api.MessagePage {
	l.t.Helper()
	var p struct {
		Data satori.PageData `json:"_data"`
	}
	l.sat(as, "message.list", map[string]any{"channel_id": id, "_data": map[string]any{"blocks": true}}, &p)
	return api.MessagePage{Blocks: p.Data.Blocks, Start: p.Data.Start, End: p.Data.End, More: p.Data.More}
}

// patchAt changes session id at base as the page (channel.update): the status.
func patchAt(t *testing.T, c *http.Client, base, id string, p api.SessionPatch) int {
	t.Helper()
	return satAt(t, c, base, "channel.update", patchBody(id, p), nil)
}

// sendAt sends text to session id at base as the page: the status, what it did.
func sendAt(t *testing.T, c *http.Client, base, id string, req api.Send) (int, api.SendReply) {
	t.Helper()
	var ms []struct {
		Data api.SendReply `json:"_data"`
	}
	code := satAt(t, c, base, "message.create", map[string]any{"channel_id": id, "_data": map[string]any{"text": req.Text, "steer": req.Steer, "attachments": req.Attachments}}, &ms)
	if len(ms) == 0 {
		return code, api.SendReply{}
	}
	return code, ms[len(ms)-1].Data
}

// pageAt is the latest page of session id's transcript at base as the page.
func pageAt(t *testing.T, c *http.Client, base, id string) api.MessagePage {
	t.Helper()
	var p struct {
		Data satori.PageData `json:"_data"`
	}
	satAt(t, c, base, "message.list", map[string]any{"channel_id": id, "_data": map[string]any{"blocks": true}}, &p)
	return api.MessagePage{Blocks: p.Data.Blocks, Start: p.Data.Start, End: p.Data.End, More: p.Data.More}
}

// answerAt answers a card at base as the page (internal/button): the status.
func answerAt(t *testing.T, c *http.Client, base string, a api.CardAnswer) int {
	t.Helper()
	return satAt(t, c, base, "internal/button", map[string]any{"id": a.Card, "option": a.Option, "choices": a.Choices, "words": a.Words, "message": a.Message}, nil)
}
