package main

// The -mock layer: the routes the page uses that the daemon does not serve yet (web/src/api/assumed.ts),
// answered from memory and the disk, so their parts of the page can be tried and looked at. It also
// puts each session in the workspace whose root its folder is under, in the list and the session
// states the event stream carries. Dev only: nothing here is the daemon's behaviour.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

type mock struct {
	mu       sync.Mutex
	home     string
	ws       []workspace
	settings map[string]any
	patches  map[string]map[string]any
	me       bool
	update   bool
}

func newMock(home, spec string, me, update bool) *mock {
	m := &mock{home: home, patches: map[string]map[string]any{}, me: me, update: update}
	for i, part := range strings.Split(spec, ",") {
		if name, root, ok := strings.Cut(part, "="); ok {
			m.ws = append(m.ws, workspace{ID: int64(i + 1), Name: name, Root: root})
		}
	}
	m.settings = map[string]any{"idleReap": "1h", "crashResume": true, "quotaResume": true, "push": map[string]any{"enabled": false, "workspaces": map[string]any{}}, "quotaHidden": []string{}}
	return m
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// serve answers a mocked route; false: not one, the daemon's.
func (m *mock) serve(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case p == "/api/workspaces" && r.Method == "GET":
		reply(w, m.ws)
	case p == "/api/workspaces" && r.Method == "POST":
		var b workspace
		json.NewDecoder(r.Body).Decode(&b)
		b.ID = int64(len(m.ws) + 1)
		m.ws = append(m.ws, b)
		reply(w, b)
	case p == "/api/folders" && r.Method == "GET":
		dir := r.URL.Query().Get("path")
		if dir == "~" || dir == "" {
			dir = m.home
		} else if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(m.home, dir[2:])
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			w.WriteHeader(400)
			reply(w, map[string]string{"error": err.Error()})
			return true
		}
		folders := []string{}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				folders = append(folders, e.Name())
			}
		}
		sort.Slice(folders, func(i, j int) bool { return strings.ToLower(folders[i]) < strings.ToLower(folders[j]) })
		reply(w, map[string]any{"path": filepath.Clean(dir), "home": m.home, "folders": folders})
	case p == "/api/folders" && r.Method == "POST":
		var b struct{ Path string }
		json.NewDecoder(r.Body).Decode(&b)
		if !strings.HasPrefix(filepath.Clean(b.Path), m.home+"/") {
			w.WriteHeader(403)
			reply(w, map[string]string{"error": "outside the mock's home"})
			return true
		}
		os.MkdirAll(b.Path, 0o755)
		reply(w, map[string]string{"path": b.Path})
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
	case strings.HasPrefix(p, "/api/sessions/") && strings.HasSuffix(p, "/files") && r.Method == "POST":
		var b struct{ Paths []string }
		json.NewDecoder(r.Body).Decode(&b)
		out := []map[string]any{}
		for _, path := range b.Paths {
			if st, err := os.Stat(path); err == nil && !st.IsDir() {
				t := mime.TypeByExtension(filepath.Ext(path))
				out = append(out, map[string]any{"path": path, "name": filepath.Base(path), "size": st.Size(), "type": t, "image": strings.HasPrefix(t, "image/")})
			}
		}
		reply(w, out)
	case strings.HasPrefix(p, "/api/sessions/") && strings.HasSuffix(p, "/file") && r.Method == "GET":
		http.ServeFile(w, r, r.URL.Query().Get("path"))
	case strings.HasPrefix(p, "/api/sessions/") && r.Method == "PATCH" && strings.Count(p, "/") == 3:
		id := strings.TrimPrefix(p, "/api/sessions/")
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if m.patches[id] == nil {
			m.patches[id] = map[string]any{}
		}
		for k, v := range b {
			m.patches[id][k] = v
		}
		reply(w, map[string]any{"id": id})
	default:
		return false
	}
	return true
}

// session puts a session (a list row or a session state) in its workspace and lays the patches over it.
func (m *mock) session(s map[string]any) {
	cwd, _ := s["cwd"].(string)
	best := ""
	for _, w := range m.ws {
		if (cwd == w.Root || strings.HasPrefix(cwd, w.Root+"/")) && len(w.Root) > len(best) {
			best = w.Root
			s["workspace"] = w.ID
		}
	}
	id, _ := s["id"].(string)
	for k, v := range m.patches[id] {
		s[k] = v
	}
}

// rewrite changes the event stream's list and session states as session says, line by line.
func (m *mock) rewrite(body io.ReadCloser) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer body.Close()
		br := bufio.NewReader(body)
		for {
			line, err := br.ReadBytes('\n')
			if bytes.HasPrefix(line, []byte("data: {\"key\":")) {
				var e struct {
					Key   string          `json:"key"`
					Value json.RawMessage `json:"value"`
				}
				if json.Unmarshal(line[6:], &e) == nil {
					m.mu.Lock()
					var v any
					json.Unmarshal(e.Value, &v)
					switch x := v.(type) {
					case []any:
						if e.Key == "list" {
							for _, s := range x {
								if s, ok := s.(map[string]any); ok {
									m.session(s)
								}
							}
						}
					case map[string]any:
						if strings.HasPrefix(e.Key, "session:") {
							m.session(x)
						}
					}
					m.mu.Unlock()
					b, _ := json.Marshal(map[string]any{"key": e.Key, "value": v})
					line = append(append([]byte("data: "), b...), '\n')
				}
			}
			if len(line) > 0 {
				if _, werr := pw.Write(line); werr != nil {
					return
				}
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}
