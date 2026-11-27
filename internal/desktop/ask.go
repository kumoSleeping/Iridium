package desktop

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/egoist/mygo"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
)

// A request from an Iridium for more of this computer (docs/app.md "请求"): the Iridium may only
// ask; the app takes the request apart (command lines, each folder, each agent) and shows it in a
// small panel of its own (web/src/appwin, #ask), in Iridium's look, never in the Iridium's page:
// the person allows each item or not. One request at a time; no answer in time is no.

type asking struct {
	server, by string
	items      []executor.Item
	answer     chan askReply
}

// askReply is the panel's answer: one yes or no an item; always, this Iridium is not asked again.
type askReply struct {
	Yes    []bool `json:"yes"`
	Always bool   `json:"always"`
}

var asks struct {
	one sync.Mutex // one panel at a time
	mu  sync.Mutex
	now *asking
	win *mygo.Window
}

// askEach shows the panel for a request from server and waits for its answer.
func (u *ui) askEach(ctx context.Context, server, by string, items []executor.Item) []bool {
	asks.one.Lock()
	defer asks.one.Unlock()
	a := &asking{server: server, by: by, items: items, answer: make(chan askReply, 1)}
	asks.mu.Lock()
	asks.now = a
	asks.mu.Unlock()
	// In front of the others. No title bar (chrome.go): 拒绝 and 允许 are its keys, Esc 拒绝.
	win := mygo.NewWindow(bare(mygo.WindowOptions{
		Title: "Iridium", URL: themed("/#ask"), Page: pageOptions("", "", 0),
		Width: 360, Height: 150 + 44*len(items), DisableResize: true, AlwaysOnTop: true,
		DisableMaximize: true, DisableMinimize: true,
	}))
	guard(win.Page(), "")
	u.named(win.Page(), "ask")
	asks.mu.Lock()
	asks.win = win
	asks.mu.Unlock()
	win.Focus()
	var r askReply
	select {
	case r = <-a.answer:
	case <-ctx.Done():
	}
	yes := r.Yes
	if r.Always && server != "" {
		_, _ = updateConfig(func(c *Config) {
			if s := c.server(server); s != nil {
				s.AlwaysAllow = true
			}
		})
	}
	asks.mu.Lock()
	asks.now = nil
	w := asks.win
	asks.win = nil
	asks.mu.Unlock()
	if w != nil {
		w.Close()
	}
	if len(yes) != len(items) {
		return make([]bool, len(items))
	}
	return yes
}

// askState is the request the panel shows; nil: none.
func askState() map[string]any {
	asks.mu.Lock()
	defer asks.mu.Unlock()
	a := asks.now
	if a == nil {
		return map[string]any{"ask": nil}
	}
	items := []map[string]string{}
	for _, it := range a.items {
		items = append(items, map[string]string{"kind": it.Kind, "value": it.Value})
	}
	return map[string]any{"ask": map[string]any{"server": hostOf(a.server), "by": a.by, "items": items}}
}

// answerAsk is the panel's answer (reply as JSON, or a JSON array of bools, one an item).
func answerAsk(arg string) {
	var r askReply
	if json.Unmarshal([]byte(arg), &r) != nil {
		_ = json.Unmarshal([]byte(arg), &r.Yes)
	}
	asks.mu.Lock()
	a := asks.now
	asks.mu.Unlock()
	if a != nil {
		select {
		case a.answer <- r:
		default:
		}
	}
}
