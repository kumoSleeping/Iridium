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

	agentreg "github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Choosing a session as the web's session list does (plan "插件", the user 2026-11-08): a group
// first, then what is in it, its sessions at its root and its folders, as the web's tree (web/src/
// tree.ts); a folder picked, its sessions. Each list ends with a new session there, its agent
// picked last. Each step is a pick card; its options are /use arguments:
//
//	g:<folder>          what is in the group or folder
//	n:<folder>          the agents to open a session with there
//	n:<folder>:<agent>  open it
//
// A group is w<group ID>, w0 for 未归类; a folder in it <group>/<path under the group's root>; or
// d<path>: a directory picked for a first session.

// folder is a group, or a folder in one, as the chat shows it.
type folder struct {
	key, name, path string        // path: "" where no session opens (未归类's root)
	sessions        []api.Session // right here, the most recent first
	subs            []*folder     // a group's folders, the latest first
	at              int64
}

func under(p, root string) bool {
	return root != "" && (p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/"))
}

// commonDir is the deepest directory all paths are in, a lone one's parent (tree.ts's).
func commonDir(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	split := make([][]string, len(paths))
	for i, p := range paths {
		split[i] = strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	}
	first, n := split[0], 0
	for n < len(first) && !slices.ContainsFunc(split, func(p []string) bool { return n >= len(p) || p[n] != first[n] }) {
		n++
	}
	if n > 0 && !slices.ContainsFunc(paths, func(p string) bool { return p != paths[0] }) {
		n--
	}
	if n == 0 {
		return ""
	}
	return "/" + strings.Join(first[:n], "/")
}

// folders are the chat's groups with what is in them (not archived, not subagents: they take no
// messages), the most recently active first.
func (s *Service) folders(ctx context.Context, c Caller) ([]*folder, error) {
	l, err := s.o.Manager.List(ctx)
	if err != nil {
		return nil, err
	}
	l = slices.DeleteFunc(s.guard().Sessions(c, l), func(x api.Session) bool { return x.Archived || x.Subagent })
	slices.SortStableFunc(l, func(a, b api.Session) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	groups := map[int64]api.Workspace{}
	if s.o.Workspaces != nil {
		if ws, err := s.o.Workspaces(); err == nil {
			for _, w := range ws {
				groups[w.ID] = w
			}
		}
	}
	byWs := map[int64][]api.Session{}
	var order []int64
	for _, x := range l {
		id := x.Workspace
		if _, ok := groups[id]; !ok {
			id = 0
		}
		if byWs[id] == nil {
			order = append(order, id)
		}
		byWs[id] = append(byWs[id], x)
	}
	var out []*folder
	for _, id := range order {
		in := byWs[id]
		g := &folder{key: "w" + strconv.FormatInt(id, 10), at: in[0].UpdatedAt}
		root := ""
		if w, ok := groups[id]; ok && id != 0 {
			root = w.Path
			g.name, g.path = cmp.Or(w.Name, filepath.Base(w.Path)), w.Path
		} else {
			var cwds []string
			for _, x := range in {
				cwds = append(cwds, x.Cwd)
			}
			root, g.name = commonDir(cwds), "未归类"
		}
		subs := map[string]*folder{}
		for _, x := range in {
			r, name := "", tilde(x.Cwd)
			if under(x.Cwd, root) {
				r, _ = filepath.Rel(root, x.Cwd)
				r, name = strings.TrimPrefix(r, "."), r
			} else {
				r = x.Cwd
			}
			if r == "" {
				g.sessions = append(g.sessions, x)
				continue
			}
			f := subs[r]
			if f == nil {
				f = &folder{key: g.key + "/" + r, name: name, path: x.Cwd, at: x.UpdatedAt}
				subs[r] = f
				g.subs = append(g.subs, f)
			}
			f.sessions = append(f.sessions, x)
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *Service) findFolder(ctx context.Context, c Caller, key string) (*folder, error) {
	l, err := s.folders(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, g := range l {
		if g.key == key {
			return g, nil
		}
		for _, f := range g.subs {
			if f.key == key {
				return f, nil
			}
		}
	}
	// A directory with no sessions in it (one picked for a first session): by its path.
	if p, ok := strings.CutPrefix(key, "d"); ok && filepath.IsAbs(p) {
		return &folder{key: key, name: filepath.Base(p), path: p}, nil
	}
	return nil, fmt.Errorf("%w: no such folder", ErrNotFound)
}

// tilde writes a path under home as ~/….
func tilde(p string) string {
	if home, _ := os.UserHomeDir(); home != "" {
		if p == home {
			return "~"
		}
		if r, ok := strings.CutPrefix(p, home+"/"); ok {
			return "~/" + r
		}
	}
	return p
}

// ago is how long ago ms was, short.
func ago(ms int64) string {
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天前", int(d.Hours()/24))
}

// tag is an agent's mark in a chat's lists (the user, 2026-11-08): [CC] 打招呼.
func tag(a api.Agent) string {
	switch a {
	case api.AgentClaude:
		return "[CC]"
	case api.AgentCodex:
		return "[GPT]"
	}
	return "[" + strings.ToUpper(string(cmp.Or(a, api.AgentClaude))) + "]"
}

// sessionText is a session as a chat lists it: its agent's tag and its name, cut to a line.
func sessionText(x api.Session) string {
	name := strings.Join(strings.Fields(cmp.Or(x.Name, x.Title, "未命名")), " ")
	if r := []rune(name); len(r) > 24 {
		name = string(r[:23]) + "…"
	}
	return tag(x.Agent) + " " + name
}

const (
	maxOptions = 15
	newText    = "[NEW] 新会话"
)

// folderCard: pick a group. forNew: then an agent, to open a session at its root (groups with a
// root only); else what is in it.
func (s *Service) folderCard(ctx context.Context, c Caller, p *store.ChatPointer, forNew bool) (api.ChatInputReply, error) {
	l, err := s.folders(ctx, c)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	card := &api.Card{Title: "文件夹", Summary: []string{}}
	if forNew {
		card.Title = "新会话"
	}
	for _, g := range l {
		if forNew && g.path == "" {
			continue
		}
		if len(card.Options) == maxOptions {
			break
		}
		o := api.CardOption{ID: "g:" + g.key, Text: g.name, Primary: g.holds(p.Session)}
		if forNew {
			o.ID = "n:" + g.key
		}
		card.Options = append(card.Options, o)
	}
	// In a session: leaving it is the list's last (the user, 2026-11-08).
	if _, in := s.pointed(c, p); in && !forNew {
		card.Options = append(card.Options, api.CardOption{ID: leaveOption, Text: "离开当前会话"})
	}
	if len(card.Options) == 0 {
		dir := cmp.Or(p.Cwd, s.o.ChatDir)
		if dir == "" {
			dir, _ = os.UserHomeDir()
		}
		if dir == "" {
			return reply(p.Session, "还没有会话：发 /new [agent] [目录] 开一个。")
		}
		// No group yet: the chat's own directory, to open the first session in.
		card.Options = append(card.Options, api.CardOption{ID: "n:d" + dir, Text: filepath.Base(dir), Description: tilde(dir)})
	}
	return api.ChatInputReply{Session: p.Session, Card: s.newPick(c, p.Chat, "use", card)}, nil
}

// holds: the session is in the group, or in one of its folders.
func (f *folder) holds(id string) bool {
	if id == "" {
		return false
	}
	if slices.ContainsFunc(f.sessions, func(x api.Session) bool { return x.ID == id }) {
		return true
	}
	return slices.ContainsFunc(f.subs, func(g *folder) bool { return g.holds(id) })
}

// sessionCard: what is in a group or folder: its sessions, then its folders, then a new session
// there.
func (s *Service) sessionCard(ctx context.Context, c Caller, p *store.ChatPointer, key string) (api.ChatInputReply, error) {
	f, err := s.findFolder(ctx, c, key)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	// Nothing to pick but one folder (未归类 of one directory): that folder's.
	if len(f.sessions) == 0 && len(f.subs) == 1 && f.path == "" {
		f = f.subs[0]
	}
	card := &api.Card{Title: f.name, Summary: []string{}}
	if f.path != "" {
		card.Summary = append(card.Summary, tilde(f.path))
	}
	room := maxOptions - 1 - len(f.subs)
	for _, x := range f.sessions {
		if len(card.Options) >= max(room, 3) {
			break
		}
		desc := ago(x.UpdatedAt)
		if x.Status == api.StatusRunning {
			desc = "进行中"
		}
		card.Options = append(card.Options, api.CardOption{ID: x.ID, Text: sessionText(x), Description: desc, Primary: x.ID == p.Session})
	}
	for _, g := range f.subs {
		if len(card.Options) == maxOptions-1 {
			break
		}
		card.Options = append(card.Options, api.CardOption{ID: "g:" + g.key, Text: g.name + "/", Primary: g.holds(p.Session)})
	}
	if f.path != "" {
		card.Options = append(card.Options, api.CardOption{ID: "n:" + f.key, Text: newText})
	}
	if len(card.Options) == 0 {
		return reply(p.Session, "这个文件夹没有会话。")
	}
	return api.ChatInputReply{Session: p.Session, Card: s.newPick(c, p.Chat, "use", card)}, nil
}

// agentCard: pick the agent of a new session in the folder.
func (s *Service) agentCard(ctx context.Context, c Caller, p *store.ChatPointer, key string) (api.ChatInputReply, error) {
	f, err := s.findFolder(ctx, c, key)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if f.path == "" {
		return api.ChatInputReply{}, fmt.Errorf("%w: the folder has no path", ErrBadRequest)
	}
	card := &api.Card{Title: "新会话 " + f.name, Summary: []string{tilde(f.path)}}
	for _, a := range agentreg.Describe() {
		if s.o.Manager.Missing(a.Agent) {
			continue
		}
		card.Options = append(card.Options, api.CardOption{ID: "n:" + f.key + ":" + string(a.Agent), Text: tag(a.Agent) + " " + a.Name, Primary: string(a.Agent) == p.Agent})
	}
	if len(card.Options) == 0 {
		return reply(p.Session, "这台机器上没有可用的 agent。")
	}
	return api.ChatInputReply{Session: p.Session, Card: s.newPick(c, p.Chat, "use", card)}, nil
}

// openIn opens a session of agent a in the folder and points the chat at it.
func (s *Service) openIn(ctx context.Context, c Caller, p *store.ChatPointer, key string, a api.Agent) (api.ChatInputReply, error) {
	f, err := s.findFolder(ctx, c, key)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if f.path == "" {
		return api.ChatInputReply{}, fmt.Errorf("%w: the folder has no path", ErrBadRequest)
	}
	id, err := s.open(ctx, c, p, a, f.path, source(c, ""))
	if err != nil {
		return api.ChatInputReply{}, err
	}
	return reply(id, fmt.Sprintf("新会话 %s  \n%s", tag(a), tilde(f.path)))
}

// folderStep runs a /use argument of the folder steps; ok false if args is none.
func (s *Service) folderStep(ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, bool, error) {
	if key, ok := strings.CutPrefix(args, "g:"); ok {
		r, err := s.sessionCard(ctx, c, p, key)
		return r, true, err
	}
	rest, ok := strings.CutPrefix(args, "n:")
	if !ok {
		return api.ChatInputReply{}, false, nil
	}
	// n:<folder>[:<agent>]; a folder may hold colons, the agent is the last part.
	if i := strings.LastIndex(rest, ":"); i > 0 {
		a := api.Agent(rest[i+1:])
		if slices.Contains(agentIDs(), a) {
			r, err := s.openIn(ctx, c, p, rest[:i], a)
			return r, true, err
		}
	}
	r, err := s.agentCard(ctx, c, p, rest)
	return r, true, err
}

func agentIDs() []api.Agent {
	var out []api.Agent
	for _, a := range agentreg.Describe() {
		out = append(out, a.Agent)
	}
	return out
}

// leaveOption is the folder card's 离开当前会话, a /use argument.
const leaveOption = ":leave"

// cmdLeave points the chat at no session (the user, 2026-11-08): the web marks none with its bot,
// and what the chat says next goes nowhere till a session is chosen.
func cmdLeave(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	if p.Session == "" {
		return reply("", "当前不在任何会话。")
	}
	p.Plugin, p.Session = c.ID, ""
	if err := s.setPointer(*p); err != nil {
		return api.ChatInputReply{}, err
	}
	return reply("", "已离开会话。")
}
