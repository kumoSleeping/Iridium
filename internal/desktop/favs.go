package desktop

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
)

// Favorites across the Iridiums (otae-c, kumo, 霓虹, 2026-11-26; docs/app.md "跨服收藏"): each
// Iridium keeps its own favorites (internal/server/favorites.go); the app gathers what each of
// their pages says of them (ir:favs, web/src/crossfav.ts: a few words a row, its favorites alone),
// keeps one order of them all and the rows as last said (favs.json beside app.json: an Iridium not
// opened since the app started is drawn at once), and gives every page all of them (an "ir-app"
// event {favs}), with each Iridium's name and face here. A page draws them among its own; a row
// of another Iridium pressed shows it beside this one (assemble.go).
//
// What is asked of an Iridium's favorites from another's page (taken off, put in another order)
// goes to that Iridium's page ({favdrop}, {favput}): its page puts it, as its own row's menu does.
// Its page not open yet: it is opened in the main window, out of sight, and asked once it says
// what it has.

// favKey is a favorite's key in the order: its Iridium's address, #, its id.
func favKey(server, id string) string { return server + "#" + id }

// favState is what the app keeps of the favorites.
type favState struct {
	Order []string                     `json:"order"`
	Items map[string][]json.RawMessage `json:"items"` // by Iridium, in its own order
}

var favs struct {
	sync.Mutex
	st      favState
	loaded  bool
	pending map[string][]map[string]any // what waits for an Iridium's page, by address
}

func favPath() string {
	p, err := configPath()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "favs.json")
}

// favLoad reads favs.json once. favs held.
func favLoad() {
	if favs.loaded {
		return
	}
	favs.loaded = true
	if p := favPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &favs.st)
		}
	}
	if favs.st.Items == nil {
		favs.st.Items = map[string][]json.RawMessage{}
	}
}

// favSave writes favs.json. favs held.
func favSave() {
	if p := favPath(); p != "" {
		if b, err := json.Marshal(favs.st); err == nil {
			_ = os.WriteFile(p, b, 0o600)
		}
	}
}

// favIDs are the ids of an Iridium's rows.
func favIDs(items []json.RawMessage) []string {
	ids := make([]string, 0, len(items))
	for _, raw := range items {
		var it struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &it) == nil && it.ID != "" {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// favMerge takes server's favorites as its page says them (ids in its order) into order: those
// it no longer has go; the places its own take in order are filled in its order (moved there, or
// put in another order on its page); new ones go last.
func favMerge(order []string, server string, ids []string) []string {
	prefix := server + "#"
	var have, fresh []string
	for _, id := range ids {
		if k := favKey(server, id); slices.Contains(order, k) {
			have = append(have, k)
		} else {
			fresh = append(fresh, k)
		}
	}
	out := make([]string, 0, len(order)+len(fresh))
	for _, k := range order {
		if !strings.HasPrefix(k, prefix) {
			out = append(out, k)
		} else if len(have) > 0 {
			out = append(out, have[0])
			have = have[1:]
		}
	}
	return append(out, fresh...)
}

// favOrderOf is server's ids in the order's order.
func favOrderOf(order []string, server string) []string {
	prefix := server + "#"
	var ids []string
	for _, k := range order {
		if id, ok := strings.CutPrefix(k, prefix); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// favsSaid: server's page said its favorites (raw: {"items": [...]}).
func (u *ui) favsSaid(server string, raw string) {
	var in struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &in) != nil || len(raw) > 1<<20 {
		return
	}
	favs.Lock()
	favLoad()
	ids := favIDs(in.Items)
	was, _ := json.Marshal(favs.st)
	favs.st.Items[server] = in.Items
	if len(in.Items) == 0 {
		delete(favs.st.Items, server)
	}
	favs.st.Order = favMerge(favs.st.Order, server, ids)
	now, _ := json.Marshal(favs.st)
	changed := string(was) != string(now)
	if changed {
		favSave()
	}
	waiting := favs.pending[server]
	delete(favs.pending, server)
	favs.Unlock()
	for _, m := range waiting {
		u.toServerPages(server, m)
	}
	if changed {
		u.favsTell()
	}
}

// favsOrder: a page put the favorites in another order (keys); each Iridium whose own come in
// another order is asked to put them so.
func (u *ui) favsOrder(raw string) {
	var keys []string
	if json.Unmarshal([]byte(raw), &keys) != nil {
		return
	}
	favs.Lock()
	favLoad()
	var order []string
	for _, k := range keys {
		if !slices.Contains(order, k) && strings.Contains(k, "#") {
			order = append(order, k)
		}
	}
	// Those it left out keep at the end.
	for _, k := range favs.st.Order {
		if !slices.Contains(order, k) {
			order = append(order, k)
		}
	}
	favs.st.Order = order
	puts := map[string][]string{}
	for server, items := range favs.st.Items {
		mine := favOrderOf(order, server)
		ids := favIDs(items)
		var known []string
		for _, id := range mine {
			if slices.Contains(ids, id) {
				known = append(known, id)
			}
		}
		if !slices.Equal(known, ids) || len(mine) != len(known) {
			puts[server] = mine
		}
	}
	favSave()
	favs.Unlock()
	for server, ids := range puts {
		u.askServer(server, map[string]any{"favput": ids})
	}
	u.favsTell()
}

// askServer gives server's page m; its page not open, it is opened (in the main window, out of
// sight) and given m once it says its favorites.
func (u *ui) askServer(server string, m map[string]any) {
	if u.toServerPages(server, m) {
		return
	}
	favs.Lock()
	if favs.pending == nil {
		favs.pending = map[string][]map[string]any{}
	}
	favs.pending[server] = append(favs.pending[server], m)
	favs.Unlock()
	go u.openOutOfSight(server)
}

// toServerPages gives m to every page of server; false: none open.
func (u *ui) toServerPages(server string, m map[string]any) bool {
	u.mu.Lock()
	var ps []*mygo.Page
	for _, p := range u.panes {
		if u.shows[p.name] == server {
			ps = append(ps, p.page())
		}
	}
	u.mu.Unlock()
	for _, p := range ps {
		reply(p, m)
	}
	return len(ps) > 0
}

// openOutOfSight makes server's page in the main window without showing it.
func (u *ui) openOutOfSight(server string) {
	c, _ := loadConfig()
	if s := c.server(server); s == nil || !(s.LoggedIn || s.Local && loopback(s.URL)) {
		return
	}
	m := u.mainWindow()
	if m == nil {
		return
	}
	u.mu.Lock()
	have := m.panes[server] != nil
	u.mu.Unlock()
	if have || u.awayWindow(server) != nil {
		return
	}
	p := &pane{}
	if err := u.newPane(p, m.win, server, server+"/"); err != nil {
		return
	}
	u.mu.Lock()
	m.panes[server] = p
	u.mu.Unlock()
}

// favsPayload is what a page of self is given of the favorites.
func (u *ui) favsPayload(c Config, self string) map[string]any {
	favs.Lock()
	favLoad()
	order := append([]string{}, favs.st.Order...) // never null: a page reads it as a list
	items := map[string][]json.RawMessage{}
	for k, v := range favs.st.Items {
		if c.server(k) != nil {
			items[k] = v
		}
	}
	favs.Unlock()
	servers := map[string]any{}
	for _, s := range c.Servers {
		title := s.label()
		if s.Local && s.Title == "" {
			title = "此电脑"
		}
		servers[s.URL] = map[string]any{"title": title, "face": s.Face, "local": s.Local}
	}
	return map[string]any{"favs": map[string]any{"self": self, "order": order, "items": items, "servers": servers}}
}

// favsTell gives every Iridium's page the favorites as they are now.
func (u *ui) favsTell() {
	c, _ := loadConfig()
	u.mu.Lock()
	type to struct {
		p      *mygo.Page
		server string
	}
	var all []to
	for _, p := range u.panes {
		all = append(all, to{p.page(), u.shows[p.name]})
	}
	u.mu.Unlock()
	for _, t := range all {
		if t.server != "" {
			reply(t.p, u.favsPayload(c, t.server))
		}
	}
}

// favAsk is what a page asks of a favorite (ir:fav): op open (shown beside the page: assemble.go),
// away (in its window of its own), drop (taken off its Iridium's favorites).
func (u *ui) favAsk(from *mygo.Page, fromServer, raw string) {
	var a struct {
		Op, Server, ID string
	}
	if json.Unmarshal([]byte(raw), &a) != nil || a.Server == "" || a.ID == "" {
		return
	}
	c, _ := loadConfig()
	if c.server(a.Server) == nil {
		return
	}
	switch a.Op {
	case "open":
		go u.assemble(from, fromServer, a.Server, a.ID)
	case "away":
		go u.openAwayAt(a.Server, chatLink(a.Server, a.ID))
	case "drop":
		u.askServer(a.Server, map[string]any{"favdrop": a.ID})
	}
}

// chatLink is the address of a chat on server.
func chatLink(server, id string) string {
	return server + "/#/s/" + url.PathEscape(id)
}

// openAwayAt shows server at link in its window of its own, made if it has none.
func (u *ui) openAwayAt(server, link string) {
	if w := u.awayWindow(server); w != nil {
		if p := u.pageIn(w); p != nil {
			_ = p.LoadURL(themed(link))
		}
		w.Show()
		w.Focus()
		return
	}
	_, _ = updateConfig(func(c *Config) { c.setAway(server, true, Frame{}) })
	u.newWindow(server, link)
	u.leave(server)
}
