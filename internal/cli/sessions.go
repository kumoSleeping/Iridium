package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Exit codes of wait (and new, send with --wait).
const (
	exitDone  = 0
	exitError = 1 // the agent ended badly, or exited, or ir failed
	exitNeeds = 2 // an ask is open: it needs you
)

// ir new <agent> [dir] "task"
func cmdNew(e *env, args []string) int {
	o, err := parse(args, "wait model approve")
	if err != nil {
		return e.fail(err)
	}
	if len(o.pos) < 1 || len(o.pos) > 3 {
		return e.failf(`usage: ir new <agent> [目录] "任务"`)
	}
	req := api.NewSession{Agent: api.Agent(o.pos[0]), Model: o.model, Mode: o.approve, Caller: caller()}
	dir := "."
	switch len(o.pos) {
	case 2:
		req.Text = o.pos[1]
	case 3:
		dir, req.Text = o.pos[1], o.pos[2]
	}
	if req.Cwd, err = localPath(dir); err != nil {
		return e.fail(err)
	}
	var created api.NewSessionReply
	if err := e.do("POST", "/api/sessions", req, &created); err != nil {
		return e.fail(err)
	}
	fmt.Fprintln(e.out, short(created.ID, e.ids()))
	if strings.TrimSpace(req.Text) == "" {
		return exitDone
	}
	if o.wait {
		return e.waitTurn(created.ID, anyInput)
	}
	return e.waitTaken(created.ID, anyInput)
}

// ir send <id> "message" [-a file]... [--steer]: while a turn runs the message waits in the
// queue for it to end, unless --steer, which joins the running turn.
func cmdSend(e *env, args []string) int {
	o, err := parse(args, "wait attach steer")
	if err != nil {
		return e.fail(err)
	}
	if len(o.pos) < 1 || len(o.pos) > 2 || (len(o.pos) == 1 && len(o.attach) == 0) {
		return e.failf(`usage: ir send <id> "消息" [-a 文件]`)
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	req := api.Send{Caller: caller(), Steer: o.steer}
	if len(o.pos) == 2 {
		req.Text = o.pos[1]
	}
	for _, a := range o.attach {
		p, err := localPath(a)
		if err != nil {
			return e.fail(err)
		}
		var f api.FileRef
		if err := e.do("POST", "/api/sessions/"+id+"/attach", api.AttachPath{Path: p}, &f); err != nil {
			return e.failf("%s: %v", a, err)
		}
		req.Attachments = append(req.Attachments, f.Ref)
	}
	var sent api.SendReply
	if err := e.do("POST", "/api/sessions/"+id+"/send", req, &sent); err != nil {
		return e.fail(err)
	}
	if sent.Queued {
		if !o.wait {
			return exitDone // it goes when the running turn ends
		}
		if code := e.waitQueued(id, sent.Input); code >= 0 {
			return code
		}
	}
	if o.wait {
		return e.waitTurn(id, sent.Input)
	}
	return e.waitTaken(id, sent.Input)
}

// waitQueued waits for a queued message to leave the queue: -1 once it went to the agent, else
// the exit code (paused: it needs you to continue).
func (e *env) waitQueued(id, input string) int {
	for {
		var st api.SessionState
		if err := e.do("GET", "/api/sessions/"+id, nil, &st); err != nil {
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

// ir wait <id>
func cmdWait(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 1 {
		return e.failf("usage: ir wait <id>")
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	return e.waitTurn(id, "")
}

// anyInput: the message waited on is the session's first, whatever its id.
const anyInput = "*"

func busy(s api.Status) bool {
	return s == api.StatusStarting || s == api.StatusRunning || s == api.StatusCompacting
}

const poll = 200 * time.Millisecond

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
		if err := e.do("GET", "/api/sessions/"+id, nil, &st); err != nil {
			p.clear()
			return e.fail(err)
		}
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
			stable := time.Since(idleAt) >= 3*poll/2
			if seenBusy || (taken && (input == "" || stable)) {
				p.clear()
				return e.printReply(id, input)
			}
			if !st.Live && time.Since(idleAt) > 10*time.Second {
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
		if err := e.do("GET", "/api/sessions/"+id, nil, &st); err != nil {
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

func (e *env) latest(id string) (api.MessagePage, error) {
	var page api.MessagePage
	err := e.do("GET", "/api/sessions/"+id+"/messages", nil, &page)
	return page, err
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
	page, err := e.latest(id)
	if err != nil {
		return e.fail(err)
	}
	from := 0
	if input != "" {
		from = max(0, userAt(page, input))
	}
	for i := len(page.Blocks) - 1; i >= from; i-- {
		if b := page.Blocks[i]; b.Kind == api.BlockKindAssistant {
			fmt.Fprintln(e.out, strings.TrimRight(b.Text, "\n"))
			break
		}
	}
	return exitDone
}

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

// ir read <id>: the latest page, plainly.
func cmdRead(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 1 {
		return e.failf("usage: ir read <id>")
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	page, err := e.latest(id)
	if err != nil {
		return e.fail(err)
	}
	for _, b := range page.Blocks {
		switch b.Kind {
		case api.BlockKindUser:
			from := ""
			if b.From != "" {
				from = "[" + b.From + "] "
			}
			fmt.Fprintf(e.out, "> %s%s\n\n", from, strings.ReplaceAll(strings.TrimRight(b.Text, "\n"), "\n", "\n> "))
		case api.BlockKindAssistant:
			fmt.Fprintf(e.out, "%s\n\n", strings.TrimRight(b.Text, "\n"))
		case api.BlockKindTool:
			fmt.Fprintf(e.out, "· %s\n\n", b.Name)
		case api.BlockKindSystem:
			if b.Event == api.SystemEventCompact || b.Event == api.SystemEventInterrupted || b.Event == api.SystemEventAPIError {
				fmt.Fprintf(e.out, "— %s\n\n", b.Event)
			}
		}
	}
	return exitDone
}

// ir ls: one session a line: id, agent, status, directory, name or title.
func cmdLs(e *env, args []string) int {
	if o, err := parse(args, ""); err != nil || len(o.pos) != 0 {
		return e.failf("usage: ir ls")
	}
	var list []api.Session
	if err := e.do("GET", "/api/sessions", nil, &list); err != nil {
		return e.fail(err)
	}
	ids := make([]string, len(list))
	for i, s := range list {
		ids[i] = s.ID
	}
	home, _ := os.UserHomeDir()
	for _, s := range list {
		cwd := s.Cwd
		if home != "" && (cwd == home || strings.HasPrefix(cwd, home+"/")) {
			cwd = "~" + strings.TrimPrefix(cwd, home)
		}
		title := s.Name
		if title == "" {
			title = s.Title
		}
		fmt.Fprintf(e.out, "%s\t%s\t%s\t%s\t%s\n", short(s.ID, ids), s.Agent, s.Status, cwd, title)
	}
	return exitDone
}

// ir stop <id>
func cmdStop(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 1 {
		return e.failf("usage: ir stop <id>")
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	if err := e.do("POST", "/api/sessions/"+id+"/stop", nil, nil); err != nil {
		return e.fail(err)
	}
	return exitDone
}
