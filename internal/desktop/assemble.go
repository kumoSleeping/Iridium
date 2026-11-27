package desktop

import (
	"github.com/egoist/mygo"
)

// Assembled (otae-c, kumo: "超级拼装", 2026-11-26; docs/app.md "跨服收藏"): a favorite of another
// Iridium pressed in a page's list shows that Iridium's chat beside the list, not the other
// Iridium in the list's place. The main window then shows two pages side by side, none over the
// other: the left one's list alone (as wide as its list; its page draws no chat: {assemble:
// {part: side}}), the other's chat alone (no list: part chat), with that Iridium's name and face
// by its title. The rail stays on the left one's. A chat of the left one's list chosen, or the
// rail pressed, and one page shows again; the name by the title pressed shows that Iridium whole.
// One in a window of its own: that window comes forward, at the chat.

// sideWidth is the list's width in the left page while assembled (its page says it as it
// changes: ir:sidebar), in its points.
const sideWidth = 292

// assemble shows server's chat id beside fromServer's list (its page from, in the main window).
func (u *ui) assemble(from *mygo.Page, fromServer, server, id string) {
	link := chatLink(server, id)
	if u.awayWindow(server) != nil {
		u.openAwayAt(server, link)
		return
	}
	u.mu.Lock()
	m := u.main
	inMain := m != nil && from != nil && from.Window() == m.win && m.panes[fromServer] != nil
	u.mu.Unlock()
	if !inMain || server == fromServer {
		u.showServer(server, link)
		return
	}
	u.mu.Lock()
	p := m.panes[server]
	u.mu.Unlock()
	if p == nil {
		p = &pane{}
		if err := u.newPane(p, m.win, server, link); err != nil {
			appLog()("pane %s: %v", server, err)
			return
		}
		u.mu.Lock()
		m.panes[server] = p
		u.mu.Unlock()
	} else {
		_ = p.page().LoadURL(themed(link))
	}
	u.mu.Lock()
	was := m.left
	m.left, m.shown = fromServer, server
	u.mu.Unlock()
	if was != "" && was != fromServer {
		u.toServerPages(was, map[string]any{"assemble": map[string]any{"part": ""}})
	}
	u.toServerPages(fromServer, u.partOf(fromServer, from))
	reply(p.page(), u.partOf(server, p.page()))
	u.railRefresh()
	m.win.Show()
	m.win.Focus()
	p.view.Load().Focus()
}

// partOf is what page p of server is while assembled, as it is told (and again as it loads:
// one made for it is told before its page is there): side, chat (with its Iridium's name and
// face), or whole.
func (u *ui) partOf(server string, p *mygo.Page) map[string]any {
	u.mu.Lock()
	m := u.main
	part := ""
	if m != nil && m.left != "" && p != nil && p.Window() == m.win {
		switch server {
		case m.left:
			part = "side"
		case m.shown:
			part = "chat"
		}
	}
	u.mu.Unlock()
	a := map[string]any{"part": part}
	if part == "chat" {
		c, _ := loadConfig()
		title, face := hostOf(server), ""
		if s := c.server(server); s != nil {
			title, face = s.label(), s.Face
			if s.Local && s.Title == "" {
				title = "此电脑"
			}
		}
		a["title"], a["face"] = title, face
	}
	return map[string]any{"assemble": a}
}

// unassemble shows one page again in the main window: the pages told; the window drawn as the
// caller shows what it shows next.
func (u *ui) unassemble() {
	u.mu.Lock()
	m := u.main
	if m == nil || m.left == "" {
		u.mu.Unlock()
		return
	}
	left, right := m.left, m.shown
	m.left = ""
	u.mu.Unlock()
	off := map[string]any{"assemble": map[string]any{"part": ""}}
	u.toServerPages(left, off)
	u.toServerPages(right, off)
}

// assembledSaid is what an assembled page says: off (the left one's list chose one of its own
// chats: that page whole again), full (the name by the right one's title pressed: that Iridium
// whole).
func (u *ui) assembledSaid(server, arg string) {
	u.mu.Lock()
	m := u.main
	left, right := "", ""
	if m != nil {
		left, right = m.left, m.shown
	}
	u.mu.Unlock()
	switch {
	case left == "":
	case arg == "off" && server == left:
		u.showServer(left, "")
	case arg == "full" && server == right:
		u.showServer(right, "")
	}
}

// sideSet: the line between two pages side by side dragged: the left one's list w wide, which
// its page keeps.
func (u *ui) sideSet(server string, w int) {
	u.sideSaid(server, w)
	u.toServerPages(server, map[string]any{"sidebarW": w})
}

// sideSaid: the left page's list is w wide (ir:sidebar).
func (u *ui) sideSaid(server string, w int) {
	if w < 120 || w > 2000 {
		return
	}
	u.mu.Lock()
	m := u.main
	changed := m != nil && m.sideW[server] != w
	if changed {
		if m.sideW == nil {
			m.sideW = map[string]int{}
		}
		m.sideW[server] = w
	}
	assembled := m != nil && m.left == server
	u.mu.Unlock()
	if changed && assembled {
		u.railRefresh()
	}
}
