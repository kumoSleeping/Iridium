package qqbridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/qq"
)

func osPID() int { return os.Getpid() }

// isOwner reads the owners under the lock (pairing adds to them).
func (b *Bridge) isOwner(openid string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Config.IsOwner(openid)
}

// errNoQuota: neither a passive reply nor an active message went; it waits for the owner's next
// message.
var errNoQuota = errors.New("no quota")

// deliver sends o to openid's chat, in pieces QQ takes. What finds no quota waits for the owner's
// next message (flush); anything else that fails is logged and dropped.
func (b *Bridge) deliver(ctx context.Context, openid string, o Outgoing) {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	if o.File != nil {
		b.sendFile(ctx, openid, o)
		return
	}
	md := b.markdown()
	var pieces []string
	if md && strings.TrimSpace(o.Markdown) != "" {
		pieces = Chunks(o.Markdown, qq.MaxText)
	} else {
		md = false
		pieces = Chunks(firstOf(o.Text, o.Markdown), qq.MaxText)
	}
	plain := Chunks(firstOf(o.Text, o.Markdown), qq.MaxText)
	for i, p := range pieces {
		m := qq.Message{Type: qq.MsgText, Content: p}
		fallback := p
		if md {
			m = qq.Message{Type: qq.MsgMarkdown, Markdown: &qq.Markdown{Content: p}}
			fallback = ""
			if i < len(plain) && len(pieces) == len(plain) {
				fallback = plain[i]
			}
			if i == len(pieces)-1 && o.card != nil {
				m.Keyboard = b.keyboard(o.card, chatKey(openid))
			}
		}
		if fallback == "" {
			fallback = p
		}
		err := b.sendOne(ctx, openid, m, fallback)
		if errors.Is(err, errNoQuota) {
			rest := strings.Join(pieces[i:], "\n")
			b.wait(openid, Outgoing{Markdown: rest, Text: rest})
			return
		}
		if err != nil {
			b.logf("qq: send: %v", err)
			return
		}
	}
}

func firstOf(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (b *Bridge) wait(openid string, o Outgoing) {
	b.mu.Lock()
	cs := b.chat(openid)
	cs.Waiting = append(cs.Waiting, o)
	b.mu.Unlock()
	b.save()
	b.logf("qq: no quota left, a message waits for the next one from the owner")
}

// markdown reports whether to try Markdown: unless QQ refused it lately.
func (b *Bridge) markdown() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Markdown != "no" || time.Since(time.UnixMilli(b.state.MarkdownAt)) > markdownRetry
}

func (b *Bridge) setMarkdown(ok bool) {
	b.mu.Lock()
	v := "no"
	if ok {
		v = "yes"
	}
	changed := b.state.Markdown != v
	b.state.Markdown, b.state.MarkdownAt = v, time.Now().UnixMilli()
	b.mu.Unlock()
	if changed {
		b.save()
		b.logf("qq: markdown %s", v)
	}
}

// passive is the message a passive reply goes to now and its msg_seq, or "" when none is left
// (the window is over or used up). It counts the reply as made.
func (b *Bridge) passive(openid string) (string, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cs := b.chat(openid)
	if cs.MsgID == "" || cs.Used >= passiveLimit || time.Since(time.UnixMilli(cs.MsgAt)) > passiveTTL {
		return "", 0
	}
	cs.Used++
	return cs.MsgID, cs.Used
}

func (b *Bridge) spend(openid string) {
	b.mu.Lock()
	b.chat(openid).Used = passiveLimit
	b.mu.Unlock()
}

// sendOne sends one message: a passive reply while there is one, else an active message. A
// Markdown one QQ refuses goes again as plain text (fallback); a passive reply whose fate is
// unknown goes once more under the same msg_seq, which QQ dedupes on.
func (b *Bridge) sendOne(ctx context.Context, openid string, m qq.Message, fallback string) error {
	defer b.save()
	for active := false; ; active = true {
		m.MsgID, m.MsgSeq = "", 0
		if !active {
			m.MsgID, m.MsgSeq = b.passive(openid)
			if m.MsgID == "" {
				continue
			}
		}
		_, err := b.QQ.Send(ctx, openid, m)
		if err == nil {
			if m.Type == qq.MsgMarkdown {
				b.setMarkdown(true)
			}
			return nil
		}
		if m.Type == qq.MsgMarkdown && qq.Refused(err) {
			p := qq.Message{Type: qq.MsgText, Content: fallback}
			if !active {
				p.MsgID, p.MsgSeq = b.passive(openid)
			}
			if p.MsgID != "" || active {
				if _, perr := b.QQ.Send(ctx, openid, p); perr == nil {
					b.setMarkdown(false)
					return nil
				} else if !qq.Refused(perr) {
					return perr
				}
			}
			// Plain text refused too: the quota, not the Markdown.
		}
		if !qq.Refused(err) {
			if m.MsgID == "" {
				return err // an active message's fate unknown: not sent again
			}
			if _, err2 := b.QQ.Send(ctx, openid, m); err2 == nil || isDeduped(err2) {
				return nil
			}
			return fmt.Errorf("outcome unknown: %w", err)
		}
		if active {
			return errNoQuota
		}
		b.spend(openid) // QQ counts otherwise: this message's passive replies are used up
	}
}

func isDeduped(err error) bool {
	var e *qq.Error
	return errors.As(err, &e) && e.Code == qq.Deduped
}

// sendFile sends a reply's file: an image as one, anything else as a file.
func (b *Bridge) sendFile(ctx context.Context, openid string, o Outgoing) {
	f := o.File
	say := func(text string) {
		_ = b.sendOne(ctx, openid, qq.Message{Type: qq.MsgText, Content: text}, text)
	}
	if f.Size > qq.MaxUpload {
		say(fmt.Sprintf("%s 有 %.1f MB，QQ 一次最多发 %d MB。", f.Name, float64(f.Size)/(1<<20), qq.MaxUpload>>20))
		return
	}
	data, err := b.IR.File(ctx, f.Chat, f.Ref, qq.MaxUpload)
	if err != nil {
		say(fmt.Sprintf("%s 取不到：%v", f.Name, err))
		return
	}
	kind := qq.FileFile
	if strings.HasPrefix(f.Type, "image/") {
		kind = qq.FileImage
	}
	info, err := b.QQ.Upload(ctx, openid, kind, data, f.Name)
	if err != nil {
		b.logf("qq: upload %s: %v", f.Name, err)
		say(fmt.Sprintf("%s 发不出去：%v", f.Name, err))
		return
	}
	m := qq.Message{Type: qq.MsgMedia, Media: &qq.Media{FileInfo: info}}
	if err := b.sendOne(ctx, openid, m, f.Name); errors.Is(err, errNoQuota) {
		b.wait(openid, o)
	} else if err != nil {
		b.logf("qq: send %s: %v", f.Name, err)
	}
}

// Chunks splits s into pieces of at most max runes, at a line break where there is one.
func Chunks(s string, max int) []string {
	s = strings.TrimSpace(s)
	var out []string
	for utf8.RuneCountInString(s) > max {
		r := []rune(s)
		cut := max
		head := string(r[:max])
		if i := strings.LastIndex(head, "\n"); i > 0 && utf8.RuneCountInString(head[:i]) > max/2 {
			cut = utf8.RuneCountInString(head[:i])
		}
		out = append(out, strings.TrimSpace(string(r[:cut])))
		s = strings.TrimSpace(string(r[cut:]))
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
