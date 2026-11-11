package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// Exit codes of wait (and new, send with --wait).
const (
	exitDone  = 0
	exitError = 1 // the agent ended badly, or exited, or ir failed
	exitNeeds = 2 // an ask is open: it needs you
)

// waitQueued waits for a queued message to leave the queue: -1 once it went to the agent, else
// the exit code (paused: it needs you to continue).
func (e *env) waitQueued(id, input string) int {
	for {
		var st api.SessionState
		if err := e.do("GET", "/v1/internal/sessions/"+id, nil, &st); err != nil {
			return e.fail(err)
		}
		i := slices.IndexFunc(st.Queue, func(q api.Queued) bool { return q.ID == input })
		if i < 0 {
			return -1
		}
		if st.Queue[i].Paused {
			fmt.Fprintln(e.out, "需要你：消息在暂停的队列里，继续后才发")
			return exitNeeds
		}
		time.Sleep(poll)
	}
}

// anyInput: the message waited on is the session's first, whatever its id.
const anyInput = "*"

func busy(s api.Status) bool {
	return s == api.StatusStarting || s == api.StatusRunning || s == api.StatusCompacting
}

// poll: how often ir looks at a session it waits on; a var for the tests.
var poll = 200 * time.Millisecond

// waitTurn waits for the turn to end and prints its last reply. input: the message whose turn it
// is, which the agent may not have taken yet ("" for whatever turn runs, anyInput for the first).
// Idle counts as the end once the agent was seen at work, or once the message is in the transcript
// and the agent stayed idle a little.
func (e *env) waitTurn(id, input string) int {
	p := e.progress()
	defer p.clear()
	seenBusy, taken := false, input == ""
	var idleAt time.Time
	for {
		var st api.SessionState
		if err := e.do("GET", "/v1/internal/sessions/"+id, nil, &st); err != nil {
			p.clear()
			return e.fail(err)
		}
		st.Status = turnStatus(&st)
		switch {
		case st.Status == api.StatusWaiting:
			p.clear()
			e.printAsks(st.Asks)
			return exitNeeds
		case st.Status == api.StatusError:
			p.clear()
			return e.failf("the agent ended badly: %s", st.Error)
		case busy(st.Status):
			seenBusy, idleAt = true, time.Time{}
			p.status(string(st.Status))
		default:
			if seenBusy && !st.Live {
				p.clear()
				return e.failf("the agent exited")
			}
			if idleAt.IsZero() {
				idleAt = time.Now()
			}
			if !taken {
				page, err := e.latest(id)
				if err != nil {
					p.clear()
					return e.fail(err)
				}
				taken = userAt(page, input) >= 0
			}
			// Its message there and idle a while, with its agent up: one at another machine shows the
			// message before its agent has started there (a few seconds), idle meanwhile.
			stable := time.Since(idleAt) >= 3*poll/2
			if seenBusy || (taken && st.Live && (input == "" || stable)) {
				p.clear()
				return e.printReply(id, input)
			}
			startup := 10 * time.Second
			if st.Machine != "" {
				startup = 30 * time.Second
			}
			if !st.Live && time.Since(idleAt) > startup {
				p.clear()
				return e.failf("the agent is not running")
			}
			p.status(string(st.Status))
		}
		time.Sleep(poll)
	}
}

// waitTaken waits, at most 15 seconds, until the agent took the message (so a wait right after
// waits for its turn); ir send and ir new without --wait.
func (e *env) waitTaken(id, input string) int {
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(poll) {
		var st api.SessionState
		if err := e.do("GET", "/v1/internal/sessions/"+id, nil, &st); err != nil {
			return e.fail(err)
		}
		if st.Status == api.StatusError {
			return e.failf("the agent ended badly: %s", st.Error)
		}
		if busy(st.Status) || st.Status == api.StatusWaiting {
			return exitDone
		}
		if page, err := e.latest(id); err == nil && userAt(page, input) >= 0 {
			return exitDone
		}
	}
	return exitDone
}

// latest is the latest page of session id's transcript, as blocks (message.list _data.blocks).
func (e *env) latest(id string) (api.MessagePage, error) {
	raw, err := e.satori("message.list", map[string]any{"channel_id": id, "_data": map[string]any{"blocks": true}})
	if err != nil {
		return api.MessagePage{}, err
	}
	var l struct {
		Data satori.PageData `json:"_data"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return api.MessagePage{}, fmt.Errorf("message.list: %s", strings.TrimSpace(string(raw)))
	}
	return api.MessagePage{Blocks: l.Data.Blocks, Start: l.Data.Start, End: l.Data.End, More: l.Data.More}, nil
}

// userAt is where on the page the message input is (anyInput: any of yours); -1 if not there.
func userAt(page api.MessagePage, input string) int {
	for i := len(page.Blocks) - 1; i >= 0; i-- {
		b := page.Blocks[i]
		if b.Kind == api.BlockKindUser && (input == anyInput || b.ID == input) {
			return i
		}
	}
	return -1
}

// printReply prints the last thing the agent said (after input, where it is on the page).
func (e *env) printReply(id, input string) int {
	// A session at another machine: its transcript is mirrored here a moment after the turn ends,
	// so the reply may come a little later than the idle.
	for deadline := time.Now().Add(replyLag); ; time.Sleep(poll / 2) {
		page, err := e.latest(id)
		if err != nil {
			return e.fail(err)
		}
		from := 0
		if input != "" {
			from = userAt(page, input)
			if from < 0 && e.after != "" { // its id not kept: what came after the page as it was
				from = since(page, e.after)
			}
			from = max(0, from)
		}
		for i := len(page.Blocks) - 1; i >= from; i-- {
			if b := page.Blocks[i]; b.Kind == api.BlockKindAssistant {
				fmt.Fprintln(e.out, strings.TrimRight(b.Text, "\n"))
				return exitDone
			}
		}
		if time.Now().After(deadline) {
			return exitDone
		}
	}
}

// replyLag is how long a reply may be awaited past the turn's end (a mirrored transcript's lag).
var replyLag = 5 * time.Second

func (e *env) printAsks(asks []api.Ask) {
	for _, a := range asks {
		what := map[string]string{"permission": "审批", "question": "提问", "plan": "确认计划"}[a.Kind]
		if what == "" {
			what = a.Kind
		}
		in, _ := json.Marshal(a.Input)
		s := string(in)
		if r := []rune(s); len(r) > 300 {
			s = string(r[:300]) + "…"
		}
		fmt.Fprintf(e.out, "需要你：%s %s %s\n", what, a.Tool, s)
	}
}

// clip is text on one line, cut to n runes.
func clip(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

// turnStatus is a session's status as a turn has it: waiting only on a step auto mode denied (its
// card above the page's composer, no ask open), the turn is over: idle.
func turnStatus(st *api.SessionState) api.Status {
	if st.Status == api.StatusWaiting && len(st.Asks) == 0 && len(st.Denials) > 0 {
		return api.StatusIdle
	}
	return st.Status
}

// waitMirrored waits for a turn DSH Desktop runs (a message given to its agent): no turn of
// Iridium's to watch, so for a reply after the page's end before, in the mirrored transcript, and
// that page still for a while (the turn may go on); then prints it.
func (e *env) waitMirrored(id string, before api.MessagePage) int {
	p := e.progress()
	defer p.clear()
	last := ""
	if n := len(before.Blocks); n > 0 {
		last = before.Blocks[n-1].At
	}
	var seen string
	var still time.Time
	for {
		page, err := e.latest(id)
		if err != nil {
			p.clear()
			return e.fail(err)
		}
		from := 0
		if last != "" {
			from = since(page, last)
		}
		var reply string
		for _, b := range page.Blocks[from:] {
			if b.Kind == api.BlockKindAssistant {
				reply = b.Text
			}
		}
		end := ""
		if n := len(page.Blocks); n > 0 {
			end = page.Blocks[n-1].ID + "\x00" + page.Blocks[n-1].Text
		}
		if end != seen {
			seen, still = end, time.Now()
		}
		if reply != "" && time.Since(still) >= desktopSettle {
			p.clear()
			fmt.Fprintln(e.out, strings.TrimRight(reply, "\n"))
			return exitDone
		}
		p.status("DSH Desktop")
		time.Sleep(poll)
	}
}

// desktopSettle: a page still this long after a reply came is taken as the turn's end.
var desktopSettle = 5 * time.Second

// since is the first block of page past at (a block's at: its line in the transcript, a dot, its
// place in the line; assistant blocks have no id to go by).
func since(page api.MessagePage, at string) int {
	for i, b := range page.Blocks {
		if atAfter(b.At, at) {
			return i
		}
	}
	return len(page.Blocks)
}

func atAfter(a, b string) bool {
	al, ai, _ := strings.Cut(a, ".")
	bl, bi, _ := strings.Cut(b, ".")
	an, _ := strconv.ParseInt(al, 10, 64)
	bn, _ := strconv.ParseInt(bl, 10, 64)
	if an != bn {
		return an > bn
	}
	x, _ := strconv.ParseInt(ai, 10, 64)
	y, _ := strconv.ParseInt(bi, 10, 64)
	return x > y
}
