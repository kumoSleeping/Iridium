package desktop

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A login waiting in the browser ends when its Iridium is removed, and when another begins:
// left waiting, it held the picker's next 连接 back.
func TestLoginOneAtATime(t *testing.T) {
	var polls atomic.Int32
	fake := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/_ir/api/app/start":
				_, _ = rw.Write([]byte(`{"id":"1","code":"AB","url":"http://` + r.Host + `/_ir/app?id=1"}`))
			case "/_ir/api/app/poll":
				polls.Add(1)
				_, _ = rw.Write([]byte(`{"status":"pending"}`))
			}
		}))
	}
	srv, srv2 := fake(), fake()
	defer srv.Close()
	defer srv2.Close()
	said := make(chan map[string]string, 8)
	start := func(server string) {
		browserLogin(server, "pc", func(string) error { return nil }, func(m map[string]string) { said <- m },
			func(string, string) { t.Error("done without an allow") })
		if m := <-said; m["code"] != "AB" {
			t.Fatalf("said %v, want the code", m)
		}
	}

	start(srv.URL)
	stopLogin("http://other")
	if logins.at != srv.URL {
		t.Fatal("another's removal ended this wait")
	}
	stopLogin(srv.URL)
	if logins.stop != nil {
		t.Fatal("the wait kept after its removal")
	}

	start(srv.URL)
	first := logins.stop
	start(srv2.URL)
	if logins.at != srv2.URL || first == nil {
		t.Fatal("the new login not the one waiting")
	}
	stopLogin(srv2.URL)
	time.Sleep(2500 * time.Millisecond)
	if n := polls.Load(); n != 0 {
		t.Fatalf("%d polls after every wait ended", n)
	}
	select {
	case m := <-said:
		t.Fatalf("an ended wait said %v", m)
	default:
	}
}
