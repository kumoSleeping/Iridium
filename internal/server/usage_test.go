package server

import (
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// GET /api/usage: by day, terminal, agent and model; everyone's for 本机 and panels, a channel's
// own for a channel, which may not ask for another's.
func TestUsage(t *testing.T) {
	l := start(t)
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot"}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
	st := l.s.st
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	cost := 0.5
	for _, u := range []store.Usage{
		{At: now.UnixMilli(), Terminal: "local", Session: "a", Agent: "claude", Model: "haiku", Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, CostUSD: &cost},
		{At: now.UnixMilli(), Terminal: "local", Session: "a", Agent: "claude", Model: "haiku", Input: 1, Output: 2, CostUSD: &cost},
		{At: yesterday.UnixMilli(), Terminal: botID, Session: "b", Agent: "pi", Model: "flash", Input: 100},
		{At: now.AddDate(0, 0, -40).UnixMilli(), Terminal: botID, Session: "b", Agent: "pi", Model: "flash", Input: 999},
		{At: now.UnixMilli(), Terminal: "", Session: "c", Agent: "codex", Model: "luna", Output: 5},
	} {
		if err := st.AddUsage(u); err != nil {
			t.Fatal(err)
		}
	}
	var all api.Usage
	l.want(200, "socket", "GET", "/api/usage", nil, &all)
	if len(all.Days) != 7 || all.Days[6] != now.Format(time.DateOnly) || all.Days[5] != yesterday.Format(time.DateOnly) {
		t.Fatalf("days: %v", all.Days)
	}
	if len(all.Rows) != 3 {
		t.Fatalf("rows: %+v", all.Rows)
	}
	var mine *api.UsageRow
	for i, r := range all.Rows {
		if r.Terminal == "local" {
			mine = &all.Rows[i]
		}
	}
	if mine == nil || mine.Turns != 2 || mine.Input != 11 || mine.Output != 22 || mine.CacheRead != 30 || mine.CostUSD != 1 || mine.Agent != api.AgentClaude || mine.Day != all.Days[6] {
		t.Fatalf("本机's row: %+v", mine)
	}
	if all.Terminals[botID].Name != "bot" || !all.Terminals["local"].Local {
		t.Fatalf("terminals: %+v", all.Terminals)
	}
	var long api.Usage
	l.want(200, "socket", "GET", "/api/usage?days=60&terminal=bot", nil, &long)
	if len(long.Days) != 60 || len(long.Rows) != 2 || long.Rows[0].Input != 999 {
		t.Fatalf("bot's 60 days: %+v", long.Rows)
	}

	// A channel: its own, asked or not; not 本机's.
	var own api.Usage
	l.want(200, bot, "GET", "/api/usage", nil, &own)
	if len(own.Rows) != 1 || own.Rows[0].Terminal != botID || own.Rows[0].Input != 100 {
		t.Fatalf("the channel's own: %+v", own.Rows)
	}
	l.want(200, bot, "GET", "/api/usage?terminal=me", nil, &own)
	if len(own.Rows) != 1 {
		t.Fatalf("me: %+v", own.Rows)
	}
	l.want(403, bot, "GET", "/api/usage?terminal=local", nil, nil)
	l.want(404, "socket", "GET", "/api/usage?terminal=nobody", nil, nil)
	l.want(400, "socket", "GET", "/api/usage?days=0", nil, nil)
}
