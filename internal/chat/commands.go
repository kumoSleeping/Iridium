package chat

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Iridium's commands (plan "插件"): the plugin does not make its own. Each answers in the input's
// reply, with text or a card to pick from.

type cmdFunc func(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error)

type cmd struct {
	desc string
	run  cmdFunc
}

var commands map[string]cmd

func init() {
	commands = map[string]cmd{
		"new":     {"开新会话", cmdNew},
		"clear":   {"清空对话：同一处开新会话，旧的归档", cmdClear},
		"use":     {"换会话", cmdUse},
		"history": {"最近几轮对话", cmdHistory},
		"ls":      {"列出会话", cmdLs},
		"stop":    {"停掉当前回合", cmdStop},
		"model":   {"换模型", cmdModel},
		"mode":    {"换审批模式", cmdMode},
		"status":  {"会话状态", cmdStatus},
		"usage":   {"额度", cmdUsage},
		"context": {"上下文用了多少", cmdContext},
	}
}

// common describes the agent commands every session has (session.Manager.Commands), which come
// without a description.
var common = map[string]string{"compact": "压缩上下文", "effort": "思考强度"}

// cmdClear starts the chat's session over: a new one in its place (session.Manager.Clear).
func cmdClear(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	if p.Session == "" {
		return reply("", "还没有会话")
	}
	if err := s.guard().Use(c, p.Session); err != nil {
		return api.ChatInputReply{}, err
	}
	id, err := s.o.Manager.Clear(ctx, p.Session, source(c, ""))
	if id != "" && id != p.Session {
		s.guard().Opened(c, id)
		p.Session = id
		if err := s.setPointer(*p); err != nil {
			return api.ChatInputReply{}, err
		}
	}
	if err != nil {
		return api.ChatInputReply{}, err
	}
	return reply(id, fmt.Sprintf("已清空，新会话 `%s`", short(id)))
}

// Order is the commands' order in a menu.
var Order = []string{"new", "clear", "use", "ls", "history", "stop", "model", "mode", "status", "usage", "context"}

// Commands is what the chat can send: Iridium's, then its session's agent's own.
func (s *Service) Commands(c Caller, chat string) ([]api.ChatCommand, error) {
	out := []api.ChatCommand{}
	for _, n := range Order {
		out = append(out, api.ChatCommand{Name: n, Description: commands[n].desc, Iridium: true})
	}
	p, err := s.o.Store.Pointer(c.ID, chat)
	if err != nil || p.Session == "" {
		return out, err
	}
	agentCmds, err := s.o.Manager.Commands(p.Session)
	if err != nil {
		return out, nil
	}
	for _, a := range agentCmds {
		if _, ours := commands[a.Name]; !ours {
			out = append(out, api.ChatCommand{Name: a.Name, Description: cmp.Or(a.Description, common[a.Name], a.Name)})
		}
	}
	return out, nil
}

func text(md string) *api.ChatReply { return &api.ChatReply{Markdown: md, Text: plain(md)} }

func reply(id, md string) (api.ChatInputReply, error) {
	return api.ChatInputReply{Session: id, Reply: text(md)}, nil
}

var agents = []api.Agent{api.AgentClaude, api.AgentCodex, api.AgentPi, api.AgentDsh}

func cmdNew(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	if args == "" { // a folder, then an agent: as the web's +
		return s.folderCard(ctx, c, p, true)
	}
	a := api.Agent(p.Agent)
	f := strings.Fields(args)
	if len(f) > 0 && slices.Contains(agents, api.Agent(strings.ToLower(f[0]))) {
		a, f = api.Agent(strings.ToLower(f[0])), f[1:]
	}
	cwd := cmp.Or(p.Cwd, s.o.ChatDir)
	if len(f) > 0 {
		dir := strings.Join(f, " ")
		if rest, ok := strings.CutPrefix(dir, "~"); ok {
			home, _ := os.UserHomeDir()
			dir = home + rest
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return api.ChatInputReply{}, fmt.Errorf("%w: no folder %s", ErrBadRequest, dir)
		}
		cwd = filepath.Clean(dir)
	}
	id, err := s.open(ctx, c, p, a, cwd, source(c, ""))
	if err != nil {
		return api.ChatInputReply{}, err
	}
	return reply(id, fmt.Sprintf("新会话 %s  \n%s", tag(a), tilde(cwd)))
}

// sessions are the ones a chat can use: not archived, the most recent first, ten.
func (s *Service) sessions(ctx context.Context, c Caller) ([]api.Session, error) {
	l, err := s.o.Manager.List(ctx)
	if err != nil {
		return nil, err
	}
	l = slices.DeleteFunc(s.guard().Sessions(c, l), func(x api.Session) bool { return x.Archived })
	return l[:min(len(l), 10)], nil
}

func short(id string) string { return id[:min(len(id), 8)] }

func cmdUse(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	l, err := s.sessions(ctx, c)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if args == "" {
		return s.folderCard(ctx, c, p, false)
	}
	if r, ok, err := s.folderStep(ctx, c, p, args); ok {
		return r, err
	}
	var chosen *api.Session
	if n, err := strconv.Atoi(args); err == nil && n >= 1 && n <= len(l) {
		chosen = &l[n-1]
	} else {
		all, err := s.o.Manager.List(ctx)
		if err != nil {
			return api.ChatInputReply{}, err
		}
		all = s.guard().Sessions(c, all)
		for i := range all {
			if strings.HasPrefix(all[i].ID, args) {
				if chosen != nil {
					return api.ChatInputReply{}, fmt.Errorf("%w: %q names more than one session", ErrBadRequest, args)
				}
				chosen = &all[i]
			}
		}
	}
	if chosen == nil {
		return api.ChatInputReply{}, fmt.Errorf("%w: no session %q", ErrNotFound, args)
	}
	p.Plugin, p.Session = c.ID, chosen.ID
	if err := s.setPointer(*p); err != nil {
		return api.ChatInputReply{}, err
	}
	msg := "现在：" + sessionText(*chosen) + "  \n" + tilde(chosen.Cwd)
	// The last rounds, as one message (QQ answers a message four times only).
	if h := s.history(chosen.ID, switchRounds, switchChars); h != "" {
		msg += "\n\n" + h
	}
	return reply(chosen.ID, msg)
}

func cmdLs(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	l, err := s.folders(ctx, c)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if len(l) == 0 {
		return reply(p.Session, "还没有会话，发 /new 开一个。")
	}
	// The groups as a tree, as the web lists them (the user, 2026-11-08); at most maxLs sessions.
	type node struct {
		text string
		kids []node
	}
	n := 0
	var walk func(f *folder) []node
	walk = func(f *folder) []node {
		var out []node
		for _, x := range f.sessions {
			if n == maxLs {
				break
			}
			n++
			t := sessionText(x)
			if x.ID == p.Session {
				t = "**" + t + "**"
			}
			d := ago(x.UpdatedAt)
			if x.Status == api.StatusRunning {
				d = "进行中"
			}
			out = append(out, node{text: t + " " + d})
		}
		for _, g := range f.subs {
			if kids := walk(g); len(kids) > 0 {
				out = append(out, node{text: g.name + "/", kids: kids})
			}
		}
		return out
	}
	var roots []node
	for _, g := range l {
		if kids := walk(g); len(kids) > 0 {
			roots = append(roots, node{text: g.name, kids: kids})
		}
	}
	lines := []string{"**会话**"}
	var draw func(ns []node, indent string)
	draw = func(ns []node, indent string) {
		for i, x := range ns {
			last := i == len(ns)-1
			branch, more := "├── ", "│\u00a0\u00a0\u00a0"
			if last {
				branch, more = "└── ", "\u00a0\u00a0\u00a0\u00a0"
			}
			lines = append(lines, indent+branch+x.text)
			draw(x.kids, indent+more)
		}
	}
	draw(roots, "")
	// Each line break a hard one (QQ's Markdown: two spaces before it).
	return reply(p.Session, strings.Join(lines, "  \n"))
}

const maxLs = 30

// current is the session the chat points at, or an error saying there is none.
func (s *Service) current(c Caller, p *store.ChatPointer) (*api.SessionState, error) {
	if p.Session == "" {
		return nil, fmt.Errorf("%w: this chat has no session yet; send a message or /new", ErrNotFound)
	}
	st, err := s.o.Manager.State(p.Session)
	if err != nil {
		return nil, fmt.Errorf("%w: its session is gone; /use or /new", ErrNotFound)
	}
	if err := s.guard().Use(c, st.ID); err != nil {
		return nil, err
	}
	return st, nil
}

func cmdStop(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if st.Status == api.StatusIdle && st.QuotaUntil == 0 {
		return reply(st.ID, "没有在跑的回合。")
	}
	if err := s.o.Manager.Interrupt(ctx, st.ID); err != nil && err != session.ErrNotLive {
		return api.ChatInputReply{}, err
	}
	return reply(st.ID, "停了。")
}

func cmdModel(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if args == "" {
		if len(st.Models) == 0 {
			return reply(st.ID, "现在：`"+cmp.Or(st.Model, "默认")+"`。agent 跑起来才知道有哪些模型。")
		}
		card := &api.Card{Session: st.ID, Title: "换模型", Summary: []string{"现在：" + cmp.Or(st.Model, "默认")}}
		for _, m := range st.Models {
			card.Options = append(card.Options, api.CardOption{ID: m.Value, Text: cmp.Or(m.Name, m.Value), Description: m.Description, Primary: m.Value == st.Model})
		}
		return api.ChatInputReply{Session: st.ID, Card: s.newPick(c, p.Chat, "model", card)}, nil
	}
	model := args
	if model == "default" {
		model = ""
	}
	if err := s.o.Manager.SetModel(ctx, st.ID, model); err != nil {
		return api.ChatInputReply{}, err
	}
	return reply(st.ID, "模型：`"+args+"`")
}

func cmdMode(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if args == "" {
		card := &api.Card{Session: st.ID, Title: "审批模式", Summary: []string{"现在：" + cmp.Or(st.Mode, "默认")}}
		for _, m := range st.Modes {
			card.Options = append(card.Options, api.CardOption{ID: m, Text: m, Primary: m == st.Mode})
		}
		return api.ChatInputReply{Session: st.ID, Card: s.newPick(c, p.Chat, "mode", card)}, nil
	}
	if err := s.guard().Mode(c, args); err != nil {
		return api.ChatInputReply{}, err
	}
	if err := s.o.Manager.SetMode(ctx, st.ID, args); err != nil {
		return api.ChatInputReply{}, err
	}
	return reply(st.ID, "审批模式：`"+args+"`")
}

func cmdStatus(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n", cmp.Or(st.Name, st.Title, filepath.Base(st.Cwd)))
	fmt.Fprintf(&b, "- 会话：%s · `%s`\n", st.Agent, short(st.ID))
	fmt.Fprintf(&b, "- 路径：%s\n", tilde(st.Cwd))
	fmt.Fprintf(&b, "- 模型：%s", cmp.Or(modelName(st), "默认"))
	if e := cmp.Or(st.Effort, st.EffortNow); e != "" {
		fmt.Fprintf(&b, " · %s", e)
	}
	if st.Ultracode != nil && st.Ultracode.On {
		b.WriteString(" · UltraCode")
	}
	if st.Context != nil && st.Context.Max > 0 {
		fmt.Fprintf(&b, "\n- 上下文：%d%%", st.Context.Used*100/st.Context.Max)
	}
	if w := statusWord(st.Status); w != "" {
		fmt.Fprintf(&b, "\n- 状态：%s", w)
	}
	if st.QuotaUntil != 0 {
		fmt.Fprintf(&b, "\n- 等额度：%s 恢复", time.UnixMilli(st.QuotaUntil).Format("01-02 15:04"))
	}
	if n := len(st.Queue); n > 0 {
		fmt.Fprintf(&b, "\n- 待发：%d 条", n)
	}
	if st.Error != "" {
		fmt.Fprintf(&b, "\n- 出错：%s", st.Error)
	}
	return reply(st.ID, b.String())
}

// statusWord is a status as /status says it; idle says nothing.
func statusWord(s api.Status) string {
	return map[api.Status]string{
		api.StatusStarting:   "启动中",
		api.StatusRunning:    "进行中",
		api.StatusCompacting: "压缩中",
		api.StatusWaiting:    "等你确认",
		api.StatusError:      "出错",
		api.StatusElsewhere:  "在别处运行",
	}[s]
}

func contextLine(used, max int) string {
	if max == 0 {
		return fmt.Sprintf("%d tokens", used)
	}
	return fmt.Sprintf("%d / %d tokens（%d%%）", used, max, used*100/max)
}

func cmdUsage(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	q, err := s.o.Manager.QuotaOf(st.ID)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if q == nil || len(q.Windows) == 0 {
		return reply(st.ID, "还没读到 "+string(st.Agent)+" 的额度。")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s 额度**", q.Agent)
	if q.Plan != "" {
		fmt.Fprintf(&b, "（%s）", q.Plan)
	}
	for _, w := range q.Windows {
		name := map[string]string{"session": "5 小时", "weekly": "7 天"}[w.Group]
		name = cmp.Or(name, w.Group)
		if w.Model != "" {
			name += " " + w.Model
		}
		fmt.Fprintf(&b, "\n- %s：%.0f%%", name, w.Percent)
		if w.ResetsAt != 0 {
			fmt.Fprintf(&b, "，%s 重置", time.UnixMilli(w.ResetsAt).Format("01-02 15:04"))
		}
	}
	if !q.Live {
		fmt.Fprintf(&b, "\n\n（%s 读的）", time.UnixMilli(q.At).Format("01-02 15:04"))
	}
	return reply(st.ID, b.String())
}

func cmdContext(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	cx, err := s.o.Manager.Context(st.ID)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if cx == nil {
		return reply(st.ID, "还没读到上下文。")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**上下文**：%s", contextLine(cx.Used, cx.Max))
	for _, k := range cx.Categories {
		fmt.Fprintf(&b, "\n- %s：%d", k.Name, k.Tokens)
	}
	return reply(st.ID, b.String())
}

// modelName is the model in use by its name in the agent's list (by value, else by the full id a
// value resolves to, an alias over the default entry), else as the agent named it.
func modelName(st *api.SessionState) string {
	for _, m := range st.Models {
		if m.Value == st.Model {
			return m.Name
		}
	}
	name := ""
	for _, m := range st.Models {
		if m.Resolved != "" && m.Resolved == st.Model && (name == "" || m.Value != "default") {
			name = m.Name
		}
	}
	return cmp.Or(name, st.Model)
}
