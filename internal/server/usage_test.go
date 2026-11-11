package server

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/pricing"
	"github.com/kumoSleeping/iridium/internal/store"
)

// GET /v1/internal/usage: by day, terminal, agent and model; everyone's for 本机 and panels, a channel's
// own for a channel, which may not ask for another's.
func TestUsage(t *testing.T) {
	t.Parallel()
	l := start(t)
	var tok api.Token
	tok = l.newApp("bot", false)
	bot, botID := tok.Token, tok.App.ID
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
	l.want(200, "socket", "GET", "/v1/internal/usage", nil, &all)
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
	if all.Terminals[botID].Name != "bot" || !all.Terminals["local"].Host {
		t.Fatalf("terminals: %+v", all.Terminals)
	}
	var long api.Usage
	l.want(200, "socket", "GET", "/v1/internal/usage?days=60&terminal=bot", nil, &long)
	if len(long.Days) != 60 || len(long.Rows) != 2 || long.Rows[0].Input != 999 {
		t.Fatalf("bot's 60 days: %+v", long.Rows)
	}
	// By hour: the last 24, this hour last; yesterday's row (24h ago) is before the first.
	var hours api.Usage
	l.want(200, "socket", "GET", "/v1/internal/usage?days=1&by=hour", nil, &hours)
	if len(hours.Days) != 24 || hours.Days[23] != now.Format("2006-01-02T15") {
		t.Fatalf("hours: %v", hours.Days)
	}
	if len(hours.Rows) != 2 || hours.Rows[0].Day != hours.Days[23] {
		t.Fatalf("hours' rows: %+v", hours.Rows)
	}
	// By weekday and hour: today's rows at today's weekday and this hour.
	var heat api.Usage
	l.want(200, "socket", "GET", "/v1/internal/usage?by=weekhour", nil, &heat)
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	if len(heat.Days) != 7 || len(heat.Rows) != 3 {
		t.Fatalf("heat: %+v", heat)
	}
	for _, r := range heat.Rows {
		if r.Terminal == "local" && r.Day != fmt.Sprintf("%dT%02d", wd, now.Hour()) {
			t.Fatalf("heat's row: %+v", r)
		}
	}

	// A channel: its own, asked or not; not 本机's.
	var own api.Usage
	l.want(200, bot, "GET", "/v1/internal/usage", nil, &own)
	if len(own.Rows) != 1 || own.Rows[0].Terminal != botID || own.Rows[0].Input != 100 {
		t.Fatalf("the channel's own: %+v", own.Rows)
	}
	l.want(200, bot, "GET", "/v1/internal/usage?terminal=me", nil, &own)
	if len(own.Rows) != 1 {
		t.Fatalf("me: %+v", own.Rows)
	}
	l.want(200, bot, "GET", "/v1/internal/usage?app=me", nil, &own) // terminal= is its name before 0.2
	if len(own.Rows) != 1 {
		t.Fatalf("app=me: %+v", own.Rows)
	}
	l.want(403, bot, "GET", "/v1/internal/usage?app=local", nil, nil)
	l.want(403, bot, "GET", "/v1/internal/usage?terminal=local", nil, nil)
	l.want(422, "socket", "GET", "/v1/internal/usage?app=nobody", nil, nil)
	l.want(400, "socket", "GET", "/v1/internal/usage?days=0", nil, nil)
}

// A row the agent gave no cost (or 0) is priced at its maker's list price; one it priced keeps it.
func TestUsagePriced(t *testing.T) {
	t.Parallel()
	l := start(t)
	f, err := os.Open("../pricing/testdata/api.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if l.s.Prices, err = pricing.Read(f); err != nil {
		t.Fatal(err)
	}
	zero, own := 0.0, 0.5
	now := time.Now().UnixMilli()
	for _, u := range []store.Usage{
		{At: now, Terminal: "local", Session: "a", Agent: "dsh", Model: "mistral/deepseek-v4-1-flash-1017", Input: 1_000_000, CacheRead: 10_000_000},
		{At: now, Terminal: "local", Session: "b", Agent: "pi", Model: "opencode-go/deepseek-flash", Output: 1_000_000, CostUSD: &zero},
		{At: now, Terminal: "local", Session: "c", Agent: "claude", Model: "claude-opus-5-5", Input: 1_000_000, CostUSD: &own},
		{At: now, Terminal: "local", Session: "d", Agent: "codex", Model: "who-knows", Input: 1_000_000},
	} {
		if err := l.s.st.AddUsage(u); err != nil {
			t.Fatal(err)
		}
	}
	var u api.Usage
	l.want(200, "socket", "GET", "/v1/internal/usage", nil, &u)
	want := map[string]float64{"mistral/deepseek-v4-1-flash-1017": 0.15 + 0.03, "opencode-go/deepseek-flash": 0.6, "claude-opus-5-5": 0.5, "who-knows": 0}
	for _, r := range u.Rows {
		if math.Abs(r.CostUSD-want[r.Model]) > 1e-9 {
			t.Errorf("%s: $%v, want $%v", r.Model, r.CostUSD, want[r.Model])
		}
	}
}
