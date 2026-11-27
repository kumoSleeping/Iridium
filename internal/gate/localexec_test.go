package gate

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalExecTokenAuth(t *testing.T) {
	st := newMemTestStore()
	g, err := New(Config{
		Store: st,
		LocalExecToken: func(tok string) bool {
			return tok == "irl_goodtoken"
		},
		SessionToken: func(tok string) (string, string, bool) {
			if tok == "irs_local_sess" {
				return HostID, "sess-local-1", true
			}
			return "", "", false
		},
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	defer g.Close()

	var gotID Identity
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	// 1. Valid local-exec token on direct loopback GET /v1/internal/exec succeeds.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7810/v1/internal/exec", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer irl_goodtoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotID.App != LocalExecApp {
		t.Fatalf("loopback exec: status=%d id=%+v", rec.Code, gotID)
	}

	// 2. Rejected on non-exec path.
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7810/v1/internal/sessions", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer irl_goodtoken")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on non-exec path, got %d", rec.Code)
	}

	// 3. Rejected when forwarded via tunnel/proxy header.
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7810/v1/internal/exec", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer irl_goodtoken")
	req.Header.Set("CF-Connecting-IP", "203.0.113.1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with CF-Connecting-IP, got %d", rec.Code)
	}

	// 4. Rejected from non-loopback peer.
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7810/v1/internal/exec", nil)
	req.RemoteAddr = "192.168.1.50:54321"
	req.Header.Set("Authorization", "Bearer irl_goodtoken")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from non-loopback, got %d", rec.Code)
	}

	// 5. Local session irs_ token resolves to HostID + Session.
	gotID = Identity{}
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7810/v1/internal/cli", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer irs_local_sess")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotID.App != HostID || gotID.Session != "sess-local-1" {
		t.Fatalf("local session token: status=%d id=%+v", rec.Code, gotID)
	}
}

type memTestStore struct{ st State }

func newMemTestStore() *memTestStore { return &memTestStore{} }

func (m *memTestStore) Load() (State, error) { return m.st, nil }
func (m *memTestStore) Apply(c Change) error { return nil }
