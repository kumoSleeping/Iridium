package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func TestPrintUsage(t *testing.T) {
	u := api.Usage{
		Days: []string{"2026-11-06", "2026-11-07"},
		Rows: []api.UsageRow{
			{Day: "2026-11-06", Terminal: "p1", Agent: api.AgentClaude, Model: "opus", Turns: 3, Input: 100, Output: 2500, CacheRead: 1_200_000, CostUSD: 1.5},
			{Day: "2026-11-07", Terminal: "gone-terminal-id", Agent: api.AgentPi, Model: "flash", Turns: 1, Output: 40},
			{Day: "2026-11-07", Terminal: "p1", Agent: api.AgentClaude, Model: "opus", Turns: 2, Output: 900},
			{Day: "2026-11-07", Terminal: "", Agent: api.AgentCodex, Model: "luna", Turns: 1, Input: 7},
		},
		Terminals: map[string]api.UsageWho{"p1": {Name: "手机"}},
	}
	var b bytes.Buffer
	printUsage(&b, u, usageDims)
	got := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(got) != 7 || got[0] != "2026-11-06 到 2026-11-07" {
		t.Fatalf("%s", b.String())
	}
	// The latest day first, the most used first in it; the unknown and the removed named.
	for i, want := range []string{"日期", "2026-11-07  手机", "2026-11-07  已移除 gone-ter", "2026-11-07  （不明）", "2026-11-06  手机", "合计"} {
		if !strings.HasPrefix(got[i+1], want) {
			t.Errorf("line %d: %q, want %q first", i+1, got[i+1], want)
		}
	}
	if !strings.HasSuffix(got[5], "1.2M  $1.50") || !strings.Contains(got[6], "$1.50") {
		t.Errorf("sums:\n%s", b.String())
	}

	b.Reset()
	printUsage(&b, u, sortedDims([]string{"model", "agent"}))
	got = strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(got) != 6 || !strings.HasPrefix(got[1], "agent   模型") || !strings.HasPrefix(got[2], "claude  opus") {
		t.Fatalf("by agent, model:\n%s", b.String())
	}
}
