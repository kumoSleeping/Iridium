package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, strings.Replace(url, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// next is the member's next frame of kind, the others skipped.
func next(t *testing.T, ws *websocket.Conn, kind websocket.MessageType) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		k, b, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if k == kind {
			return b
		}
	}
}

func sig(t *testing.T, ws *websocket.Conn, op string) signal {
	t.Helper()
	for {
		var s signal
		if err := json.Unmarshal(next(t, ws, websocket.MessageText), &s); err != nil {
			t.Fatal(err)
		}
		if s.Op == op {
			return s
		}
	}
}

func frame(seq uint16, payload string) []byte {
	b := make([]byte, Header, Header+len(payload))
	b[0] = KindOpus
	binary.BigEndian.PutUint16(b[2:], seq)
	return append(b, payload...)
}

func serve(t *testing.T, rs *Rooms) string {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		rs.Serve(w, r, r.URL.Query().Get("room"), Who{App: "ir_web", Name: r.URL.Query().Get("name")})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A frame goes to the others as it came, the sender's number before it; not back to the sender,
// not to another room, not from a muted member. The members are said as they change.
func TestRoom(t *testing.T) {
	rs := New(nil)
	url := serve(t, rs)
	ctx := context.Background()
	a := dial(t, url+"?room=r&name=a")
	ha := sig(t, a, "hello")
	if ha.Self != 1 || len(ha.Members) != 1 || ha.Now == 0 {
		t.Fatalf("hello %+v", ha)
	}
	b := dial(t, url+"?room=r&name=b")
	if hb := sig(t, b, "hello"); hb.Self != 2 || len(hb.Members) != 2 || hb.Members[0].Name != "a" {
		t.Fatalf("hello %+v", hb)
	}
	if m := sig(t, a, "members"); len(m.Members) != 2 {
		t.Fatalf("members %+v", m)
	}
	other := dial(t, url+"?room=other&name=c")
	sig(t, other, "hello")

	if err := a.Write(ctx, websocket.MessageBinary, frame(7, "opus")); err != nil {
		t.Fatal(err)
	}
	got := next(t, b, websocket.MessageBinary)
	if binary.BigEndian.Uint16(got) != 1 || string(got[2:]) != string(frame(7, "opus")) {
		t.Fatalf("frame %q", got)
	}

	// A ping comes back with the room's clock.
	_ = a.Write(ctx, websocket.MessageText, []byte(`{"op":"ping","t":42.5}`))
	if p := sig(t, a, "pong"); p.T != 42.5 || p.Now == 0 {
		t.Fatalf("pong %+v", p)
	}

	// Muted: said to all, its frames not passed.
	_ = a.Write(ctx, websocket.MessageText, []byte(`{"op":"mute","muted":true}`))
	if m := sig(t, b, "members"); !m.Members[0].Muted {
		t.Fatalf("members %+v", m)
	}
	_ = a.Write(ctx, websocket.MessageBinary, frame(8, "muted"))
	_ = a.Write(ctx, websocket.MessageText, []byte(`{"op":"mute","muted":false}`))
	sig(t, b, "members")
	_ = a.Write(ctx, websocket.MessageBinary, frame(9, "after"))
	if got := next(t, b, websocket.MessageBinary); binary.BigEndian.Uint16(got[4:]) != 9 {
		t.Fatalf("a muted member's frame was passed: %q", got)
	}

	// Gone: said to the rest; the room with nobody is gone.
	a.Close(websocket.StatusNormalClosure, "")
	if m := sig(t, b, "members"); len(m.Members) != 1 || m.Members[0].Name != "b" {
		t.Fatalf("members %+v", m)
	}
	b.Close(websocket.StatusNormalClosure, "")
	for i := 0; len(rs.Members("r")) != 0; i++ {
		if i > 200 {
			t.Fatal("the room stayed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A listener that does not read: the oldest frames go, the newest stay.
func TestBacklogDropsOldest(t *testing.T) {
	rs := New(nil)
	a, err := rs.join("r", Who{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := rs.join("r", Who{Name: "b"})
	for i := range Backlog + 10 {
		rs.pass("r", a, frame(uint16(i), "x"))
	}
	if len(b.audio) != Backlog || b.dropped != 10 {
		t.Fatalf("%d waiting, %d dropped", len(b.audio), b.dropped)
	}
	if first := <-b.audio; binary.BigEndian.Uint16(first[4:]) != 10 {
		t.Fatalf("the oldest left is %d", binary.BigEndian.Uint16(first[4:]))
	}
	if len(a.audio) != 0 {
		t.Fatal("a frame came back to its sender")
	}
}

func TestFull(t *testing.T) {
	rs := New(nil)
	for range MaxMembers {
		if _, err := rs.join("r", Who{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rs.join("r", Who{}); err != ErrFull {
		t.Fatalf("err %v", err)
	}
}
