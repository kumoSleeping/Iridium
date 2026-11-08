package remote

import (
	"github.com/kumoSleeping/iridium/internal/remote/wire"

	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
)

// FileRemote is the relay's record in a hold directory: which executor holds it, and where its
// offsets begin in the mirror.
const FileRemote = "remote.json"

type relayMeta struct {
	Machine string `json:"machine"`
	Hold    string `json:"hold"`
	// OutBase, InBase: out.log's and in.log's sizes when the holder started, the mirror's offsets
	// of the executor's 0.
	OutBase int64 `json:"outBase"`
	InBase  int64 `json:"inBase"`
	Started bool  `json:"started"` // the executor took hold.start
	// Stop: "stop" or "kill", asked while the executor was away.
	Stop string `json:"stop,omitempty"`
}

// Launcher is a platform.Launcher whose holders run at an executor. A unit's hold directory is
// the hub's Dir; the holder's spec is read from it (hold.Prepare wrote it).
type Launcher struct{ m *Machine }

// Launcher is the machine's platform.Launcher.
func (m *Machine) Launcher() platform.Launcher { return Launcher{m} }

func (l Launcher) Start(ctx context.Context, u platform.Unit) error {
	m := l.m
	dir := m.hub.Dir(u.Name)
	m.mu.Lock()
	_, exists := m.relays[u.Name]
	m.mu.Unlock()
	if exists {
		return fmt.Errorf("unit %s exists", u.Name)
	}
	rm := relayMeta{
		Machine: m.id, Hold: u.Name + "-" + strconv.FormatInt(time.Now().UnixMilli(), 36),
		OutBase: size(filepath.Join(dir, hold.FileOutLog)), InBase: size(filepath.Join(dir, hold.FileInLog)),
	}
	if err := writeJSON(filepath.Join(dir, FileRemote), rm); err != nil {
		return err
	}
	r, err := m.newRelay(u.Name, dir, rm)
	if err != nil {
		return err
	}
	r.run()
	return nil
}

func (l Launcher) Stop(ctx context.Context, name string) error {
	if r := l.relay(name); r != nil {
		r.stop("stop")
	}
	return nil
}

func (l Launcher) Kill(ctx context.Context, name string, sig string, main bool) error {
	if r := l.relay(name); r != nil {
		r.stop("kill")
	}
	return nil
}

// State: a holder runs while its relay does; one an earlier daemon relayed is taken up again.
// Active is "active", or "disconnected" while the executor is away.
func (l Launcher) State(ctx context.Context, name string) (platform.State, error) {
	r := l.relay(name)
	if r == nil {
		return platform.State{}, nil
	}
	if _, ok := l.m.Hello(); !ok {
		return platform.State{Exists: true, Active: "disconnected"}, nil
	}
	return platform.State{Exists: true, Active: "active"}, nil
}

func (l Launcher) List(ctx context.Context, prefix string) ([]string, error) {
	l.m.mu.Lock()
	defer l.m.mu.Unlock()
	var out []string
	for u := range l.m.relays {
		if strings.HasPrefix(u, prefix) {
			out = append(out, u)
		}
	}
	return out, nil
}

// relay is the running relay of unit, taking up one an earlier daemon left (its remote.json and
// no exit); nil if none runs.
func (l Launcher) relay(unit string) *relay {
	m := l.m
	m.mu.Lock()
	r := m.relays[unit]
	m.mu.Unlock()
	if r != nil {
		return r
	}
	dir := m.hub.Dir(unit)
	var rm relayMeta
	if readJSON(filepath.Join(dir, FileRemote), &rm) != nil || rm.Machine != m.id {
		return nil
	}
	if e, _ := hold.ReadExit(dir); e != nil {
		return nil
	}
	r, err := m.newRelay(unit, dir, rm)
	if err != nil {
		m.hub.logf("executor %s: taking up %s: %v", m.id, unit, err)
		return nil
	}
	r.run()
	return r
}

// relay mirrors one remote holder into its hold directory: the input pipe's lines go to the
// executor (in.log first, its offset the line's seq), its output is appended to out.log, its end
// written as the exit file.
type relay struct {
	m    *Machine
	unit string
	dir  string

	mu     sync.Mutex
	meta   relayMeta
	sent   int64      // in.log offset of the next line to send
	tailed *wire.Conn // the connection hold.tail was asked on
	ended  bool
	wake   chan struct{}
	quit   chan struct{}
	fifo   *os.File
	outMu  sync.Mutex // out.log appends, one at a time
}

func (m *Machine) newRelay(unit, dir string, rm relayMeta) (*relay, error) {
	fifo, err := os.OpenFile(filepath.Join(dir, hold.FileIn), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	r := &relay{m: m, unit: unit, dir: dir, meta: rm, sent: rm.InBase, wake: make(chan struct{}, 1), quit: make(chan struct{}), fifo: fifo}
	m.mu.Lock()
	if old := m.relays[unit]; old != nil {
		m.mu.Unlock()
		fifo.Close()
		return old, nil
	}
	m.relays[unit] = r
	m.mu.Unlock()
	return r, nil
}

func (r *relay) hold() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.meta.Hold
}

func (r *relay) logf(format string, a ...any) {
	r.m.hub.logf("executor %s: %s: "+format, append([]any{r.m.id, r.unit}, a...)...)
}

func (r *relay) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *relay) run() {
	go r.pump()
	go r.loop()
	if c, err := r.m.Conn(); err == nil {
		hi, _ := r.m.Hello()
		states := map[string]wire.HoldState{}
		for _, s := range hi.Holds {
			states[s.Hold] = s
		}
		r.connected(c, states)
	}
}

// pump records each line of the input pipe in in.log (out.log's size, a space, the line), as the
// local holder does, and wakes the sender.
func (r *relay) pump() {
	in, err := os.OpenFile(filepath.Join(r.dir, hold.FileInLog), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		r.logf("in.log: %v", err)
		return
	}
	defer in.Close()
	br := bufio.NewReaderSize(r.fifo, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return // the fifo closed: the relay ended
		}
		rec := append(strconv.AppendInt(nil, size(filepath.Join(r.dir, hold.FileOutLog)), 10), ' ')
		if _, err := in.Write(append(rec, line...)); err != nil {
			r.logf("writing in.log: %v", err)
		}
		r.poke()
	}
}

// loop sends what is due while the executor is connected: the start, a stop asked meanwhile, and
// the input lines not sent yet.
func (r *relay) loop() {
	for {
		change := r.m.changed()
		if c, err := r.m.Conn(); err == nil {
			if err := r.send(c); err != nil && !errors.Is(err, wire.ErrOffline) && !errors.Is(err, context.Canceled) {
				r.logf("%v", err)
			}
		}
		select {
		case <-r.quit:
			return
		case <-r.wake:
		case <-change:
		case <-time.After(5 * time.Second):
		}
	}
}

func (r *relay) send(c *wire.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.mu.Lock()
	rm := r.meta
	r.mu.Unlock()
	if !rm.Started {
		var spec hold.Spec
		if err := readJSON(filepath.Join(r.dir, hold.FileSpec), &spec); err != nil {
			r.end(hold.Exit{From: "agent", Code: ptr(hold.CodeCannotStart)}, "bad spec: "+err.Error())
			return nil
		}
		if rm.Stop != "" { // stopped before it ever ran
			r.end(hold.Exit{From: "agent", Code: ptr(0)}, "")
			return nil
		}
		err := c.Call(ctx, "hold.start", wire.StartParams{Hold: rm.Hold, Argv: spec.Argv, Env: spec.Env, Cwd: spec.Cwd, StopGraceMS: spec.StopGraceMS}, nil)
		if err != nil {
			var e *wire.Error
			if errors.As(err, &e) {
				r.end(hold.Exit{From: "agent", Code: ptr(hold.CodeCannotStart)}, e.Message)
				return nil
			}
			return err
		}
		r.mu.Lock()
		r.meta.Started = true
		rm = r.meta
		r.mu.Unlock()
		if err := writeJSON(filepath.Join(r.dir, FileRemote), rm); err != nil {
			return err
		}
	}
	r.mu.Lock()
	tail := r.tailed != c
	r.tailed = c
	r.mu.Unlock()
	if tail {
		from := size(filepath.Join(r.dir, hold.FileOutLog)) - rm.OutBase
		if err := c.Call(ctx, "hold.tail", wire.TailParams{Hold: rm.Hold, From: max(from, 0)}, nil); err != nil {
			r.mu.Lock()
			r.tailed = nil
			r.mu.Unlock()
			return err
		}
	}
	if err := r.sendLines(ctx, c); err != nil {
		return err
	}
	if rm.Stop != "" {
		if err := c.Call(ctx, "hold."+rm.Stop, wire.HoldParams{Hold: rm.Hold}, nil); err != nil && !wire.IsCode(err, wire.CodeNotFound) {
			return err
		}
		r.mu.Lock()
		r.meta.Stop = ""
		rm = r.meta
		r.mu.Unlock()
		return writeJSON(filepath.Join(r.dir, FileRemote), rm)
	}
	return nil
}

// sendLines sends in.log's records from r.sent: each line with its seq, the record's offset + 1.
func (r *relay) sendLines(ctx context.Context, c *wire.Conn) error {
	f, err := os.Open(filepath.Join(r.dir, hold.FileInLog))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r.mu.Lock()
	off := r.sent
	h := r.meta.Hold
	r.mu.Unlock()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		rec, err := br.ReadBytes('\n')
		if err != nil {
			return nil // a record not whole yet is sent next time
		}
		_, line, ok := bytes.Cut(rec, []byte{' '})
		if ok {
			line = bytes.TrimSuffix(line, []byte{'\n'})
			if err := c.Call(ctx, "hold.write", wire.WriteParams{Hold: h, Seq: off + 1, Line: string(line)}, nil); err != nil {
				return err
			}
		}
		off += int64(len(rec))
		r.mu.Lock()
		r.sent = off
		r.mu.Unlock()
	}
}

// connected: the executor (re)connected, holding states. A holder it started and no longer has
// is gone with it; else output is asked for again from the mirror's end, and input resent past
// the last line it took (its InSeq: that line's in.log offset + 1).
func (r *relay) connected(c *wire.Conn, states map[string]wire.HoldState) {
	r.mu.Lock()
	rm := r.meta
	st, ok := states[rm.Hold]
	r.tailed = nil
	r.mu.Unlock()
	if rm.Started && !ok {
		r.end(hold.Exit{From: "holder"}, "the executor no longer has the agent process")
		return
	}
	if ok && st.InSeq > 0 {
		r.skipPast(st.InSeq - 1)
	}
	r.poke()
}

// skipPast moves sent past the in.log record that starts at off.
func (r *relay) skipPast(off int64) {
	f, err := os.Open(filepath.Join(r.dir, hold.FileInLog))
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return
	}
	rec, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return
	}
	r.mu.Lock()
	r.sent = max(r.sent, off+int64(len(rec)))
	r.mu.Unlock()
}

// out appends what of e the mirror does not have yet. A gap (output lost on the way) asks for
// the rest again.
func (r *relay) out(e wire.OutEvent) {
	r.outMu.Lock()
	defer r.outMu.Unlock()
	r.mu.Lock()
	base := r.meta.OutBase
	r.mu.Unlock()
	path := filepath.Join(r.dir, hold.FileOutLog)
	have := size(path)
	at := base + e.Off
	data := []byte(e.Data)
	switch {
	case at > have:
		r.logf("output from %d, the mirror has %d: asking again", at, have)
		r.mu.Lock()
		r.tailed = nil
		r.mu.Unlock()
		r.poke()
		return
	case at+int64(len(data)) <= have:
		return
	}
	data = data[have-at:]
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		r.logf("out.log: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		r.logf("out.log: %v", err)
	}
}

// exit: the holder ended, its output all sent before.
func (r *relay) exit(e wire.ExitEvent) {
	r.end(hold.Exit(e.Exit), e.ErrTail)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = r.m.Call(ctx, "hold.forget", wire.HoldParams{Hold: e.Hold}, nil)
	}()
}

// end writes the exit file (and why, to err.log) and stops the relay.
func (r *relay) end(e hold.Exit, why string) {
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return
	}
	r.ended = true
	r.mu.Unlock()
	if why != "" {
		if f, err := os.OpenFile(filepath.Join(r.dir, hold.FileErrLog), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
			_, _ = f.WriteString(strings.TrimRight(why, "\n") + "\n")
			f.Close()
		}
	}
	b, _ := json.Marshal(e)
	if err := writeFile(filepath.Join(r.dir, hold.FileExit), append(b, '\n')); err != nil {
		r.logf("writing the exit file: %v", err)
	}
	r.m.mu.Lock()
	if r.m.relays[r.unit] == r {
		delete(r.m.relays, r.unit)
	}
	r.m.mu.Unlock()
	close(r.quit)
	r.fifo.Close()
}

// stop asks the executor to stop the holder (kind "stop" or "kill"), now or once it is back.
func (r *relay) stop(kind string) {
	r.mu.Lock()
	r.meta.Stop = kind
	rm := r.meta
	r.mu.Unlock()
	if err := writeJSON(filepath.Join(r.dir, FileRemote), rm); err != nil {
		r.logf("%v", err)
	}
	r.poke()
}

func size(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func ptr[T any](v T) *T { return &v }

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

func writeFile(path string, b []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
