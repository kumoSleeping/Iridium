package qqbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/qq"
)

const (
	// passiveLimit and passiveTTL: QQ takes this many passive replies to one message, within
	// this long of it (the SDK's ReplyLimiter); a little under, to be safe.
	passiveLimit = 4
	passiveTTL   = 55 * time.Minute
	// maxIn is the largest attachment passed on to Iridium.
	maxIn = 50 << 20
	// markdownRetry: a bot QQ refused Markdown for is tried again after this long.
	markdownRetry = 24 * time.Hour
)

// Bridge passes a bot's owners' private chats to Iridium and back.
type Bridge struct {
	Home   string
	Config Config
	QQ     *qq.Client
	IR     *Iridium
	// GatewayURL: "" asks QQ (tests point it at a fake).
	GatewayURL string
	HTTP       *http.Client // for QQ's attachment URLs
	Logf       func(string, ...any)

	inbound  chan inbound
	sendMu   sync.Mutex // one message out at a time, in order
	avatarMu sync.Mutex
	menuMu   sync.Mutex

	mu     sync.Mutex
	state  State
	status Status
	cards  map[string]api.Card // chat → its latest open card
	keys   map[string]press    // button data → what it answers
	nkey   int
}

type inbound struct {
	kind string
	id   string
	data json.RawMessage
}

type press struct {
	chat, card, option string
}

// Outgoing is one message to a chat: Markdown (its Text when QQ takes no Markdown from this bot),
// or a reply's file (fetched from Iridium as it goes).
type Outgoing struct {
	Markdown string   `json:"markdown,omitempty"`
	Text     string   `json:"text,omitempty"`
	File     *OutFile `json:"file,omitempty"`
	card     *api.Card
}

type OutFile struct {
	Chat string `json:"chat"`
	Ref  string `json:"ref"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func (b *Bridge) logf(f string, a ...any) {
	if b.Logf != nil {
		b.Logf(f, a...)
	}
}

// chatKey is Iridium's chat key of openid's private chat; openidOf the other way.
func chatKey(openid string) string { return "c2c:" + openid }

func openidOf(chat string) (string, bool) { return strings.CutPrefix(chat, "c2c:") }

// Run holds the gateway connection and Iridium's event stream till ctx ends.
func (b *Bridge) Run(ctx context.Context) error {
	if _, err := Read(b.Home, StateFile, &b.state); err != nil {
		b.logf("state: %v (starting afresh)", err)
		b.state = State{}
	}
	if b.state.AppID != b.Config.AppID { // another bot: its sessions and counts are not this one's
		b.state = State{AppID: b.Config.AppID, LastEvent: b.state.LastEvent}
	}
	if b.state.Chats == nil {
		b.state.Chats = map[string]*ChatState{}
	}
	b.cards, b.keys = map[string]api.Card{}, map[string]press{}
	b.inbound = make(chan inbound, 256)
	b.setStatus(func(s *Status) {})
	go b.work(ctx)
	g := &qq.Gateway{
		Client:  b.QQ,
		Intents: qq.IntentC2C | qq.IntentInteraction,
		Session: b.state.Gateway,
		URL:     b.GatewayURL,
		Save: func(s qq.Session) {
			b.mu.Lock()
			b.state.Gateway = s
			b.mu.Unlock()
			b.save()
		},
		Event: b.event,
		State: func(ok bool, err error) {
			b.setStatus(func(s *Status) {
				s.QQ, s.QQError = ok, ""
				if err != nil {
					s.QQError = err.Error()
				}
			})
			if ok {
				b.logf("qq: connected")
				go b.avatar(ctx)
				go b.menus(ctx)
			} else if err != nil {
				b.logf("qq: disconnected: %v", err)
			}
		},
	}
	go func() { _ = g.Run(ctx) }()
	b.events(ctx)
	return ctx.Err()
}

func (b *Bridge) save() {
	b.mu.Lock()
	b.state.AppID = b.Config.AppID
	s := b.state
	err := Write(b.Home, StateFile, &s)
	b.mu.Unlock()
	if err != nil {
		b.logf("state: %v", err)
	}
}

func (b *Bridge) setStatus(f func(*Status)) {
	b.mu.Lock()
	f(&b.status)
	b.status.At = time.Now().UnixMilli()
	b.status.PID = osPID()
	s := b.status
	b.mu.Unlock()
	if err := Write(b.Home, StatusFile, &s); err != nil {
		b.logf("status: %v", err)
	}
}

// event takes a gateway dispatch. It must not block the connection's reading: a button press is
// answered to QQ at once (it shows a failure after 3 s), and the rest waits its turn in order.
func (b *Bridge) event(kind, id string, data json.RawMessage) {
	switch kind {
	case "C2C_MESSAGE_CREATE":
	case "INTERACTION_CREATE":
		var e struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &e)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := b.QQ.Ack(ctx, e.ID); err != nil {
				b.logf("qq: answer a button press: %v", err)
			}
		}()
	default:
		return
	}
	select {
	case b.inbound <- inbound{kind, id, data}:
	default:
		b.logf("qq: too much at once, a %s dropped", kind)
	}
}

func (b *Bridge) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case in := <-b.inbound:
			switch in.kind {
			case "C2C_MESSAGE_CREATE":
				b.message(ctx, in.data)
			case "INTERACTION_CREATE":
				b.press(ctx, in.data)
			}
		}
	}
}

type c2cMessage struct {
	ID     string `json:"id"`
	Author struct {
		OpenID string `json:"user_openid"`
	} `json:"author"`
	Content     string `json:"content"`
	Attachments []struct {
		ContentType string `json:"content_type"`
		Filename    string `json:"filename"`
		URL         string `json:"url"`
		Size        int64  `json:"size"`
		ASR         string `json:"asr_refer_text"`
	} `json:"attachments"`
}

// message takes a private message: an owner's goes to Iridium; anyone else's only pairs.
func (b *Bridge) message(ctx context.Context, data json.RawMessage) {
	var m c2cMessage
	if json.Unmarshal(data, &m) != nil || m.Author.OpenID == "" {
		return
	}
	who := m.Author.OpenID
	text := strings.TrimSpace(m.Content)
	if !b.isOwner(who) {
		if !TakePair(b.Home, text) {
			b.logf("qq: a message from someone not an owner, dropped")
			return
		}
		if err := b.addOwner(who); err != nil {
			b.logf("pair: %v", err)
			return
		}
		b.received(who, m.ID)
		b.deliver(ctx, who, Outgoing{Text: "已配对。之后在这里说的话交给 Iridium。"})
		return
	}
	b.received(who, m.ID)
	b.flush(ctx, who)
	chat := chatKey(who)
	var asr []string
	var refs []string
	var notes []string
	for i, a := range m.Attachments {
		if a.ASR = strings.TrimSpace(a.ASR); a.ASR != "" {
			asr = append(asr, a.ASR)
		}
		name := attachmentName(a.Filename, a.ContentType, i)
		ref, err := b.upload(ctx, chat, name, a.URL)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusConflict {
			// No session chosen: the input below gets the sessions to pick from.
			notes = append(notes, fmt.Sprintf("（附件 %s 没传：先选会话）", name))
			continue
		}
		if err != nil {
			b.logf("attachment %s: %v", name, err)
			notes = append(notes, fmt.Sprintf("（附件 %s 没传上：%v）", name, err))
			continue
		}
		refs = append(refs, ref)
	}
	if len(asr) > 0 {
		text = strings.TrimSpace(text + "\n[语音转写] " + strings.Join(asr, " "))
	}
	if len(notes) > 0 {
		text = strings.TrimSpace(text + "\n" + strings.Join(notes, "\n"))
	}
	if len(refs) == 0 && b.answerByName(ctx, who, text) {
		return
	}
	if text == "" && len(refs) == 0 {
		return
	}
	r, err := b.IR.Input(ctx, chat, api.ChatInput{Text: text, Attachments: refs})
	if err != nil {
		b.deliver(ctx, who, Outgoing{Text: failure(err)})
		return
	}
	b.answered(ctx, who, r.Reply, r.Card)
}

// failure is what the chat is told when Iridium said no.
func failure(err error) string {
	var e *APIError
	switch {
	case errors.Is(err, ErrToken):
		return "Iridium 不认这个插件的 TOKEN：在电脑上运行 iridium-qq setup。"
	case errors.As(err, &e):
		return "Iridium：" + e.Message
	}
	return "连不上 Iridium：" + err.Error()
}

func attachmentName(name, ctype string, i int) string {
	if name = path.Base(strings.ReplaceAll(name, "\\", "/")); name != "" && name != "." && name != "/" {
		return name
	}
	ext := ""
	if exts, _ := mime.ExtensionsByType(ctype); len(exts) > 0 {
		ext = exts[len(exts)-1]
	}
	if ctype == "image/jpeg" {
		ext = ".jpg"
	}
	return fmt.Sprintf("qq-%s-%d%s", time.Now().Format("20060102-150405"), i+1, ext)
}

func (b *Bridge) upload(ctx context.Context, chat, name, url string) (string, error) {
	data, err := qq.Download(ctx, b.HTTP, url, maxIn)
	if err != nil {
		return "", err
	}
	return b.IR.Upload(ctx, chat, name, data)
}

func (b *Bridge) addOwner(openid string) error {
	c, _, err := LoadConfig(b.Home)
	if err != nil {
		return err
	}
	if !c.IsOwner(openid) {
		c.Owners = append(c.Owners, openid)
	}
	if err := Write(b.Home, ConfigFile, &c); err != nil {
		return err
	}
	b.mu.Lock()
	b.Config.Owners = c.Owners
	b.mu.Unlock()
	b.logf("pair: a new owner")
	return nil
}

// received notes the owner's newest message: the passive replies go to it from now on.
func (b *Bridge) received(openid, msgID string) {
	b.mu.Lock()
	cs := b.chat(openid)
	cs.MsgID, cs.MsgAt, cs.Used = msgID, time.Now().UnixMilli(), 0
	b.mu.Unlock()
	b.save()
}

func (b *Bridge) chat(openid string) *ChatState {
	cs := b.state.Chats[openid]
	if cs == nil {
		cs = &ChatState{}
		b.state.Chats[openid] = cs
	}
	return cs
}

// flush sends what waited for the owner's next message.
func (b *Bridge) flush(ctx context.Context, openid string) {
	b.mu.Lock()
	cs := b.chat(openid)
	w := cs.Waiting
	cs.Waiting = nil
	b.mu.Unlock()
	if len(w) == 0 {
		return
	}
	b.save()
	for _, o := range w {
		b.deliver(ctx, openid, o)
	}
}

// answered sends what Iridium answered at once: a command's reply or card.
func (b *Bridge) answered(ctx context.Context, openid string, r *api.ChatReply, c *api.Card) {
	if r != nil {
		b.reply(ctx, openid, chatKey(openid), *r)
	}
	if c != nil {
		b.card(ctx, openid, chatKey(openid), *c)
	}
}

// ---- Iridium's events ----

func (b *Bridge) events(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		b.mu.Lock()
		last := b.state.LastEvent
		b.mu.Unlock()
		if last == 0 {
			last = -1
		}
		err := b.IR.Events(ctx, last, func() {
			wait = time.Second
			b.setStatus(func(s *Status) { s.Iridium, s.IridiumError = true, "" })
			b.logf("iridium: events connected")
			go b.avatar(ctx)
			go b.menus(ctx)
		}, func(e Event) error {
			b.handle(ctx, e)
			b.mu.Lock()
			b.state.LastEvent = e.ID
			b.mu.Unlock()
			b.save()
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		b.setStatus(func(s *Status) { s.Iridium, s.IridiumError = false, err.Error() })
		b.logf("iridium: events: %v", err)
		if errors.Is(err, ErrToken) {
			wait = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

func (b *Bridge) handle(ctx context.Context, e Event) {
	openid, ok := openidOf(e.Data.Chat)
	if !ok || !b.isOwner(openid) {
		return
	}
	switch e.Kind {
	case "reply":
		if e.Data.Reply != nil {
			b.reply(ctx, openid, e.Data.Chat, *e.Data.Reply)
		}
	case "card":
		if e.Data.Card != nil {
			b.card(ctx, openid, e.Data.Chat, *e.Data.Card)
		}
	case "card_done":
		b.mu.Lock()
		if c, ok := b.cards[e.Data.Chat]; ok && c.ID == e.Data.CardID {
			delete(b.cards, e.Data.Chat)
		}
		b.mu.Unlock()
	case "error":
		b.deliver(ctx, openid, Outgoing{Text: "出错：" + e.Data.Text})
	}
}

func (b *Bridge) reply(ctx context.Context, openid, chat string, r api.ChatReply) {
	if strings.TrimSpace(r.Markdown) != "" || strings.TrimSpace(r.Text) != "" {
		b.deliver(ctx, openid, Outgoing{Markdown: r.Markdown, Text: r.Text})
	}
	for _, f := range r.Files {
		b.deliver(ctx, openid, Outgoing{File: &OutFile{Chat: chat, Ref: f.Ref, Name: f.Name, Type: f.Type, Size: f.Size}})
	}
}

// ---- cards ----

// card shows a card: its options drawn as a tree under its title, no numbers (the user,
// 2026-11-08); with Markdown a button per option answers it, and so does a reply of an option's
// name.
func (b *Bridge) card(ctx context.Context, openid, chat string, c api.Card) {
	b.mu.Lock()
	b.cards[chat] = c
	b.mu.Unlock()
	md, text := cardText(c)
	b.deliver(ctx, openid, Outgoing{Markdown: md, Text: text, card: &c})
}

func groups(c api.Card) int {
	seen := map[int]bool{}
	for _, o := range c.Options {
		seen[o.Group] = true
	}
	return len(seen)
}

// mdEscape keeps text as written in QQ's Markdown (Rhodium's literal, for one line).
var mdEscape = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "~", `\~`, "<", `\<`)

// optionLine is an option as the tree shows it: its text, then its note, a space between.
func optionLine(o api.CardOption, md bool) string {
	line := o.Text
	if md {
		line = mdEscape.Replace(o.Text)
		if o.Primary {
			line = "**" + line + "**"
		}
	}
	if o.Description != "" {
		d := o.Description
		if md {
			d = mdEscape.Replace(d)
		}
		line += " " + d
	}
	return line
}

// tree draws the options: one branch each, or a branch per question with its options under it.
func tree(c api.Card, md bool) []string {
	var lines []string
	branch := func(last bool) string {
		if last {
			return "└── "
		}
		return "├── "
	}
	if groups(c) <= 1 {
		for i, o := range c.Options {
			lines = append(lines, branch(i == len(c.Options)-1)+optionLine(o, md))
		}
		return lines
	}
	var by [][]api.CardOption
	group := -1
	for _, o := range c.Options {
		if o.Group != group {
			group = o.Group
			by = append(by, nil)
		}
		by[len(by)-1] = append(by[len(by)-1], o)
	}
	for i, opts := range by {
		last := i == len(by)-1
		lines = append(lines, branch(last)+fmt.Sprintf("问题 %d", i+1))
		// The indent as no-break spaces: QQ's Markdown takes four spaces for a code block.
		indent := "│\u00a0\u00a0\u00a0"
		if last {
			indent = "\u00a0\u00a0\u00a0\u00a0"
		}
		for j, o := range opts {
			lines = append(lines, indent+branch(j == len(opts)-1)+optionLine(o, md))
		}
	}
	return lines
}

func cardText(c api.Card) (md, text string) {
	m := []string{"**" + mdEscape.Replace(c.Title) + "**"}
	t := []string{c.Title}
	if len(c.Summary) > 0 {
		m = append(m, strings.Join(c.Summary, "\n"))
		t = append(t, strings.Join(c.Summary, "\n"))
	}
	// Each line break a hard one: two spaces before it.
	m = append(m, strings.Join(tree(c, true), "  \n"))
	t = append(t, strings.Join(tree(c, false), "\n"))
	if groups(c) > 1 {
		const hint = "每个问题回一个选项，逗号隔开。"
		m, t = append(m, hint), append(t, hint)
	}
	return strings.Join(m, "\n\n"), strings.Join(t, "\n\n")
}

// keyboard is a button per option of a one-question card, at most 10 (5 rows of 2).
func (b *Bridge) keyboard(c *api.Card, chat string) *qq.Keyboard {
	if c == nil || groups(*c) > 1 || len(c.Options) == 0 || len(c.Options) > 10 {
		return nil
	}
	per := 1
	if len(c.Options) > 5 {
		per = 2
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var rows []qq.Row
	for i, o := range c.Options {
		b.nkey++
		key := "k" + strconv.Itoa(b.nkey)
		b.keys[key] = press{chat: chat, card: c.ID, option: o.ID}
		label := o.Text
		if r := []rune(label); len(r) > 16 {
			label = string(r[:15]) + "…"
		}
		style := 0
		if o.Primary {
			style = 1
		}
		btn := qq.Button{ID: strconv.Itoa(i + 1), RenderData: qq.RenderData{Label: label, VisitedLabel: "✓ " + label, Style: style},
			Action: qq.ButtonAction{Type: 1, Permission: qq.ButtonPermission{Type: 2}, Data: key}}
		if len(rows) == 0 || len(rows[len(rows)-1].Buttons) >= per {
			rows = append(rows, qq.Row{})
		}
		rows[len(rows)-1].Buttons = append(rows[len(rows)-1].Buttons, btn)
	}
	return &qq.Keyboard{Content: qq.KeyboardContent{Rows: rows}}
}

// names are what a reply may call an option: its text, without its tag ("[CC] ") or a folder's
// "/" at its end; lower case.
func names(o api.CardOption) []string {
	t := strings.ToLower(strings.TrimSpace(o.Text))
	out := []string{t}
	if strings.HasPrefix(t, "[") {
		if i := strings.Index(t, "] "); i > 0 {
			out = append(out, strings.TrimSpace(t[i+2:]))
		}
	}
	if s := strings.TrimSuffix(t, "/"); s != t {
		out = append(out, s)
	}
	return out
}

// pickByName is the option named so; in a card of more questions, of the ones not yet answered.
func pickByName(opts []api.CardOption, name string) (api.CardOption, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, o := range opts {
		if slices.Contains(names(o), name) {
			return o, true
		}
	}
	return api.CardOption{}, false
}

// answerByName answers the chat's open card if text names its options: one, or one a question
// with commas between ("允许, 继续"). Names are seldom the same (the user, 2026-11-08): the first
// such one is it.
func (b *Bridge) answerByName(ctx context.Context, openid, text string) bool {
	chat := chatKey(openid)
	b.mu.Lock()
	c, ok := b.cards[chat]
	b.mu.Unlock()
	text = strings.TrimSpace(text)
	if !ok || text == "" {
		return false
	}
	a := api.CardAnswer{Card: c.ID}
	if o, ok := pickByName(c.Options, text); ok {
		if groups(c) > 1 {
			return false
		}
		a.Option = o.ID
		b.answer(ctx, openid, chat, a)
		return true
	}
	if groups(c) <= 1 {
		return false
	}
	parts := strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(",，、;；\n", r) })
	for _, part := range parts {
		o, ok := pickByName(c.Options, part)
		if !ok {
			return false
		}
		a.Choices = append(a.Choices, o.ID)
	}
	b.answer(ctx, openid, chat, a)
	return true
}

func (b *Bridge) answer(ctx context.Context, openid, chat string, a api.CardAnswer) {
	r, err := b.IR.Answer(ctx, a)
	if err != nil {
		var e *APIError
		if errors.As(err, &e) && (e.Status == http.StatusGone || e.Status == http.StatusNotFound) {
			b.deliver(ctx, openid, Outgoing{Text: "这个提问已经结束。"})
		} else {
			b.deliver(ctx, openid, Outgoing{Text: failure(err)})
		}
		b.mu.Lock()
		if c, ok := b.cards[chat]; ok && c.ID == a.Card {
			delete(b.cards, chat)
		}
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	if c, ok := b.cards[chat]; ok && c.ID == a.Card {
		delete(b.cards, chat)
	}
	b.mu.Unlock()
	b.answered(ctx, openid, r.Reply, r.Card)
}

func (b *Bridge) press(ctx context.Context, data json.RawMessage) {
	var e struct {
		Scene  string `json:"scene"`
		OpenID string `json:"user_openid"`
		Data   struct {
			Resolved struct {
				ButtonData string `json:"button_data"`
			} `json:"resolved"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &e) != nil || e.OpenID == "" || !b.isOwner(e.OpenID) {
		return
	}
	b.mu.Lock()
	p, ok := b.keys[e.Data.Resolved.ButtonData]
	b.mu.Unlock()
	if !ok || p.chat != chatKey(e.OpenID) {
		b.deliver(ctx, e.OpenID, Outgoing{Text: "这个按钮已失效。"})
		return
	}
	b.answer(ctx, e.OpenID, p.chat, api.CardAnswer{Card: p.card, Option: p.option})
}

// ---- avatar ----

// avatar puts the bot's avatar on its terminal in Iridium, once per avatar and TOKEN (a terminal
// made again has none): as QQ or Iridium connects, so whichever comes second does it.
func (b *Bridge) avatar(ctx context.Context) {
	b.avatarMu.Lock()
	defer b.avatarMu.Unlock()
	me, err := b.QQ.Me(ctx)
	if err != nil {
		b.logf("avatar: %v", err)
		return
	}
	b.setStatus(func(s *Status) { s.Bot = me.Username })
	if me.Avatar == "" {
		return
	}
	// Marked by the image itself (QQ's URL changes from call to call) and the TOKEN.
	img, err := qq.Download(ctx, b.HTTP, me.Avatar, 4<<20)
	if err != nil {
		b.logf("avatar: %v", err)
		return
	}
	sum := sha256.Sum256(append(img, b.IR.Token()...))
	mark := hex.EncodeToString(sum[:8])
	b.mu.Lock()
	done := b.state.Avatar == mark
	b.mu.Unlock()
	if done {
		return
	}
	if err := b.IR.SetAvatar(ctx, img); err != nil {
		b.logf("avatar: %v", err)
		return
	}
	b.mu.Lock()
	b.state.Avatar = mark
	b.mu.Unlock()
	b.save()
	b.logf("avatar: set")
}
