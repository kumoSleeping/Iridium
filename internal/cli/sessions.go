package cli

import (
	"cmp"
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

// ir send <id> "message" [-a file]... [--queue]: while a turn runs the message joins it (steers),
// as Enter does on the page (user 2026-11-06): what agents tell each other is wanted now, and
// steering does not stop the turn. --queue waits in the queue for the turn to end instead, for a
// person at a terminal only: an agent's message always joins (the daemon's send). --steer, the old
// way to ask for it, is still taken.
func cmdSend(e *env, args []string) int {
	o, err := parse(args, "wait attach steer queue")
	if err != nil {
		return e.fail(err)
	}
	if o.steer && o.queue {
		return e.failf("--steer 和 --queue 只能选一个")
	}
	if len(o.pos) < 1 || len(o.pos) > 2 || (len(o.pos) == 1 && len(o.attach) == 0) {
		return e.failf(`usage: ir send <id> "消息" [-a 文件]`)
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	req := api.Send{Caller: caller(), Steer: !o.queue}
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
	if sent.Session != "" { // /clear: it goes on in a new session
		fmt.Fprintln(e.out, short(sent.Session, e.ids()))
		return exitDone
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
		if err := e.do("GET", "/api/sessions/"+id, nil, &st); err != nil {
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

// ir read <id> [-n N] [--full]: by default each message of the latest page with the agent's final
// reply to it, both cut short, the last N (10); a look at what a session did, not its record
// (user 2026-11-06). --full: the page as written, tool calls a line each.
func cmdRead(e *env, args []string) int {
	o, err := parse(args, "n full")
	if err != nil || len(o.pos) != 1 {
		return e.failf("usage: ir read <id> [-n 条数] [--full]")
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	page, err := e.latest(id)
	if err != nil {
		return e.fail(err)
	}
	if o.full {
		e.printPage(page)
		return exitDone
	}
	var st api.SessionState
	_ = e.do("GET", "/api/sessions/"+id, nil, &st)
	st.Status = turnStatus(&st)
	n := o.n
	if n == 0 {
		n = readCount
	}
	ts := turns(page.Blocks)
	if len(ts) > n {
		ts = ts[len(ts)-n:]
	}
	for i, t := range ts {
		if t.asked != "" {
			fmt.Fprintf(e.out, "> %s%s\n", fromTag(t.from), clip(t.asked, askLen))
		}
		switch {
		case t.reply != "":
			fmt.Fprintf(e.out, "  %s\n", clip(t.reply, replyLen))
		case i == len(ts)-1 && busy(st.Status):
			fmt.Fprintln(e.out, "  （进行中）")
		case i == len(ts)-1 && st.Status == api.StatusWaiting:
			fmt.Fprintln(e.out, "  （需要你）")
		default:
			fmt.Fprintln(e.out, "  （没有回复）")
		}
		if i < len(ts)-1 {
			fmt.Fprintln(e.out)
		}
	}
	return exitDone
}

// What ir read shows by default: how many messages, how much of each.
const (
	readCount = 10
	askLen    = 80
	replyLen  = 300
)

// turn is one message of yours and the agent's last words after it, before the next one.
type turn struct{ asked, from, reply string }

// turns are the page's messages with their final replies; words before the first message (a
// page that starts in a turn) make one with nothing asked.
func turns(blocks []api.Block) []turn {
	var out []turn
	for _, b := range blocks {
		switch b.Kind {
		case api.BlockKindUser:
			out = append(out, turn{asked: b.Text, from: b.From})
		case api.BlockKindAssistant:
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			if len(out) == 0 {
				out = append(out, turn{})
			}
			out[len(out)-1].reply = b.Text
		}
	}
	return out
}

func fromTag(from string) string {
	if from == "" {
		return ""
	}
	return "[" + from + "] "
}

// clip is text on one line, cut to n runes.
func clip(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

// printPage is ir read --full: the page as written.
func (e *env) printPage(page api.MessagePage) {
	for _, b := range page.Blocks {
		switch b.Kind {
		case api.BlockKindUser:
			fmt.Fprintf(e.out, "> %s%s\n\n", fromTag(b.From), strings.ReplaceAll(strings.TrimRight(b.Text, "\n"), "\n", "\n> "))
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
}

// subQuiet: how long a subagent may write nothing and still count as at work (the page's
// SUB_QUIET): one its parent dropped never writes its final answer.
const subQuiet = 5 * time.Minute

// atWork: a subagent not done, written lately, whose parent's process is alive (not its turn:
// background agents and workflows work on after it); as the sidebar shows them (user
// 2026-11-06: a subagent is seen only while it works).
func atWork(s api.Session, byID map[string]api.Session, now time.Time) bool {
	p, ok := byID[s.Parent]
	return !s.Done && ok && p.Live && p.Status != api.StatusError && now.Sub(time.UnixMilli(s.UpdatedAt)) < subQuiet
}

// listed is what ir ls and find show: without all, no archive and no subagent that is not at work.
func (e *env) listed(all bool) ([]api.Session, []string, error) {
	var list []api.Session
	if err := e.do("GET", "/api/sessions", nil, &list); err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(list))
	byID := make(map[string]api.Session, len(list))
	for i, s := range list {
		ids[i] = s.ID
		byID[s.ID] = s
	}
	if !all {
		now := time.Now()
		list = slices.DeleteFunc(list, func(s api.Session) bool { return s.Archived || (s.Subagent && !atWork(s, byID, now)) })
	}
	return list, ids, nil
}

// line is a session's line in ir ls and find: id, agent, status, directory, name or title (a
// subagent's marked, an archived one too).
func line(s api.Session, ids []string, home string) string {
	cwd := s.Cwd
	if home != "" && (cwd == home || strings.HasPrefix(cwd, home+"/")) {
		cwd = "~" + strings.TrimPrefix(cwd, home)
	}
	title := s.Name
	if title == "" {
		title = s.Title
	}
	if s.Subagent && s.Parent != "" {
		title = "[子代理 ← " + short(s.Parent, ids) + "] " + title
	} else if s.Subagent {
		title = "[子代理] " + title
	}
	if s.Archived {
		title = "[归档] " + title
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s", short(s.ID, ids), s.Agent, s.Status, cwd, title)
}

// ir ls [--all]: one session a line; the archive and the subagents done only with --all.
func cmdLs(e *env, args []string) int {
	o, err := parse(args, "all")
	if err != nil || len(o.pos) != 0 {
		return e.failf("usage: ir ls [--all]")
	}
	list, ids, err := e.listed(o.all)
	if err != nil {
		return e.fail(err)
	}
	home, _ := os.UserHomeDir()
	for _, s := range underParents(list) {
		fmt.Fprintln(e.out, line(s, ids, home))
	}
	return exitDone
}

// underParents is list with each subagent moved right after the session that started it, as
// the sidebar puts it; one whose parent is not in list stays where it was.
func underParents(list []api.Session) []api.Session {
	in := make(map[string]bool, len(list))
	for _, s := range list {
		if !s.Subagent {
			in[s.ID] = true
		}
	}
	kids := map[string][]api.Session{}
	for _, s := range list {
		if s.Subagent && in[s.Parent] {
			kids[s.Parent] = append(kids[s.Parent], s)
		}
	}
	out := make([]api.Session, 0, len(list))
	for _, s := range list {
		if s.Subagent && in[s.Parent] {
			continue
		}
		out = append(out, s)
		out = append(out, kids[s.ID]...)
	}
	return out
}

// ir find <words>... [--all]: the sessions whose id, name, title, directory or last words have
// every word, case aside; each line as ir ls, then the last words. The archive is searched too
// (it is where a session you look for often is); subagents done only with --all.
func cmdFind(e *env, args []string) int {
	o, err := parse(args, "all")
	if err != nil || len(o.pos) == 0 {
		return e.failf("usage: ir find <词>... [--all]")
	}
	var words []string
	for _, p := range o.pos {
		words = append(words, strings.Fields(strings.ToLower(p))...)
	}
	all, ids, err := e.listed(true)
	if err != nil {
		return e.fail(err)
	}
	shown, _, err := e.listed(o.all)
	if err != nil {
		return e.fail(err)
	}
	keep := make(map[string]bool, len(shown))
	for _, s := range shown {
		keep[s.ID] = true
	}
	home, _ := os.UserHomeDir()
	found := 0
	for _, s := range all {
		if !keep[s.ID] && !(s.Archived && !s.Subagent) {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{s.ID, s.Name, s.Title, s.Cwd, lastText(s)}, "\n"))
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			found++
			fmt.Fprintln(e.out, line(s, ids, home))
			if t := lastText(s); t != "" {
				fmt.Fprintf(e.out, "\t%s\n", clip(t, askLen))
			}
		}
	}
	if found == 0 {
		return e.failf("没找到：%s", strings.Join(o.pos, " "))
	}
	return exitDone
}

func lastText(s api.Session) string {
	if s.Last == nil {
		return ""
	}
	return s.Last.Text
}

// ir archive | unarchive <id>: as the sidebar's menu does; an archived session is listed only by
// ls --all (and find).
func cmdArchive(e *env, args []string, archived bool) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 1 {
		if archived {
			return e.failf("usage: ir archive <id>")
		}
		return e.failf("usage: ir unarchive <id>")
	}
	id, err := e.resolve(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	if err := e.do("PATCH", "/api/sessions/"+id, api.SessionPatch{Archived: &archived}, nil); err != nil {
		return e.fail(err)
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

// turnStatus is a session's status as a turn has it: waiting only on a step auto mode denied (its
// card above the page's composer, no ask open), the turn is over: idle.
func turnStatus(st *api.SessionState) api.Status {
	if st.Status == api.StatusWaiting && len(st.Asks) == 0 && len(st.Denials) > 0 {
		return api.StatusIdle
	}
	return st.Status
}

// ir bot "message" [--to bot]: the session it runs in tells the user through a bot. A chat that
// points at the session gets it as its reply; else a letter, 进入会话 or 关闭 (docs/qq.md).
func cmdBot(e *env, args []string) int {
	o, err := parse(args, "to")
	if err != nil {
		return e.fail(err)
	}
	if len(o.pos) != 1 || strings.TrimSpace(o.pos[0]) == "" {
		return e.failf(`usage: ir bot "消息" [--to bot]`)
	}
	from := caller()
	if from == "" {
		return e.failf("ir bot 在会话里运行（没有 IRIDIUM_SESSION）")
	}
	var r api.BotReply
	if err := e.do("POST", "/api/bot", api.BotLetter{Text: o.pos[0], To: o.to, Caller: from}, &r); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.out, "已发到 %s\n", cmp.Or(r.Name, r.Bot))
	return exitDone
}
