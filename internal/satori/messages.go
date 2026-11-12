package satori

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"encoding/base64"
	gomime "mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Messages: a session's transcript, a message per block (blocks.go); ids are the blocks' places
// ("<offset>.<index>"), a page token a line's offset.

// internalURL is a file of session id as login sees it: internal:{app}/{login}/files/{id}/{ref}.
func internalURL(app, login, id, ref string) string {
	return "internal:" + app + "/" + url.PathEscape(login) + "/files/" + id + "/" + ref
}

// parseInternal splits an internal URL: app, login and path; ok false if it is not one.
func parseInternal(u string) (app, login, path string, ok bool) {
	rest, ok := strings.CutPrefix(u, "internal:")
	if !ok {
		return "", "", "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	l, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", "", false
	}
	return parts[0], l, parts[2], true
}

// fileOf splits a file's path (files/{session}/{ref}).
func fileOf(path string) (id, ref string, ok bool) {
	rest, ok := strings.CutPrefix(path, "files/")
	if !ok {
		return "", "", false
	}
	id, ref, ok = strings.Cut(rest, "/")
	return id, ref, ok && id != "" && ref != "" && !strings.Contains(ref, "/")
}

// fileElements are files as elements for login: <img>, <video>, <audio> or <file> by its kind,
// src its internal: URL, title its name; and the rest of the file's record (path, size, mime,
// mtime, thumb, sha256; an image's width and height), so the web has all it shows.
func fileElements(app, login, id string, fs []api.FileRef) string {
	var els []Element
	for _, f := range fs {
		if f.Ref == "" {
			continue
		}
		typ := "file"
		switch f.Kind {
		case api.FileKindImage:
			typ = "img"
		case api.FileKindVideo, api.FileKindAudio:
			typ = string(f.Kind)
		}
		e := Tag(typ, []string{"src", internalURL(app, login, id, f.Ref), "title", f.Name, "path", f.Path,
			"size", strconv.FormatInt(f.Size, 10), "mime", f.Mime, "mtime", strconv.FormatInt(f.Mtime, 10)})
		if typ == "file" && f.Kind != api.FileKindFile {
			e.Attrs = append(e.Attrs, Attr{Key: "kind", Value: string(f.Kind)})
		}
		if f.Thumb {
			e.Attrs = append(e.Attrs, Attr{Key: "thumb", Bare: true})
		}
		if f.W != 0 || f.H != 0 {
			e.Attrs = append(e.Attrs, Attr{Key: "width", Value: strconv.Itoa(f.W)}, Attr{Key: "height", Value: strconv.Itoa(f.H)})
		}
		if f.Sha256 != "" {
			e.Attrs = append(e.Attrs, Attr{Key: "sha256", Value: f.Sha256})
		}
		els = append(els, e)
	}
	return String(els)
}

// messageOf is block b of session ss as a message (blockContent), its files as login sees them
// (none if ""). The sender: who wrote a person's message (the login it came from, else the app;
// user.name the tag the agent saw), else the agent.
func (s *Service) messageOf(c Caller, ss api.Session, b api.Block, guild *Guild) Message {
	login := c.Login
	m := Message{ID: b.At, Content: blockContent(b, c.App, login, ss.ID, s.b.Mask), Channel: s.channelMasked(ss), Guild: guild, CreatedAt: b.Time}
	switch {
	case b.FromLogin != "":
		m.User = &User{ID: b.FromLogin, Name: b.From}
	case b.From != "" || b.FromTerminal != "":
		m.User = &User{ID: b.FromTerminal, Name: b.From}
	case b.Kind != api.BlockKindUser:
		if u, ok := agentUser(ss.Agent); ok {
			m.User = &u
		}
	}
	return m
}

// lineOf is the offset of the line a block's place is in.
func lineOf(at string) int64 {
	o, _, _ := strings.Cut(at, ".")
	n, err := strconv.ParseInt(o, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func (s *Service) messageGet(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		MessageID string `json:"message_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	noMessage := refuse(http.StatusUnprocessableEntity, "no_such_message", "no message "+req.MessageID)
	if e, ok := s.out.message(st.ID, req.MessageID); ok {
		ev := s.render(e, c.App, c.Login)
		m := *ev.Message
		m.Channel, m.Guild = ev.Channel, ev.Guild
		return m, nil
	}
	if strings.HasPrefix(req.MessageID, "ask:") {
		for _, a := range st.Asks {
			if cardID(st.ID, a.RequestID) == req.MessageID {
				m := s.askMessage(st.Session, a)
				return m, nil
			}
		}
		return nil, noMessage
	}
	off := lineOf(req.MessageID)
	if off < 0 {
		// message.create's id: the input, in the transcript once the agent took it (a person's
		// block by its uuid), else still in the queue.
		return s.inputMessage(c, st, req.MessageID, noMessage)
	}
	p, err := s.b.Messages(st.ID, -1, off)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(p.Blocks, func(b api.Block) bool { return b.At == req.MessageID })
	if i < 0 {
		return nil, noMessage
	}
	b := p.Blocks[i]
	if b.Length > len([]rune(b.Text)) {
		if full, err := s.b.Full(st.ID, b.At); err == nil {
			b.Text = full
		}
	}
	return s.messageOf(c, st.Session, b, s.guildOf(st.Session)), nil
}

// inputMessage is the message input id became: its block in the latest page, else its place
// in the queue.
func (s *Service) inputMessage(c Caller, st *api.SessionState, id string, none error) (any, error) {
	p, err := s.b.Messages(st.ID, -1, -1)
	if err != nil {
		return nil, err
	}
	for i := len(p.Blocks) - 1; i >= 0; i-- {
		if b := p.Blocks[i]; b.Kind == api.BlockKindUser && b.ID == id {
			if b.Length > len([]rune(b.Text)) {
				if full, err := s.b.Full(st.ID, b.At); err == nil {
					b.Text = full
				}
			}
			m := s.messageOf(c, st.Session, b, s.guildOf(st.Session))
			m.ID = id
			return m, nil
		}
	}
	for _, q := range st.Queue {
		if q.ID == id {
			u := &User{ID: cmp.Or(q.FromLogin, q.FromTerminal), Name: q.From}
			return Message{ID: id, Content: Escape(q.Text), Channel: s.channelMasked(st.Session), Guild: s.guildOf(st.Session), User: u, CreatedAt: q.At}, nil
		}
	}
	return nil, none
}

// messageList pages a session's transcript: BidiList, tokens the offsets of lines.
func (s *Service) messageList(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		Next      string `json:"next"`
		Direction string `json:"direction"`
		Limit     int    `json:"limit"`
		Order     string `json:"order"`
		Data      struct {
			Blocks bool `json:"blocks"`
		} `json:"_data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	dir := req.Direction
	if dir == "" {
		dir = "before"
	}
	if !slices.Contains([]string{"before", "after", "around"}, dir) {
		return nil, bad("direction: before, after or around")
	}
	if req.Order != "" && req.Order != "asc" && req.Order != "desc" {
		return nil, bad("order: asc or desc")
	}
	at := int64(-1)
	if req.Next != "" {
		n, err := strconv.ParseInt(req.Next, 10, 64)
		if err != nil || n < 0 {
			return nil, bad("next")
		}
		at = n
	} else if dir != "before" {
		return nil, bad("with no next, the direction is before")
	}
	var before, after *api.MessagePage
	if dir == "before" || dir == "around" {
		p, err := s.b.Messages(st.ID, at, -1) // at < 0: the latest
		if err != nil {
			return nil, err
		}
		before = &p
	}
	if dir == "after" || dir == "around" {
		p, err := s.b.Messages(st.ID, -1, at)
		if err != nil {
			return nil, err
		}
		after = &p
	}
	out := BidiList[Message]{Data: []Message{}, XData: &PageData{}}
	guild := s.guildOf(st.Session)
	var blocks []api.Block
	add := func(bs []api.Block) {
		for _, b := range bs {
			out.Data = append(out.Data, s.messageOf(c, st.Session, b, guild))
			if req.Data.Blocks {
				blocks = append(blocks, s.maskBlock(b))
			}
		}
	}
	limit := req.Limit
	if before != nil {
		bs, more := before.Blocks, before.More
		start := before.Start
		if limit > 0 {
			if cut, ok := cutBefore(bs, limit); ok {
				bs, more, start = bs[cut:], true, lineOf(bs[cut].At)
			}
		}
		add(bs)
		if more {
			out.Prev = strconv.FormatInt(start, 10)
		}
		*out.XData = PageData{Start: start, End: before.End, More: more}
	}
	if after != nil {
		bs, end := after.Blocks, after.End
		more := false
		if cut, ok := cutAfter(bs, limit); limit > 0 && ok {
			bs, more, end = bs[:cut], true, lineOf(bs[cut].At)
		} else if len(bs) > 0 && end > at {
			// The page stopped short of the end: more is written after it.
			if rest, err := s.b.Messages(st.ID, -1, end); err == nil {
				more = len(rest.Blocks) > 0
			}
		}
		add(bs)
		if more {
			out.Next = strconv.FormatInt(end, 10)
		}
		if before == nil {
			out.XData.Start, out.XData.More = after.Start, after.More
		}
		out.XData.End = end
	}
	switch dir {
	case "before":
		out.Next = out.Prev
	case "after":
		out.Prev = out.Next
	}
	if req.Order == "desc" {
		slices.Reverse(out.Data)
		slices.Reverse(blocks)
	}
	if req.Data.Blocks {
		out.XData.Blocks = nonNil(blocks)
	}
	return out, nil
}

// maskBlock is b with the credentials' values masked wherever they are in it.
func (s *Service) maskBlock(b api.Block) api.Block {
	raw, err := json.Marshal(b)
	if err != nil {
		return b
	}
	var out api.Block
	if json.Unmarshal([]byte(s.b.Mask(string(raw))), &out) != nil {
		return api.Block{At: b.At, Kind: b.Kind, Time: b.Time}
	}
	return out
}

// cutBefore is where to cut bs so that its last limit messages are kept, at a line's start: ok
// false to keep it all.
func cutBefore(bs []api.Block, limit int) (int, bool) {
	n := 0
	for i := len(bs) - 1; i >= 0; i-- {
		if n++; n == limit {
			for i > 0 && lineOf(bs[i-1].At) == lineOf(bs[i].At) {
				i--
			}
			return i, i > 0
		}
	}
	return 0, false
}

// cutAfter is where to cut bs so that its first limit messages are kept, at a line's start.
func cutAfter(bs []api.Block, limit int) (int, bool) {
	n := 0
	for i := range bs {
		if n++; n == limit {
			j := i + 1
			for j < len(bs) && lineOf(bs[j].At) == lineOf(bs[i].At) {
				j++
			}
			return j, j < len(bs)
		}
	}
	return 0, false
}

// messageCreate sends a message to a session: its text, files uploaded to it (<img>/<file> by
// their internal: URL, or data: URLs, stored as uploads), the rest by what it says. A message that
// begins with an input button's text answers that ask instead. <message> elements at the top
// are each a message of its own, sent in turn (what lies between them too).
func (s *Service) messageCreate(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string   `json:"channel_id"`
		Content   string   `json:"content"`
		Data      SendOpts `json:"_data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	if t := req.Data.Text; t != nil {
		if req.Content != "" {
			return nil, bad("content or _data.text, not both")
		}
		req.Content, req.Data.Text = Escape(*t), nil
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	var groups [][]Element
	var loose []Element
	for _, e := range Parse(req.Content) {
		if e.Type != "message" {
			loose = append(loose, e)
			continue
		}
		if len(loose) > 0 {
			groups, loose = append(groups, loose), nil
		}
		groups = append(groups, e.Children)
	}
	if len(loose) > 0 || len(groups) == 0 {
		groups = append(groups, loose)
	}
	out := []Message{}
	for i, els := range groups {
		o := req.Data
		if i > 0 {
			o.Attachments, o.Paths, o.ID = nil, nil, "" // the id is the first message's
		}
		m, err := s.sendOne(ctx, c, st, els, o)
		if err != nil {
			if len(out) > 0 {
				break // what went, went
			}
			return nil, err
		}
		out = append(out, m...)
	}
	return out, nil
}

// MaxDataURL is the most a data: URL in a message may hold.
const MaxDataURL = 20 << 20

// sendOne sends one message's elements.
func (s *Service) sendOne(ctx context.Context, c Caller, st *api.SessionState, els []Element, o SendOpts) ([]Message, error) {
	var refs []string
	text := Plain(els, func(e Element) string {
		src := e.Attr("src")
		if app, login, path, ok := parseInternal(src); ok && app == c.App && login == c.Login {
			if id, ref, ok := fileOf(path); ok && id == st.ID {
				refs = append(refs, ref)
				return ""
			}
		}
		if strings.HasPrefix(src, "data:") {
			if ref, err := s.dataURL(c, st.ID, e, src); err == nil {
				refs = append(refs, ref)
				return ""
			}
			return cmp.Or(e.Attr("title"), "[file]")
		}
		if t := e.Attr("title"); t != "" && src != "" {
			return t + " (" + src + ")"
		}
		return src
	})
	if strings.HasPrefix(text, "ask:") && !c.Only {
		if done, err := s.typed(ctx, c, text); done {
			return nil, err
		}
	}
	refs = append(refs, o.Attachments...)
	if text == "" && len(refs) == 0 && len(o.Paths) == 0 {
		return nil, bad("an empty message")
	}
	r, err := s.b.Send(ctx, c, st.ID, text, refs, o)
	if err != nil {
		return nil, err
	}
	ss := st.Session
	if r.Session != "" && r.Session != st.ID {
		if nst, err := s.b.Session(r.Session); err == nil {
			ss = nst.Session
		}
	}
	return []Message{{ID: r.Input, Content: String(els), Channel: s.channelMasked(ss), Guild: s.guildOf(ss),
		User: &User{ID: c.Login, Name: c.LoginName}, Data: r}}, nil
}

// dataURL stores the file a data: URL holds as an upload to session id: its ref.
func (s *Service) dataURL(c Caller, id string, e Element, src string) (string, error) {
	meta, data, ok := strings.Cut(strings.TrimPrefix(src, "data:"), ",")
	if !ok || len(data) > MaxDataURL*4/3+4 {
		return "", bad("a data: URL")
	}
	var b []byte
	if strings.HasSuffix(meta, ";base64") {
		var err error
		if b, err = base64.StdEncoding.DecodeString(data); err != nil {
			if b, err = base64.RawStdEncoding.DecodeString(data); err != nil {
				return "", bad("a data: URL")
			}
		}
	} else {
		t, err := url.PathUnescape(data)
		if err != nil {
			return "", bad("a data: URL")
		}
		b = []byte(t)
	}
	mime, _, _ := strings.Cut(strings.TrimSuffix(meta, ";base64"), ";")
	name := e.Attr("title")
	if name == "" {
		name = "file"
		if exts, _ := gomime.ExtensionsByType(mime); len(exts) > 0 {
			name += exts[0]
		} else if e.Type == "img" {
			name += ".png"
		}
	}
	return s.b.Upload(c, id, name, int64(len(b)), bytes.NewReader(b))
}

// uploadCreate takes files for a session (form field channel_id), one request each: each file's
// internal: URL by its form name.
func (s *Service) uploadCreate(r *http.Request, c Caller) (map[string]string, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, bad("a multipart/form-data body: " + err.Error())
	}
	defer r.MultipartForm.RemoveAll()
	st, err := s.channel(c, r.FormValue("channel_id"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, fhs := range r.MultipartForm.File {
		if len(fhs) != 1 {
			return nil, bad("one file per name: " + name)
		}
		fh := fhs[0]
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		fname := fh.Filename
		if fname == "" {
			fname = name
		}
		ref, err := s.b.Upload(c, st.ID, fname, fh.Size, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		out[name] = internalURL(c.App, c.Login, st.ID, ref)
	}
	if len(out) == 0 {
		return nil, bad("no file")
	}
	return out, nil
}

// serveProxy serves /v1/proxy/{url}: an internal: URL of the app's own logins' files. Satori's
// codes: 400 not a URL, 404 no such login, 403 any other URL (no proxy_urls).
func (s *Service) serveProxy(w http.ResponseWriter, r *http.Request, raw string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.fail(w, refuse(http.StatusMethodNotAllowed, "method", "use GET"))
		return
	}
	c, ok := s.b.Who(r)
	if !ok {
		s.fail(w, notApp)
		return
	}
	if !strings.HasPrefix(raw, "internal:") {
		if u, err := url.Parse(raw); err != nil || u.Scheme == "" {
			s.fail(w, refuse(http.StatusBadRequest, "bad_url", "not a URL"))
			return
		}
		s.fail(w, refuse(http.StatusForbidden, "not_proxied", "only internal: URLs"))
		return
	}
	app, login, path, ok := parseInternal(raw)
	if !ok {
		s.fail(w, refuse(http.StatusBadRequest, "bad_url", "internal:{platform}/{user}/{path}"))
		return
	}
	notFound := refuse(http.StatusNotFound, "no_such_file", "not found")
	if app != c.App || !slices.ContainsFunc(s.b.Logins(app), func(l LoginInfo) bool { return l.ID == login }) {
		s.fail(w, refuse(http.StatusNotFound, "no_such_login", "no such login"))
		return
	}
	c.Login, c.LoginName = login, login
	id, ref, ok := fileOf(path)
	if !ok || !s.b.Sees(c, id) {
		s.fail(w, notFound)
		return
	}
	p, err := s.b.File(c, id, ref)
	if err != nil {
		s.fail(w, notFound)
		return
	}
	s.b.ServeFile(w, r, p)
}

// messageUpdate changes the words of a message sent while it waits in the queue (message.create's
// id); once the agent took it, it stays as it is (422 no_such_message).
func (s *Service) messageUpdate(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		MessageID string `json:"message_id"`
		Content   string `json:"content"`
		Data      struct {
			Text *string `json:"text"`
		} `json:"_data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	text := Plain(Parse(req.Content), nil)
	if req.Data.Text != nil {
		text = *req.Data.Text
	}
	if strings.TrimSpace(text) == "" {
		return nil, bad("content: words")
	}
	return s.queued(c, req.ChannelID, req.MessageID, &text)
}

// messageDelete takes a message sent back while it waits in the queue.
func (s *Service) messageDelete(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		MessageID string `json:"message_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	return s.queued(c, req.ChannelID, req.MessageID, nil)
}

func (s *Service) queued(c Caller, channel, id string, text *string) (any, error) {
	if id == "" {
		return nil, bad("message_id")
	}
	st, err := s.channel(c, channel)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(st.Queue, func(q api.Queued) bool { return q.ID == id }) {
		return nil, refuse(http.StatusUnprocessableEntity, "no_such_message", "not waiting in the queue: "+id)
	}
	return struct{}{}, s.b.EditQueued(c, st.ID, id, text)
}
