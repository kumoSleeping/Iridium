package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/testagent"
)

// keeper is a channel that keeps pictures by handle, as dsh's attachments are.
type keeper struct {
	mu    sync.Mutex
	asked []string
	data  []byte
}

func (k *keeper) Agent() api.Agent  { return api.AgentClaude }
func (k *keeper) Bind(harness.Host) {}
func (k *keeper) Image(_ context.Context, r harness.Ref, handle string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.asked = append(k.asked, r.ID+" "+handle)
	return k.data, handle == testagent.TinyHandle, nil
}

// pictured runs a turn whose Read gives two pictures, and is the at of its result.
func pictured(t *testing.T, r *rig) (string, string) {
	t.Helper()
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "PICTURE"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, b := range r.messages(id) {
			if b.Kind == api.BlockKindTool && b.Result != nil && len(b.Result.Images) == 2 {
				return id, b.Result.At
			}
		}
	}
	t.Fatalf("no result with pictures: %+v", r.messages(id))
	return "", ""
}

func TestImageInRecordAndByHandle(t *testing.T) {
	t.Parallel()
	tiny, _ := base64.StdEncoding.DecodeString(testagent.TinyPNG)
	k := &keeper{data: tiny}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Channels = harness.Set{api.AgentClaude: k} })
	id, at := pictured(t, r)
	mime, data, err := r.m.Image(bg, id, at, 0)
	if err != nil || mime != "image/png" || !bytes.Equal(data, tiny) {
		t.Fatalf("in the record: %q %d bytes %v", mime, len(data), err)
	}
	if mime, data, err = r.m.Image(bg, id, at, 1); err != nil || mime != "image/png" || !bytes.Equal(data, tiny) {
		t.Fatalf("by its handle: %q %d bytes %v", mime, len(data), err)
	}
	if len(k.asked) != 1 || k.asked[0] != id+" "+testagent.TinyHandle {
		t.Errorf("the channel was asked %q", k.asked)
	}
	if _, _, err := r.m.Image(bg, id, at, 2); !errors.Is(err, ErrNoImage) {
		t.Errorf("a third: %v", err)
	}
	// What the channel gives is shown only if it is a picture.
	k.data = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, _, err := r.m.Image(bg, id, at, 1); !errors.Is(err, ErrNoImage) {
		t.Errorf("not a picture: %v", err)
	}
}

func TestImageByHandleWithoutChannel(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, at := pictured(t, r)
	if _, _, err := r.m.Image(bg, id, at, 1); !errors.Is(err, ErrNoImage) {
		t.Errorf("no channel: %v", err)
	}
	if _, _, err := r.m.Image(bg, "nope", at, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("no session: %v", err)
	}
}
