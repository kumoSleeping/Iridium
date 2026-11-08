package wire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pair is a Conn running on a test server's side, and the raw client socket.
func pair(t *testing.T) (*Conn, chan error, *websocket.Conn) {
	t.Helper()
	got := make(chan *Conn, 1)
	ran := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c := NewConn(ws)
		got <- c
		ran <- c.Run(context.Background())
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return <-got, ran, ws
}

func TestHeartbeat(t *testing.T) {
	every, dead := PingEvery, DeadAfter
	PingEvery, DeadAfter = 50*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { PingEvery, DeadAfter = every, dead })

	// A peer that answers (its reads answer pings) has a round trip.
	c, _, ws := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}()
	for deadline := time.Now().Add(2 * time.Second); c.RTT() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no round trip")
		}
	}

	// A peer that pinged and then went quiet is cut off.
	_, ran, quiet := pair(t)
	if err := quiet.Write(context.Background(), websocket.MessageText, []byte(`{"event":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("a quiet peer was kept")
	}

	// One that never pings (an older one) is kept however quiet.
	_, ran, _ = pair(t)
	select {
	case err := <-ran:
		t.Fatalf("an older peer was cut off: %v", err)
	case <-time.After(time.Second):
	}
}
