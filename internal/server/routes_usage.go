package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// maxUsageDays is the most days GET /api/usage gives at once.
const maxUsageDays = 366

// usage is GET /api/usage?days=N&terminal=<ID, name or "me">: the last N days' (7 by default)
// usage by day, terminal, agent and model. Without a terminal it is everyone's for those who
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
	y, m, d := time.Now().Date()
	first := time.Date(y, m, d-days+1, 0, 0, 0, 0, time.Local)
	got, err := s.st.UsageByDay(first.UnixMilli(), only...)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := api.Usage{Days: make([]string, days), Rows: make([]api.UsageRow, 0, len(got)), Terminals: map[string]api.UsageTerminal{}}
	for i := range days {
		out.Days[i] = time.Date(y, m, d-days+1+i, 0, 0, 0, 0, time.Local).Format(time.DateOnly)
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
