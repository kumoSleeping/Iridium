package main

// The -mock layer: the routes the page uses that the daemon does not serve yet (web/src/api/assumed.ts:
// settings, update, this terminal), answered from memory so their parts of the page can be tried and
// looked at. Dev only: nothing here is the daemon's behaviour.

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

type mock struct {
	mu       sync.Mutex
	settings map[string]any
	me       bool
	update   bool
}

func newMock(me, update bool) *mock {
	return &mock{me: me, update: update, settings: map[string]any{
		"idleReap": "1h", "crashResume": true, "quotaResume": true,
		"push":        map[string]any{"enabled": false, "workspaces": map[string]any{}},
		"quotaHidden": []string{},
	}}
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// serve answers a mocked route; false: not one, the daemon's.
func (m *mock) serve(w http.ResponseWriter, r *http.Request) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch p := r.URL.Path; {
	case p == "/api/settings" && r.Method == "GET":
		reply(w, m.settings)
	case p == "/api/settings" && r.Method == "PUT":
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		for k, v := range b {
			m.settings[k] = v
		}
		reply(w, m.settings)
	case p == "/api/update" && m.update:
		reply(w, map[string]any{"current": "0.1.0", "latest": "0.2.0", "available": true, "notes": "· Uploads resume after a break\n· The topology view"})
	case p == "/api/me" && m.me:
		reply(w, map[string]any{"id": "e1", "name": "kumo-m2-mac", "kind": "panel", "local": false, "avatar": "/api/_dev/avatar.svg",
			"permissions": map[string]any{"agents": []string{"claude", "codex", "pi", "dsh"}, "workspaces": "all", "others": true, "copyOut": true, "copyIn": true, "answer": true, "mustApprove": false}})
	case p == "/api/_dev/avatar.svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 96 96"><defs><linearGradient id="s" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#9cc3e6"/><stop offset="1" stop-color="#f3d9c2"/></linearGradient></defs><rect width="96" height="96" fill="url(#s)"/><circle cx="64" cy="34" r="10" fill="#fff6e8"/><path d="M0 70 28 46l18 14 16-12 34 26v22H0z" fill="#6f8aa3"/><path d="M0 80 22 66l22 10 20-8 32 14v14H0z" fill="#4c6275"/></svg>`)
	default:
		return false
	}
	return true
}
