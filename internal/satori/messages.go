package satori

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Messages: a session's transcript, its user and assistant blocks (tools, thoughts and system
// lines are the web's, GET /api/sessions/{id}/messages); ids are the blocks' places
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

// fileElements are files as elements for login: <img> for an image, else <file>.
func fileElements(app, login, id string, fs []api.FileRef) string {
	var els []Element
	for _, f := range fs {
		if f.Ref == "" {
			continue
		}
		typ := "file"
		if f.Kind == api.FileKindImage {
			typ = "img"
		}
		els = append(els, Tag(typ, []string{"src", internalURL(app, login, id, f.Ref), "title", f.Name}))
	}
	return String(els)
}

// messageOf is block b of session ss as a message, its files as login sees them (none if "").
func (s *Service) messageOf(c Caller, ss api.Session, b api.Block, guild *Guild) Message {
	m := Message{ID: b.At, Content: Escape(s.b.Mask(b.Text)), Channel: channelOf(ss), Guild: guild, CreatedAt: b.Time}
	if c.Login != "" {
		m.Content += fileElements(c.App, c.Login, ss.ID, b.Files)
	}
	if b.Kind == api.BlockKindAssistant {
		if u, ok := agentUser(ss.Agent); ok {
			m.User = &u
		}
	} else {
		m.User = &User{ID: b.FromTerminal, Name: b.From}
		if m.User.ID == "" && b.From == "" {
			m.User = nil
		}
	}
	return m
}

func isMessage(b api.Block) bool {
	return b.Kind == api.BlockKindUser || b.Kind == api.BlockKindAssistant
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
	if e, ok := s.out.message(req.ChannelID, req.MessageID); ok {
		return s.render(e, c.App, c.Login).Message, nil
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
		return nil, noMessage
	}
	p, err := s.b.Messages(st.ID, -1, off)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(p.Blocks, func(b api.Block) bool { return b.At == req.MessageID })
	if i < 0 || !isMessage(p.Blocks[i]) {
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

// messageList pages a session's transcript: BidiList, tokens the offsets of lines.
func (s *Service) messageList(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		Next      string `json:"next"`
		Direction string `json:"direction"`
		Limit     int    `json:"limit"`
		Order     string `json:"order"`
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
	out := BidiList[Message]{Data: []Message{}}
	guild := s.guildOf(st.Session)
	add := func(bs []api.Block) {
		for _, b := range bs {
			if isMessage(b) {
				out.Data = append(out.Data, s.messageOf(c, st.Session, b, guild))
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
	}
	if after != nil {
		bs, end := after.Blocks, after.End
		more := len(bs) > 0 && end > at
		if limit > 0 {
			if cut, ok := cutAfter(bs, limit); ok {
				bs, more, end = bs[:cut], true, lineOf(bs[cut].At)
			}
		}
		add(bs)
		if more {
			out.Next = strconv.FormatInt(end, 10)
		}
	}
	switch dir {
	case "before":
		out.Next = out.Prev
	case "after":
		out.Prev = out.Next
	}
	if req.Order == "desc" {
		slices.Reverse(out.Data)
	}
	return out, nil
}

// cutBefore is where to cut bs so that its last limit messages are kept, at a line's start: ok
// false to keep it all.
func cutBefore(bs []api.Block, limit int) (int, bool) {
	n := 0
	for i := len(bs) - 1; i >= 0; i-- {
		if isMessage(bs[i]) {
			n++
		}
		if n == limit {
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
	for i, b := range bs {
		if isMessage(b) {
			n++
		}
		if n == limit {
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
// their internal: URL), the rest by what it says. A message that begins with an input button's
// text answers that ask instead.
func (s *Service) messageCreate(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	els := Parse(req.Content)
	var refs []string
	text := Plain(els, func(e Element) string {
		src := e.Attr("src")
		if app, login, path, ok := parseInternal(src); ok && app == c.App && login == c.Login {
			if id, ref, ok := fileOf(path); ok && id == st.ID {
				refs = append(refs, ref)
				return ""
			}
		}
		if t := e.Attr("title"); t != "" && src != "" {
			return t + " (" + src + ")"
		}
		return src
	})
	if strings.HasPrefix(text, "ask:") {
		if done, err := s.typed(ctx, c, text); done {
			return []Message{}, err
		}
	}
	if text == "" && len(refs) == 0 {
		return nil, bad("an empty message")
	}
	r, err := s.b.Send(ctx, c, st.ID, text, refs)
	if err != nil {
		return nil, err
	}
	ss := st.Session
	if r.Session != "" && r.Session != st.ID {
		if nst, err := s.b.Session(r.Session); err == nil {
			ss = nst.Session
		}
	}
	return []Message{{ID: r.Input, Content: req.Content, Channel: channelOf(ss), Guild: s.guildOf(ss),
		User: &User{ID: c.Login, Name: c.LoginName}}}, nil
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
