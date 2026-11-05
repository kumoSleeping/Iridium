package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Cards: an ask as the web's confirmation panel and plugins show it (plan "确认面板", "插件"). One
// model for both: a title, a few lines of summary, options with ids.

// Option ids of an ask's card.
const (
	OptionAllow   = "allow"
	OptionDeny    = "deny"
	OptionAlways  = "always"
	OptionApprove = "approve"
	OptionReject  = "reject"
)

// AskCardID names an ask's card: "ask:<session>:<request id>".
func AskCardID(session, request string) string { return "ask:" + session + ":" + request }

// CardOf is ask's card. A question's options are "<question>.<option>" by index, grouped by
// question.
func CardOf(session string, a api.Ask) *api.Card {
	c := &api.Card{ID: AskCardID(session, a.RequestID), Kind: a.Kind, Session: session, Title: a.Tool, Summary: []string{}}
	in, _ := a.Input.(map[string]any)
	switch a.Kind {
	case "question":
		qs, _ := in["questions"].([]any)
		c.Title = "提问"
		for i, q := range qs {
			q, _ := q.(map[string]any)
			text, _ := q["question"].(string)
			c.Summary = append(c.Summary, text)
			if multi, _ := q["multiSelect"].(bool); multi {
				c.Multi = append(c.Multi, i)
			}
			opts, _ := q["options"].([]any)
			for j, o := range opts {
				o, _ := o.(map[string]any)
				label, _ := o["label"].(string)
				desc, _ := o["description"].(string)
				c.Options = append(c.Options, api.CardOption{ID: fmt.Sprintf("%d.%d", i, j), Text: label, Description: desc, Group: i, Primary: j == 0})
			}
		}
	case "plan":
		c.Title = "计划"
		plan, _ := in["plan"].(string)
		c.Summary = firstLines(plan, 6)
		c.Options = []api.CardOption{{ID: OptionApprove, Text: "批准", Primary: true}, {ID: OptionReject, Text: "拒绝"}}
	default:
		if s := subjectOf(a); s != "" {
			c.Summary = append(c.Summary, cutText(s, 300))
		} else if b, err := json.Marshal(a.Input); err == nil && string(b) != "null" {
			c.Summary = append(c.Summary, cutText(string(b), 300))
		}
		if a.Description != "" && a.Description != subjectOf(a) {
			c.Summary = append(c.Summary, cutText(a.Description, 200))
		}
		always := "本会话一直允许"
		if p := patternOf(a); p != "" {
			always += "：" + p
		}
		c.Options = []api.CardOption{{ID: OptionAllow, Text: "允许", Primary: true}, {ID: OptionDeny, Text: "拒绝"}, {ID: OptionAlways, Text: always}}
	}
	return c
}

// AnswerCard answers an ask's card.
func (m *Manager) AnswerCard(ctx context.Context, ans api.CardAnswer) error {
	rest, ok := strings.CutPrefix(ans.Card, "ask:")
	session, request, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 {
		return fmt.Errorf("%w: no card %q", ErrBadRequest, ans.Card)
	}
	h, err := m.get(session)
	if err != nil {
		return err
	}
	h.mu.Lock()
	i := slices.IndexFunc(h.asks, func(a api.Ask) bool { return a.RequestID == request })
	var ask api.Ask
	if i >= 0 {
		ask = h.asks[i]
	}
	h.mu.Unlock()
	if i < 0 {
		return fmt.Errorf("%w: the card is no longer open", ErrGone)
	}
	a := api.Answer{RequestID: request, Kind: ask.Kind, Message: ans.Message}
	choices := ans.Choices
	if ans.Option != "" {
		choices = append([]string{ans.Option}, choices...)
	}
	card := CardOf(session, ask)
	switch ask.Kind {
	case "question":
		a.Answers = map[string]string{}
		for _, id := range choices {
			j := slices.IndexFunc(card.Options, func(o api.CardOption) bool { return o.ID == id })
			if j < 0 {
				return fmt.Errorf("%w: no option %q", ErrBadRequest, id)
			}
			o := card.Options[j]
			q := card.Summary[o.Group]
			if prev, ok := a.Answers[q]; ok && slices.Contains(card.Multi, o.Group) {
				a.Answers[q] = prev + ", " + o.Text
			} else {
				a.Answers[q] = o.Text
			}
		}
		if len(a.Answers) != len(card.Summary) {
			return fmt.Errorf("%w: answer each of the %d questions", ErrBadRequest, len(card.Summary))
		}
	case "plan":
		a.Allow = len(choices) == 1 && choices[0] == OptionApprove
		if !a.Allow && (len(choices) != 1 || choices[0] != OptionReject) {
			return fmt.Errorf("%w: approve or reject", ErrBadRequest)
		}
	default:
		if len(choices) != 1 {
			return fmt.Errorf("%w: pick one option", ErrBadRequest)
		}
		switch choices[0] {
		case OptionAllow:
			a.Allow = true
		case OptionAlways:
			a.Allow, a.Always = true, true
		case OptionDeny:
		default:
			return fmt.Errorf("%w: no option %q", ErrBadRequest, choices[0])
		}
	}
	return m.Answer(ctx, session, a)
}

// ErrGone: what the request names was there and is no more (an ask answered elsewhere).
var ErrGone = errors.New("gone")

func firstLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, cutText(l, 200))
		}
		if len(out) == n {
			break
		}
	}
	return out
}

func cutText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
