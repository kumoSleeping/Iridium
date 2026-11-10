package satori

import (
	"cmp"
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
)

// Guilds: one per folder group of the web (api.Workspace, id its number), and Loose for the
// sessions in none. Channels: the sessions, newest activity first; archived ones and subagents are
// left out of channel.list (channel.get still gives them).

// Loose is the guild of the sessions in no folder group.
const (
	Loose     = "loose"
	LooseName = "未分组"
	pageSize  = 100
)

func agentUser(a api.Agent) (User, bool) {
	info, ok := agents.Get(a)
	if !ok {
		return User{}, false
	}
	return User{ID: string(a), Name: info.Name, IsBot: true}, true
}

// channelOf is session ss as a channel.
func channelOf(ss api.Session) *Channel {
	name := cmp.Or(ss.Name, ss.Title)
	if name == "" && ss.Cwd != "" {
		name = filepath.Base(ss.Cwd)
	}
	return &Channel{ID: ss.ID, Type: ChannelText, Name: name, ParentID: ss.Parent}
}

// guildOf is the guild session ss is in.
func (s *Service) guildOf(ss api.Session) *Guild {
	if ss.Workspace == 0 {
		return &Guild{ID: Loose, Name: LooseName}
	}
	g := &Guild{ID: strconv.FormatInt(ss.Workspace, 10)}
	if ws, err := s.b.Workspaces(); err == nil {
		if i := slices.IndexFunc(ws, func(w api.Workspace) bool { return w.ID == ss.Workspace }); i >= 0 {
			g.Name = ws[i].Name
		}
	}
	return g
}

// channel is session id, if c sees it.
func (s *Service) channel(c Caller, id string) (*api.SessionState, error) {
	if id == "" {
		return nil, bad("channel_id")
	}
	if !s.b.Sees(c, id) {
		return nil, errNoSession
	}
	st, err := s.b.Session(id)
	if err != nil {
		if status, _ := s.b.Fault(err); status == http.StatusNotFound || status == http.StatusUnprocessableEntity {
			return nil, errNoSession
		}
		return nil, err
	}
	return st, nil
}

func (s *Service) channelGet(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	return channelOf(st.Session), nil
}

// visible are the sessions c sees, newest first, archived and subagents left out.
func (s *Service) visible(ctx context.Context, c Caller) ([]api.Session, error) {
	l, err := s.b.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := l[:0]
	for _, ss := range l {
		if !ss.Archived && !ss.Subagent && s.b.Sees(c, ss.ID) {
			out = append(out, ss)
		}
	}
	slices.SortStableFunc(out, func(a, b api.Session) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	return out, nil
}

// page cuts l at token next (a decimal offset): the page and the next token.
func page[T any](l []T, next string) ([]T, string, error) {
	at := 0
	if next != "" {
		n, err := strconv.Atoi(next)
		if err != nil || n < 0 {
			return nil, "", bad("next")
		}
		at = min(n, len(l))
	}
	end := min(at+pageSize, len(l))
	tok := ""
	if end < len(l) {
		tok = strconv.Itoa(end)
	}
	return l[at:end], tok, nil
}

func (s *Service) channelList(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		GuildID string `json:"guild_id"`
		Next    string `json:"next"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	l, err := s.visible(ctx, c)
	if err != nil {
		return nil, err
	}
	var chans []Channel
	for _, ss := range l {
		if req.GuildID == "" || s.guildOf(ss).ID == req.GuildID {
			chans = append(chans, *channelOf(ss))
		}
	}
	if req.GuildID != "" && len(chans) == 0 && !s.guildThere(c, req.GuildID) {
		return nil, refuse(http.StatusUnprocessableEntity, "no_such_guild", "no guild "+req.GuildID)
	}
	p, next, err := page(chans, req.Next)
	if err != nil {
		return nil, err
	}
	return List[Channel]{Data: nonNil(p), Next: next}, nil
}

func nonNil[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}

// guilds are the folder groups c sees, then Loose.
func (s *Service) guilds(c Caller) []Guild {
	var out []Guild
	if ws, err := s.b.Workspaces(); err == nil {
		for _, w := range ws {
			if s.b.SeesGuild(c, w) {
				out = append(out, Guild{ID: strconv.FormatInt(w.ID, 10), Name: w.Name})
			}
		}
	}
	return append(out, Guild{ID: Loose, Name: LooseName})
}

func (s *Service) guildThere(c Caller, id string) bool {
	return slices.ContainsFunc(s.guilds(c), func(g Guild) bool { return g.ID == id })
}

func (s *Service) guildList(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		Next string `json:"next"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	p, next, err := page(s.guilds(c), req.Next)
	if err != nil {
		return nil, err
	}
	return List[Guild]{Data: nonNil(p), Next: next}, nil
}

func (s *Service) guildGet(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		GuildID string `json:"guild_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	for _, g := range s.guilds(c) {
		if g.ID == req.GuildID {
			return g, nil
		}
	}
	return nil, refuse(http.StatusUnprocessableEntity, "no_such_guild", "no guild "+req.GuildID)
}
