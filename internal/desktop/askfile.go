package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/remote/executor"
)

// The runtime has no window of its own: a request from an Iridium it joins (more of this computer,
// docs/app.md "请求") goes to the window as a file, ask-<pid>-<n>.json in the run directory, and the
// window's answer comes back as answer-<pid>-<n>.json. No window open, or none in time: no.

type askFile struct {
	Server string          `json:"server"`
	By     string          `json:"by"`
	Items  []executor.Item `json:"items"`
}

var askSeq atomic.Int64

// windowOpen: does a window run here?
func windowOpen() bool {
	for _, r := range Running() {
		if r.Kind == "window" {
			return true
		}
	}
	return false
}

// writeWhole writes path as a whole: made under another name and renamed, so the other side
// never reads it half written (an empty ask was marked done and never answered: TestAskWindow
// on CI; a half answer read as no).
func writeWhole(path string, b []byte) error {
	tmp := filepath.Join(filepath.Dir(path), ".part-"+filepath.Base(path))
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// askWindow asks the window about items; one answer each, false where none.
func askWindow(ctx context.Context, server, by string, items []executor.Item) []bool {
	no := make([]bool, len(items))
	d, err := runDir()
	if err != nil || !windowOpen() {
		return no
	}
	id := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(askSeq.Add(1), 10)
	ask, answer := filepath.Join(d, "ask-"+id+".json"), filepath.Join(d, "answer-"+id+".json")
	b, _ := json.Marshal(askFile{Server: server, By: by, Items: items})
	if writeWhole(ask, b) != nil {
		return no
	}
	defer os.Remove(ask)
	defer os.Remove(answer)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		if b, err := os.ReadFile(answer); err == nil {
			var yes []bool
			if json.Unmarshal(b, &yes) == nil && len(yes) == len(items) {
				return yes
			}
			return no
		}
		select {
		case <-ctx.Done():
			return no
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// answerAsks is the window's side: each request the runtime left is shown (ask, one at a time)
// and answered, till ctx ends.
func answerAsks(ctx context.Context, ask func(ctx context.Context, server, by string, items []executor.Item) []bool) {
	done := map[string]bool{}
	for {
		if d, err := runDir(); err == nil {
			ents, _ := os.ReadDir(d)
			for _, e := range ents {
				name := e.Name()
				id, ok := strings.CutPrefix(strings.TrimSuffix(name, ".json"), "ask-")
				if !ok || done[id] {
					continue
				}
				done[id] = true
				var a askFile
				b, err := os.ReadFile(filepath.Join(d, name))
				if err != nil || json.Unmarshal(b, &a) != nil {
					continue
				}
				yes := ask(ctx, a.Server, a.By, a.Items)
				out, _ := json.Marshal(yes)
				_ = writeWhole(filepath.Join(d, "answer-"+id+".json"), out)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
