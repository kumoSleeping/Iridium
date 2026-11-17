package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// A session's first screen in one answer (GET /v1/internal/sessions/{id}/first): what its page
// asked for one by one and drew as each came (its state, its record's newest page, its
// channel's members; with another agent in the channel: how far each has read, the log as far
// back as the record's page goes, the other agents' states and the work under their words).
// Each part is the answer of the API it stands for, asked here as the caller, so what a caller
// may read and how it is masked are those APIs' own; a part that failed is left out, and the
// page asks for it as before.

// firstKey is the handler the parts are asked of (withSatori), in the request's context.
type firstKey struct{}

// firstWorkMax is how many messages' work a first screen brings at most, the newest.
const firstWorkMax = 80

// firstBack is how many pages back the log is read at most, as the page does (alignBack).
const firstBack = 20

type firstPage struct {
	Data  []json.RawMessage `json:"data"`
	XData struct {
		Start  int64       `json:"start"`
		End    int64       `json:"end"`
		More   bool        `json:"more"`
		Blocks []api.Block `json:"blocks,omitempty"`
	} `json:"_data"`
}

// firstWork is the work under one message of another agent: where it is from in that agent's
// record (as the message's _data says), and the record's blocks there, as block.list gives them.
type firstWork struct {
	Session string            `json:"session"`
	Block   string            `json:"block,omitempty"`
	From    *int64            `json:"from,omitempty"`
	To      int64             `json:"to,omitempty"`
	Data    []json.RawMessage `json:"data"`
}

type firstScreen struct {
	Session  json.RawMessage            `json:"session"`
	Page     json.RawMessage            `json:"page,omitempty"`
	Members  json.RawMessage            `json:"members,omitempty"`
	Reads    json.RawMessage            `json:"reads,omitempty"`
	Log      *firstPage                 `json:"log,omitempty"`
	Sessions map[string]json.RawMessage `json:"sessions,omitempty"`
	Work     []firstWork                `json:"work,omitempty"`
}

// memWriter keeps an answer in memory.
type memWriter struct {
	h    http.Header
	code int
	buf  bytes.Buffer
}

func (m *memWriter) Header() http.Header { return m.h }
func (m *memWriter) WriteHeader(code int) {
	if m.code == 0 {
		m.code = code
	}
}
func (m *memWriter) Write(p []byte) (int, error) {
	m.WriteHeader(http.StatusOK)
	return m.buf.Write(p)
}

// line is the line of a block's at ("<line>.<n>").
func line(at string) int64 {
	s, _, _ := strings.Cut(at, ".")
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// workAt picks the blocks under a message from the page of its agent's record around it, as the
// page does (web chat/channel/lib.ts workOf): said with ir, those of lines from..to; said
// straight out, those after the last thing the agent said or was given before the block.
func workAt(blocks []api.Block, block string, from *int64, to int64) []int {
	var out []int
	if from != nil && *from >= 0 && to > 0 {
		for i, b := range blocks {
			if l := line(b.At); l >= *from && l < to && b.Kind != "user" && b.Kind != "system" {
				out = append(out, i)
			}
		}
		return out
	}
	if block == "" {
		return nil
	}
	end := line(block)
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if line(b.At) >= end {
			continue
		}
		if b.Kind == "user" || b.Kind == "system" || (b.Kind == "assistant" && strings.TrimSpace(b.Text) != "") {
			break
		}
		out = append([]int{i}, out...)
	}
	return out
}

func (s *Server) first(w http.ResponseWriter, r *http.Request) {
	h, _ := r.Context().Value(firstKey{}).(http.Handler)
	if h == nil {
		refuse(w, http.StatusNotFound, "not_found", "no first screen here")
		return
	}
	id := r.PathValue("id")
	// As the page speaks in Satori: its app and login (what /v1/internal/me says).
	hdr := r.Header.Clone()
	hdr.Del("Accept-Encoding")
	hdr.Del("Content-Length")
	hdr.Set("Content-Type", "application/json")
	if c, ok := (satoriBackend{s}).Who(r); ok && c.Login != "" {
		hdr.Set(satori.HeaderPlatform, c.App)
		hdr.Set(satori.HeaderUser, c.Login)
	}
	ask := func(method, path string, body any) (json.RawMessage, int) {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, err := http.NewRequestWithContext(r.Context(), method, path, bytes.NewReader(raw))
		if err != nil {
			return nil, http.StatusBadRequest
		}
		req.Header = hdr.Clone()
		req.RemoteAddr, req.RequestURI, req.Host = r.RemoteAddr, path, r.Host
		req.TLS = r.TLS
		m := &memWriter{h: http.Header{}}
		h.ServeHTTP(m, req)
		if m.code == 0 {
			m.code = http.StatusOK
		}
		return bytes.TrimSpace(m.buf.Bytes()), m.code
	}
	ok := func(raw json.RawMessage, code int) json.RawMessage {
		if code != http.StatusOK || !json.Valid(raw) {
			return nil
		}
		return raw
	}
	sessionPath := func(x string) string { return "/v1/internal/sessions/" + url.PathEscape(x) }
	channelPath := "/v1/internal/channels/" + url.PathEscape(id)

	// Its state, its record's newest page (not for a page that has it: record=0), its members.
	q := r.URL.Query()
	var out firstScreen
	var stateCode int
	var wg sync.WaitGroup
	wg.Go(func() { out.Session, stateCode = ask("GET", sessionPath(id), nil) })
	if q.Get("record") != "0" {
		wg.Go(func() { out.Page = ok(ask("POST", "/v1/internal/block.list", map[string]any{"channel_id": id})) })
	}
	wg.Go(func() { out.Members = ok(ask("GET", channelPath+"/members", nil)) })
	wg.Wait()
	if stateCode != http.StatusOK {
		// No such session, or not the caller's to see: as its state's API says it.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(stateCode)
		_, _ = w.Write(out.Session)
		return
	}

	// With another agent in its channel: the channel's side.
	var ms api.ChannelMembers
	_ = json.Unmarshal(out.Members, &ms)
	var others []string
	for _, m := range ms.Members {
		if m.Session != "" && m.Session != id {
			others = append(others, m.Session)
		}
	}
	if len(others) > 0 {
		// How far back the page shows the others' words: to its record's first block, or all of
		// them when the record is whole (since: as a page that has the record says it).
		since, known := int64(0), false
		if v := q.Get("since"); v != "" {
			since, _ = strconv.ParseInt(v, 10, 64)
			known = true
		} else if out.Page != nil {
			var p firstPage
			var head struct {
				At int64 `json:"created_at"`
			}
			if json.Unmarshal(out.Page, &p) == nil && len(p.Data) > 0 && json.Unmarshal(p.Data[0], &head) == nil && head.At > 0 {
				known = true
				if p.XData.More {
					since = head.At
				}
			}
		}
		wg.Go(func() { out.Reads = ok(ask("GET", channelPath+"/reads", nil)) })
		var mu sync.Mutex
		out.Sessions = map[string]json.RawMessage{}
		for _, x := range others {
			wg.Go(func() {
				if st := ok(ask("GET", sessionPath(x), nil)); st != nil {
					mu.Lock()
					out.Sessions[x] = st
					mu.Unlock()
				}
			})
		}
		wg.Go(func() {
			out.Log = firstLog(ask, id, since, known)
			if out.Log != nil {
				out.Work = firstWorks(ask, out.Log, id, since)
			}
		})
		wg.Wait()
	}
	reply(w, http.StatusOK, out)
}

type logHead struct {
	ID   string `json:"id"`
	At   int64  `json:"created_at"`
	User *struct {
		ID string `json:"id"`
	} `json:"user"`
	Data *struct {
		Session string `json:"session"`
		Block   string `json:"block"`
		From    *int64 `json:"from"`
		To      int64  `json:"to"`
	} `json:"_data"`
}

// firstLog is the channel's log as the page holds it at first: its newest page, and those
// before it until they reach back to since (known: there is a since to reach back to).
func firstLog(ask func(method, path string, body any) (json.RawMessage, int), id string, since int64, known bool) *firstPage {
	var log firstPage
	raw, code := ask("POST", "/v1/message.list", map[string]any{"channel_id": id})
	if code != http.StatusOK || json.Unmarshal(raw, &log) != nil {
		return nil
	}
	if log.Data == nil {
		log.Data = []json.RawMessage{}
	}
	for i := 0; known && i < firstBack && log.XData.More && len(log.Data) > 0; i++ {
		var head logHead
		if json.Unmarshal(log.Data[0], &head) != nil || head.At <= since {
			break
		}
		var p firstPage
		raw, code := ask("POST", "/v1/message.list", map[string]any{"channel_id": id, "next": head.ID, "direction": "before"})
		if code != http.StatusOK || json.Unmarshal(raw, &p) != nil {
			break
		}
		if len(p.Data) == 0 {
			log.XData.More = false
			break
		}
		log.Data = append(p.Data, log.Data...)
		log.XData.Start, log.XData.More = p.XData.Start, p.XData.More
	}
	return &log
}

// firstWorks is the work under the other agents' messages of log that the page shows (those
// since since), each place in a record once, the newest firstWorkMax of them.
func firstWorks(ask func(method, path string, body any) (json.RawMessage, int), log *firstPage, own string, since int64) []firstWork {
	var want []firstWork
	seen := map[string]bool{}
	for i := len(log.Data) - 1; i >= 0 && len(want) < firstWorkMax; i-- {
		var m logHead
		if json.Unmarshal(log.Data[i], &m) != nil || m.User == nil || m.Data == nil || !strings.HasPrefix(m.User.ID, "agent:") {
			continue
		}
		d := m.Data
		if d.Session == "" || d.Session == own || m.At < since || (d.From == nil && d.Block == "") {
			continue
		}
		from := "-"
		if d.From != nil {
			from = strconv.FormatInt(*d.From, 10)
		}
		key := d.Session + "\x00" + d.Block + "\x00" + from + "\x00" + strconv.FormatInt(d.To, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		want = append(want, firstWork{Session: d.Session, Block: d.Block, From: d.From, To: max(d.To, 0)})
	}
	var wg sync.WaitGroup
	gate := make(chan struct{}, 8)
	got := make([]bool, len(want))
	for i := range want {
		wg.Go(func() {
			gate <- struct{}{}
			defer func() { <-gate }()
			k := &want[i]
			body := map[string]any{"channel_id": k.Session, "_data": map[string]any{"blocks": true}}
			if k.From != nil {
				body["next"], body["direction"] = strconv.FormatInt(max(0, *k.From-1), 10), "after"
			} else {
				body["next"], body["direction"] = strconv.FormatInt(line(k.Block)+1, 10), "before"
			}
			var p firstPage
			raw, code := ask("POST", "/v1/internal/block.list", body)
			if code != http.StatusOK || json.Unmarshal(raw, &p) != nil || len(p.XData.Blocks) != len(p.Data) {
				return
			}
			k.Data = []json.RawMessage{}
			for _, j := range workAt(p.XData.Blocks, k.Block, k.From, k.To) {
				k.Data = append(k.Data, p.Data[j])
			}
			got[i] = true
		})
	}
	wg.Wait()
	out := []firstWork{}
	for i, k := range want {
		if got[i] {
			out = append(out, k)
		}
	}
	return out
}
