package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/store"
)

// maxUsageDays is the most days GET /api/usage gives at once.
const maxUsageDays = 366

// usage is GET /api/usage?days=N&terminal=<ID, name or "me">: the last N days' (7 by default)
// usage by day, terminal, agent and model; by=hour: the last 24 hours' by hour, its days
// "2006-01-02T15" (the 1 天 view, the user, 2026-11-08); by=weekhour: the N days' by weekday and
// hour, the rows' days "1T14" (Monday, 14:00; 7 is Sunday; the heat map, the user, 2026-11-10).
// A row the agent gave no cost is priced at its maker's list price (s.Prices; the user, 2026-11-10). Without a terminal it is everyone's for those who
// manage terminals (panels, 本机), a channel's own for a channel; another's only for those who
// manage.
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxUsageDays {
			fail(w, http.StatusBadRequest, "days is 1 to 366")
			return
		}
		days = n
	}
	by := r.URL.Query().Get("by")
	hourly := by == "hour"
	var only []string
	switch ref := r.URL.Query().Get("terminal"); {
	case ref == "me":
		only = []string{c.EndpointID}
	case ref == "" && !c.CanManage():
		only = []string{c.EndpointID}
	case ref != "":
		info, ok := s.gate().Find(ref)
		if !ok {
			fail(w, http.StatusNotFound, "no such terminal")
			return
		}
		if info.ID != c.EndpointID && !c.CanManage() {
			forbid(w, errors.New("only a web panel or 本机 sees another terminal's usage"))
			return
		}
		only = []string{info.ID}
	}
	now := time.Now()
	y, m, d := now.Date()
	n, step, format := days, func(i int) time.Time { return time.Date(y, m, d-days+1+i, 0, 0, 0, 0, time.Local) }, time.DateOnly
	if hourly {
		const hourKey = "2006-01-02T15"
		h := now.Hour()
		n, step, format = 24, func(i int) time.Time { return time.Date(y, m, d, h-23+i, 0, 0, 0, time.Local) }, hourKey
	}
	bucket := func(t time.Time) string { return t.Format(format) }
	if by == "weekhour" && !hourly {
		bucket = func(t time.Time) string {
			wd := int(t.Weekday())
			if wd == 0 {
				wd = 7
			}
			return fmt.Sprintf("%dT%02d", wd, t.Hour())
		}
	}
	var price store.Price
	if s.Prices != nil {
		price = s.Prices.Cost
	}
	got, err := s.st.UsageBy(step(0).UnixMilli(), bucket, price, only...)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := api.Usage{Days: make([]string, n), Rows: make([]api.UsageRow, 0, len(got)), Terminals: map[string]api.UsageTerminal{}}
	for i := range n {
		out.Days[i] = step(i).Format(format)
	}
	name := func(id string) {
		if _, done := out.Terminals[id]; done || id == "" {
			return
		}
		if info, ok := s.gate().Endpoint(id); ok {
			out.Terminals[id] = api.UsageTerminal{Name: info.Name, Kind: api.TerminalKind(info.Kind), Avatar: info.Avatar, Local: id == gate.LocalEndpointID}
		}
	}
	for _, u := range got {
		out.Rows = append(out.Rows, api.UsageRow{Day: u.Day, Terminal: u.Terminal, Agent: api.Agent(u.Agent), Model: u.Model, Turns: u.Turns, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, CostUSD: u.CostUSD})
		name(u.Terminal)
	}
	for _, id := range only { // asked for: named even with no use
		name(id)
	}
	reply(w, http.StatusOK, out)
}
