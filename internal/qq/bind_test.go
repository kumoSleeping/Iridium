package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The QR flow against a fake portal: a task, waiting, then the bot with its secret unsealed.
func TestBind(t *testing.T) {
	var mu sync.Mutex
	key, polls := "", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			w.Write([]byte("<html>"))
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/lite/create_bind_task":
			key = body["key"]
			fmt.Fprint(w, `{"retcode":0,"data":{"task_id":"T1"}}`)
		case "/lite/poll_bind_result":
			polls++
			if polls == 1 {
				fmt.Fprint(w, `{"retcode":0,"data":{"status":1}}`)
				return
			}
			fmt.Fprintf(w, `{"retcode":0,"data":{"status":2,"bot_appid":102030,"bot_encrypt_secret":%q,"user_openid":"OPEN"}}`, Seal(key, "SECRET"))
		}
	}))
	defer srv.Close()
	p := Portal{Base: srv.URL}
	ctx := context.Background()
	b, err := p.NewBind(ctx)
	if err != nil || b.Task != "T1" || !strings.Contains(b.URL, "task_id=T1") {
		t.Fatalf("bind %+v %v", b, err)
	}
	if st, _, err := p.Poll(ctx, b); st != BindWaiting || err != nil {
		t.Fatalf("first poll %v %v", st, err)
	}
	st, bound, err := p.Poll(ctx, b)
	if st != BindDone || err != nil || bound != (Bound{AppID: "102030", Secret: "SECRET", Owner: "OPEN"}) {
		t.Fatalf("done %v %+v %v", st, bound, err)
	}
	// Another key does not open it.
	if _, _, err := p.Poll(ctx, Bind{Task: "T1", Key: strings.Repeat("A", 43) + "="}); err == nil {
		t.Fatal("unsealed with the wrong key")
	}
}

func TestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/getAppAccessToken" {
			fmt.Fprint(w, `{"access_token":"tok","expires_in":7200}`)
			return
		}
		if r.Header.Get("Authorization") != "QQBot tok" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(400)
		fmt.Fprint(w, `{"code":40054005,"message":"dedup"}`)
	}))
	defer srv.Close()
	c := &Client{AppID: "a", Secret: "s", API: srv.URL, TokenURL: srv.URL}
	_, err := c.Send(context.Background(), "u", Message{Type: MsgText, Content: "x", MsgID: "m", MsgSeq: 1})
	var e *Error
	if !Refused(err) || !errorsAs(err, &e) || e.Code != Deduped {
		t.Fatalf("%v", err)
	}
}

func errorsAs(err error, e **Error) bool {
	x, ok := err.(*Error)
	*e = x
	return ok
}
