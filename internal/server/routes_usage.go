package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// maxUsageDays is the most days GET /v1/internal/usage gives at once.
const maxUsageDays = 366

// usage is GET /v1/internal/usage?days=N&app=<ID, name or "me"> (terminal= as before 0.2): the last N
// days' (7 by default) usage by day, app, agent and model; by=hour: the last 24 hours' by hour, its days
// "2006-01-02T15" (the 1 天 view, the user, 2026-11-08); by=weekhour: the N days' by weekday and
// hour, the rows' days "1T14" (Monday, 14:00; 7 is Sunday; the heat map, the user, 2026-11-10).
// A row the agent gave no cost is priced at its maker's list price (s.Prices; the user, 2026-11-10). Without an app it is everyone's for those who
// manage (ir_web's logins, the host), else the caller's own; another's only for those who manage.
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
	ref := r.URL.Query().Get("app")
	if ref == "" {
		ref = r.URL.Query().Get("terminal") // its name before 0.2
	}
	switch {
	case ref == "me":
		only = []string{c.ID()}
	case ref == "" && !c.CanManage():
		only = []string{c.ID()}
	case ref != "":
		info, ok := s.gate().Find(ref)
		if !ok {
			refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app")
			return
		}
		if info.ID != c.ID() && !c.CanManage() {
			forbid(w, errors.New("only ir_web's logins and the host see another's usage"))
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
	out := api.Usage{Days: make([]string, n), Rows: make([]api.UsageRow, 0, len(got)), Terminals: map[string]api.UsageWho{}}
	for i := range n {
		out.Days[i] = step(i).Format(format)
	}
	name := func(id string) {
		if _, done := out.Terminals[id]; done || id == "" {
			return
		}
		if info, ok := s.gate().Endpoint(id); ok {
			out.Terminals[id] = api.UsageWho{Name: info.Name, App: info.Identity().App, Avatar: info.Avatar, Host: info.Host()}
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
