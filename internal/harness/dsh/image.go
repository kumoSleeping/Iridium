package dsh

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/dshhome"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// A picture a tool read for dsh's model (read_image) is not in the session's record: dsh keeps
// it in its attachments, by its content's sha256, and the record has only that handle
// (harness.Imager). It is read from the home of the machine the session runs at: this one's,
// or through the executor. Checked against its sum, a picture read is good for ever: the last
// few are kept here, so one shown again is not read again from another machine.

const (
	maxImage  = 32 << 20 // the largest picture read
	keptBytes = 64 << 20 // the pictures kept, at most
)

var _ harness.Imager = (*Channel)(nil)

// Image is the picture handle names, from session r's machine. done false: not a handle of dsh's.
func (c *Channel) Image(ctx context.Context, r harness.Ref, handle string) ([]byte, bool, error) {
	elems, sum, ok := dshhome.Attachment(handle)
	if !ours(r) || !ok {
		return nil, false, nil
	}
	if data, ok := c.kept(sum); ok {
		return data, true, nil
	}
	var data []byte
	var err error
	if r.Machine == "" {
		data, err = c.localImage(elems)
	} else {
		data, err = c.remoteImage(ctx, r.Machine, elems)
	}
	if err != nil {
		return nil, true, err
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
		return nil, true, fmt.Errorf("dsh: attachment %s: %w", handle, harness.ErrGone)
	}
	c.keep(sum, data)
	return data, true, nil
}

// localImage reads elems below this machine's dsh home.
func (c *Channel) localImage(elems []string) ([]byte, error) {
	var env []string
	if h := c.h(); h != nil {
		env = h.AgentEnv()
	}
	path := filepath.Join(append([]string{transcript.DshHome(env)}, elems...)...)
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("dsh: %s: %w", path, harness.ErrGone)
	}
	if err != nil {
		return nil, err
	}
	if st.Size() > maxImage {
		return nil, fmt.Errorf("dsh: %s: %d bytes, more than %d", path, st.Size(), maxImage)
	}
	return os.ReadFile(path)
}

// remoteImage reads elems below dsh's home at machine, as dsh finds it there.
func (c *Channel) remoteImage(ctx context.Context, machine string, elems []string) ([]byte, error) {
	mc := c.machine(machine)
	if mc == nil {
		return nil, harness.ErrOffline
	}
	hi, _ := mc.Hello()
	ai, _ := mc.Agent(string(api.AgentDsh))
	home := cmp.Or(dshhome.Home(ai.Home, hi.Env), remote.Join(hi.Sep, hi.Home, ".dsh"))
	path := remote.Join(hi.Sep, append([]string{home}, elems...)...)
	data, err := mc.ReadFile(ctx, path, maxImage)
	if we := (*wire.Error)(nil); errors.As(err, &we) && we.Code == wire.CodeNotFound {
		return nil, fmt.Errorf("dsh: %s: %w", path, harness.ErrGone)
	}
	return data, err
}

// kept is the picture of sum read before, if it is still kept.
func (c *Channel) kept(sum string) ([]byte, bool) {
	c.imageMu.Lock()
	defer c.imageMu.Unlock()
	data, ok := c.images[sum]
	return data, ok
}

// keep keeps the picture of sum, the oldest going while those kept are more than keptBytes.
func (c *Channel) keep(sum string, data []byte) {
	c.imageMu.Lock()
	defer c.imageMu.Unlock()
	if _, ok := c.images[sum]; ok || len(data) > keptBytes {
		return
	}
	if c.images == nil {
		c.images = map[string][]byte{}
	}
	c.images[sum] = data
	c.imageOrder = append(c.imageOrder, sum)
	c.imageBytes += len(data)
	for c.imageBytes > keptBytes {
		old := c.imageOrder[0]
		c.imageOrder = c.imageOrder[1:]
		c.imageBytes -= len(c.images[old])
		delete(c.images, old)
	}
}
