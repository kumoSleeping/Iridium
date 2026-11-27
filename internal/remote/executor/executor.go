// Package executor is a reference executor (docs/exec.md) in Go: it connects out to an Iridium,
// starts the processes asked for and relays them, and serves files under the roots it allows.
// Tests use it as the machine at the other end; it is also what the dsh plugin follows.
package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/machineid"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Options configure an executor.
type Options struct {
	URL   string // ws(s)://…/v1/internal/exec
	Token string
	State string // its own directory: holders' files, scratch
	// Roots are the directories files may be read and written under; none: all but the denied.
	Roots []string
	// Deny are paths never served, besides its own TOKEN's, ~/.ssh and the agents' logins (denied).
	Deny   []string
	Agents []wire.AgentInfo
	// Shell: command lines run here, a person's on its page and its agents' (ir sh); else it
	// starts nothing but the agents (an argv beginning with one of theirs) (docs/exec.md). Roots
	// and Shell are where it begins: a grant the person here allows widens them while it runs.
	Shell bool
	// Ask asks the person at its terminal question, a grant from the Iridium's page: true, they
	// allowed it. nil: no one to ask, and grants of more are declined.
	// It gives up as ctx ends (no answer in time: no).
	Ask func(ctx context.Context, question string) bool
	// AskEach, when set, is asked in Ask's place, the request taken apart: each thing asked for
	// (Item) allowed or not on its own (the desktop app's panel). Its answer is one bool an item.
	AskEach func(ctx context.Context, by string, items []Item) []bool
	// Widened is told what it allows after a request widened it (a grant, an agent started that
	// was off), so that its owner's settings keep it.
	Widened func(shell bool, roots, off []string)
	// Denied tells the person at its terminal of something it turned down (Denial), each once a
	// minute at most.
	Denied func(Denial)
	// MachineID is what the hello says of this computer (wire.Hello.MachineID); "":
	// machineid.Hash().
	MachineID string
	// NoRun: it says no run in its caps, as an older executor (tests).
	NoRun   bool
	Env     []string // nil: os.Environ(); the hello gives it without secrets (helloEnv)
	Program string   // what the hello says it is; "": iridium-executor
	// IR: an ir program (this binary) for the agents started with their session's own TOKEN: linked
	// as <State>/bin/ir, first on their PATH (docs/exec.md "远端的 ir"). "": none.
	IR string
	// HTTPClient dials the Iridium; nil: http.DefaultClient.
	HTTPClient *http.Client
	// Connected is told each time the hello is answered: the terminal name the Iridium knows it by.
	Connected func(name string)
	// Replied is told the whole answer to each hello, after Connected (the Iridium's version).
	Replied func(wire.HelloReply)
	// Disconnected is told each time a connection ends, before it dials again.
	Disconnected func(err error)
	Logf         func(string, ...any)
}

// ErrRefused: the Iridium turned the TOKEN down (401): its Bot is gone, connecting again is no use.
var ErrRefused = errors.New("the Iridium refuses this TOKEN")

// Executor is one running executor.
type Executor struct {
	o Options

	mu    sync.Mutex
	conn  *wire.Conn
	holds map[string]*holder
	runs  map[string]*running // run's, by their ID
	// shell and roots are what it has now: Options' widened by grants.
	shell bool
	roots []string
	// off: the agents its owner turned off (by name, wire.AgentInfo.Agent): not offered in the
	// hello, not started (Set).
	off   map[string]bool
	told  map[Denial]time.Time // Denied, by when it was last told
	asked sync.Mutex           // one question at its terminal at a time
}

func New(o Options) (*Executor, error) {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Program == "" {
		o.Program = "iridium-executor"
	}
	if err := os.MkdirAll(filepath.Join(o.State, "holds"), 0o700); err != nil {
		return nil, err
	}
	if o.MachineID == "" {
		o.MachineID = machineid.Hash()
	}
	e := &Executor{o: o, holds: map[string]*holder{}, runs: map[string]*running{}, shell: o.Shell, roots: slices.Clone(o.Roots), told: map[Denial]time.Time{}}
	if err := e.load(); err != nil {
		return nil, err
	}
	return e, nil
}

// Run connects, and connects again whenever the connection ends, until ctx ends or the Iridium
// refuses the TOKEN (ErrRefused).
func (e *Executor) Run(ctx context.Context) error {
	if runtime.GOOS == "darwin" {
		go e.awake(ctx)
	}
	wait := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := e.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, ErrRefused) {
			return err
		}
		if time.Since(start) > time.Minute {
			wait = time.Second
		}
		e.o.Logf("executor: connection ended: %v; again in %v", err, wait)
		if e.o.Disconnected != nil {
			e.o.Disconnected(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(2*wait, 30*time.Second)
	}
	return ctx.Err()
}

// awake keeps a Mac from sleeping when idle while a process it started runs: caffeinate -i for a
// minute, again every 30s; once none runs, it sleeps as it would (a closed lid still does).
func (e *Executor) awake(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if e.running() {
			if c := exec.Command("caffeinate", "-i", "-t", "60"); c.Start() == nil {
				go func() { _ = c.Wait() }()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// running: a process it started has not ended.
func (e *Executor) running() bool {
	e.mu.Lock()
	hs := make([]*holder, 0, len(e.holds))
	for _, h := range e.holds {
		hs = append(hs, h)
	}
	e.mu.Unlock()
	for _, h := range hs {
		if h.state().Exit == nil {
			return true
		}
	}
	return false
}

func (e *Executor) session(ctx context.Context) error {
	ws, resp, err := websocket.Dial(ctx, e.o.URL, &websocket.DialOptions{HTTPClient: e.o.HTTPClient, HTTPHeader: http.Header{"Authorization": {"Bearer " + e.o.Token}}})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return ErrRefused
		}
		return err
	}
	c := wire.NewConn(ws)
	c.OnRequest = e.handle
	c.OnEvent = e.event
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	e.mu.Lock()
	e.conn = c
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.conn == c {
			e.conn = nil
		}
		for _, h := range e.holds {
			h.untail(c)
		}
		e.mu.Unlock()
		e.dropRuns(c)
	}()
	var named wire.HelloReply
	if err := c.Call(ctx, "hello", e.hello(), &named); err != nil {
		c.Close()
		<-done
		return fmt.Errorf("hello: %w", err)
	}
	if e.o.Connected != nil {
		e.o.Connected(named.Name)
	}
	if e.o.Replied != nil {
		e.o.Replied(named)
	}
	return <-done
}

// Close ends every process it runs, as its own exit would (their pipes go with it).
func (e *Executor) Close() {
	e.mu.Lock()
	hs := make([]*holder, 0, len(e.holds))
	for _, h := range e.holds {
		hs = append(hs, h)
	}
	var cmds []*exec.Cmd
	for _, r := range e.runs {
		if r.cmd != nil {
			cmds = append(cmds, r.cmd)
		}
	}
	e.mu.Unlock()
	for _, h := range hs {
		h.stop(true)
	}
	for _, cmd := range cmds {
		killTree(cmd, true)
	}
	// Each killed hold's exit is written as its process is reaped: wait for that, so that
	// nothing is written into the state folder after Close.
	end := time.After(5 * time.Second)
	for _, h := range hs {
		h.mu.Lock()
		started := h.cmd != nil
		h.mu.Unlock()
		if !started {
			continue
		}
		select {
		case <-h.ended:
		case <-end:
			return
		}
	}
}

// Drop ends the connection now (it connects again): as when the network goes.
func (e *Executor) Drop() {
	e.mu.Lock()
	c := e.conn
	e.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (e *Executor) hello() wire.Hello {
	home, _ := os.UserHomeDir()
	caps := []string{wire.CapRun, wire.CapGrant}
	if e.o.NoRun {
		caps = []string{wire.CapGrant}
	}
	e.mu.Lock()
	shell, roots := e.shell, slices.Clone(e.roots)
	var agents []wire.AgentInfo
	for _, a := range e.o.Agents {
		if !e.off[a.Agent] {
			agents = append(agents, a)
		}
	}
	var holds []wire.HoldState
	for _, h := range e.holds {
		holds = append(holds, h.state())
	}
	e.mu.Unlock()
	return wire.Hello{
		Protocol: wire.Protocol, Program: e.o.Program, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Home: home, Sep: string(filepath.Separator), Scratch: filepath.Join(e.o.State, "scratch"),
		Env: helloEnv(e.o.Env), Agents: agents, Holds: holds, IRClient: e.o.IR != "",
		// The older two said too, for an older Iridium.
		Shell: &shell, NoShell: !shell, AgentShell: shell, Roots: roots,
		MachineID: e.o.MachineID, Caps: caps,
	}
}

// withIR is env for an agent started with its session's own TOKEN: ir first on its PATH, in its
// holder's bin/ (dir), a script with the address (this connection's, unless env has one) and the
// TOKEN written in, as an agent's commands need not get its environment, which runs this binary
// by the name ir (bin/.ir/ir, a link).
func (e *Executor) withIR(env []string, dir string) []string {
	get := func(k string) (string, int) {
		for i, kv := range env {
			if name, v, _ := strings.Cut(kv, "="); strings.EqualFold(name, k) {
				return v, i
			}
		}
		return "", -1
	}
	tok, _ := get("IRIDIUM_TOKEN")
	if e.o.IR == "" || tok == "" {
		return env
	}
	env = slices.Clone(env)
	u, i := get("IRIDIUM_URL")
	if u == "" {
		if i >= 0 {
			env = slices.Delete(env, i, i+1)
		}
		base := strings.Replace(strings.Replace(e.o.URL, "wss://", "https://", 1), "ws://", "http://", 1)
		if p, err := url.Parse(base); err == nil {
			u = p.Scheme + "://" + p.Host
			env = append(env, "IRIDIUM_URL="+u)
		}
	}
	bin := filepath.Join(dir, "bin")
	link := filepath.Join(bin, ".ir", "ir")
	script := "#!/bin/sh\nIRIDIUM_URL=" + shQuote(u) + " IRIDIUM_TOKEN=" + shQuote(tok) + " exec " + shQuote(link) + " \"$@\"\n"
	err := os.MkdirAll(filepath.Dir(link), 0o700)
	if err == nil {
		os.Remove(link)
		err = os.Symlink(e.o.IR, link)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(bin, "ir"), []byte(script), 0o700)
	}
	if err != nil {
		e.o.Logf("executor: ir for its agents: %v", err)
		return env
	}
	if p, i := get("PATH"); i >= 0 {
		env[i] = env[i][:strings.Index(env[i], "=")+1] + bin + string(filepath.ListSeparator) + p
	} else {
		env = append(env, "PATH="+bin)
	}
	return env
}

// shQuote is s as one word for sh.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// agentArgv: argv begins with one of the agents' own that is on.
func (e *Executor) agentArgv(argv []string) bool {
	a := e.agentOf(argv)
	return a != "" && !e.isOff(a)
}

// agentOf is the agent whose own argv begins argv; "": none.
func (e *Executor) agentOf(argv []string) string {
	for _, a := range e.o.Agents {
		if len(a.Argv) > 0 && len(argv) >= len(a.Argv) && slices.Equal(argv[:len(a.Argv)], a.Argv) {
			return a.Agent
		}
	}
	return ""
}

func (e *Executor) isOff(agent string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.off[agent]
}

// Set is what its owner allows now, at this computer (the desktop app's switches, kumo,
// 2026-11-20): command lines, the folders served (none: all), the agents turned off. At once for
// what starts from now on; what runs keeps running. The connection is made again, so that its
// Iridium hears the hello with them.
func (e *Executor) Set(shell bool, roots []string, off []string) {
	m := map[string]bool{}
	for _, a := range off {
		m[a] = true
	}
	e.mu.Lock()
	same := e.shell == shell && slices.Equal(e.roots, roots) && maps.Equal(e.off, m)
	e.shell, e.roots, e.off = shell, slices.Clone(roots), m
	e.mu.Unlock()
	if !same {
		e.o.Logf("executor: set: shell %v, roots %s, agents off %s", shell, strings.Join(roots, ", "), strings.Join(off, ", "))
		e.Drop()
	}
}

func bad(format string, a ...any) error {
	return &wire.Error{Code: wire.CodeBadRequest, Message: fmt.Sprintf(format, a...)}
}

func (e *Executor) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var p struct {
		Hold, Path, Line, Cwd, SHA256 string
		Seq, From, Offset, Length     int64
		Head                          int
		Argv, Env, Scope              []string
		StopGraceMS                   int `json:"stopGraceMs"`
		Data                          []byte
		Replace                       bool
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, bad("%v", err)
	}
	// ".browser" calls enforce the web file browser's dot-name rules on top of scope; an older
	// executor rejects the unknown method so those rules are never silently skipped.
	read := fileRead{scope: p.Scope, browser: strings.HasSuffix(method, ".browser")}
	switch method {
	case "run":
		var rp wire.RunParams
		if err := json.Unmarshal(params, &rp); err != nil {
			return nil, bad("%v", err)
		}
		e.mu.Lock()
		c := e.conn
		e.mu.Unlock()
		return e.startRun(c, rp)
	case "run.stop":
		var rp wire.RunStopParams
		if err := json.Unmarshal(params, &rp); err != nil {
			return nil, bad("%v", err)
		}
		return e.stopRun(rp.Run)
	case "grant":
		var gp wire.GrantParams
		if err := json.Unmarshal(params, &gp); err != nil {
			return nil, bad("%v", err)
		}
		return e.grant(ctx, gp)
	case "hold.start":
		return e.start(p.Hold, spec{Argv: p.Argv, Env: p.Env, Cwd: p.Cwd, StopGraceMS: p.StopGraceMS})
	case "hold.write", "hold.tail", "hold.stop", "hold.kill", "hold.forget":
		h := e.holder(p.Hold)
		if h == nil {
			return nil, &wire.Error{Code: wire.CodeNotFound, Message: p.Hold}
		}
		switch method {
		case "hold.write":
			return h.write(p.Seq, p.Line)
		case "hold.tail":
			e.mu.Lock()
			c := e.conn
			e.mu.Unlock()
			h.tail(c, p.From)
		case "hold.stop":
			h.stop(false)
		case "hold.kill":
			h.stop(true)
		case "hold.forget":
			if err := e.forget(h); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	case "fs.stat", "fs.stat.browser":
		return e.stat(p.Path, p.Head, read)
	case "fs.list":
		return e.list(p.Path, read)
	case "fs.list.browser":
		return e.browse(p.Path, p.Scope)
	case "fs.read", "fs.read.browser":
		return e.read(p.Path, p.Offset, p.Length, read)
	case "fs.write":
		return struct{}{}, e.write(p.Path, p.Data, p.SHA256, p.Replace)
	case "fs.mkdir":
		path, err := e.allowed(p.Path)
		if err != nil {
			return nil, err
		}
		return struct{}{}, os.MkdirAll(path, 0o755)
	}
	return nil, &wire.Error{Code: wire.CodeUnsupported, Message: method}
}

// ---- holders ----

// holder is one process and its files: out.log (stdout), err.log, in.seq (the last input's seq),
// exit, spec.json.
type holder struct {
	e   *Executor
	id  string
	dir string

	mu     sync.Mutex
	stdin  io.WriteCloser
	cmd    *exec.Cmd
	inSeq  int64
	exit   *wire.Exit
	tailOn *wire.Conn
	tailAt int64
	tailN  int // the push running now; an older one stops
	kick   chan struct{}
	ended  chan struct{}
	grace  time.Duration
}

func (e *Executor) holder(id string) *holder {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.holds[id]
}

// load takes up the holders of an earlier run: their processes went with it (their pipes did),
// so each has ended unless it wrote its exit already.
func (e *Executor) load() error {
	ents, err := os.ReadDir(filepath.Join(e.o.State, "holds"))
	if err != nil {
		return err
	}
	for _, d := range ents {
		h := e.newHolder(d.Name())
		if b, err := os.ReadFile(filepath.Join(h.dir, "in.seq")); err == nil {
			h.inSeq, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		}
		if x, err := readExit(h.dir); err == nil {
			h.exit = x
		} else {
			h.exit = &wire.Exit{From: "holder"}
			b, _ := json.Marshal(h.exit)
			_ = os.WriteFile(filepath.Join(h.dir, fileExit), b, 0o600)
		}
		close(h.ended)
		e.holds[h.id] = h
	}
	return nil
}

func (e *Executor) newHolder(id string) *holder {
	return &holder{e: e, id: id, dir: filepath.Join(e.o.State, "holds", id), kick: make(chan struct{}, 1), ended: make(chan struct{})}
}

func (e *Executor) start(id string, spec spec) (any, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) || len(spec.Argv) == 0 {
		return nil, bad("bad hold.start")
	}
	if a := e.agentOf(spec.Argv); a != "" && e.isOff(a) {
		// Turned off here: the Iridium may only ask; the person here says.
		if yes := e.askEach(context.Background(), "", []Item{{Kind: ItemAgent, Value: a}}); !yes[0] {
			return nil, &wire.Error{Code: wire.CodeDenied, Message: a + " does not start here: its owner turned it off"}
		}
		e.mu.Lock()
		delete(e.off, a)
		e.mu.Unlock()
		e.widened()
	}
	if !e.shellOn() && !e.agentArgv(spec.Argv) {
		e.deny(Denial{What: DeniedShell})
		return nil, &wire.Error{Code: wire.CodeDenied, Message: "command lines do not run here (only its agents start): its owner turned them off"}
	}
	e.mu.Lock()
	if h := e.holds[id]; h != nil {
		e.mu.Unlock()
		return map[string]int{"pid": h.pid()}, nil
	}
	h := e.newHolder(id)
	e.holds[id] = h
	e.mu.Unlock()
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return nil, err
	}
	spec.Env = e.withIR(spec.Env, h.dir)
	out, err := os.OpenFile(filepath.Join(h.dir, fileOut), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	errLog, err := os.OpenFile(filepath.Join(h.dir, fileErr), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		out.Close()
		return nil, err
	}
	h.grace = defaultGrace
	if spec.StopGraceMS > 0 {
		h.grace = time.Duration(spec.StopGraceMS) * time.Millisecond
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	spec.Env = startEnv(spec.Env, e.o.Env)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = spec.Env, spec.Cwd, out, errLog
	if p := lookPath(spec.Argv[0], spec.Env); p != "" {
		cmd.Path, cmd.Err = p, nil
	}
	setGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		fmt.Fprintf(errLog, "cannot start %s: %v\n", spec.Argv[0], err)
		out.Close()
		errLog.Close()
		h.finish(wire.Exit{From: "agent", Code: ptr(codeCannotStart)})
		return map[string]int{"pid": 0}, nil
	}
	h.mu.Lock()
	h.cmd, h.stdin = cmd, stdin
	h.mu.Unlock()
	go func() {
		err := cmd.Wait()
		out.Close()
		errLog.Close()
		h.finish(exitOf(cmd.ProcessState, err))
	}()
	return map[string]int{"pid": cmd.Process.Pid}, nil
}

func (h *holder) pid() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *holder) state() wire.HoldState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return wire.HoldState{Hold: h.id, OutSize: fileSize(filepath.Join(h.dir, fileOut)), InSeq: h.inSeq, Exit: h.exit}
}

func (h *holder) finish(x wire.Exit) {
	b, _ := json.Marshal(x)
	_ = os.WriteFile(filepath.Join(h.dir, fileExit), b, 0o600)
	h.mu.Lock()
	h.exit = &x
	h.mu.Unlock()
	close(h.ended)
	h.poke()
}

func (h *holder) poke() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func (h *holder) write(seq int64, line string) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if seq <= h.inSeq {
		return map[string]int64{"outSize": fileSize(filepath.Join(h.dir, fileOut))}, nil
	}
	if h.stdin == nil || h.exit != nil {
		return nil, &wire.Error{Code: wire.CodeFailed, Message: "the process is not running"}
	}
	if _, err := io.WriteString(h.stdin, line+"\n"); err != nil {
		return nil, err
	}
	h.inSeq = seq
	_ = os.WriteFile(filepath.Join(h.dir, "in.seq"), []byte(strconv.FormatInt(seq, 10)), 0o600)
	return map[string]int64{"outSize": fileSize(filepath.Join(h.dir, fileOut))}, nil
}

// tail pushes out.log from `from` on c, whole lines, as it grows; then, once the process has
// ended and all is pushed, its exit.
func (h *holder) tail(c *wire.Conn, from int64) {
	h.mu.Lock()
	h.tailOn, h.tailAt = c, from
	h.tailN++
	n := h.tailN
	h.mu.Unlock()
	go h.push(c, n)
	h.poke()
}

func (h *holder) untail(c *wire.Conn) {
	h.mu.Lock()
	if h.tailOn == c {
		h.tailOn = nil
	}
	h.mu.Unlock()
	h.poke()
}

func (h *holder) push(c *wire.Conn, n int) {
	ctx := context.Background()
	f, err := os.Open(filepath.Join(h.dir, fileOut))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if f != nil {
		defer f.Close()
	}
	for {
		h.mu.Lock()
		if h.tailOn != c || h.tailN != n {
			h.mu.Unlock()
			return
		}
		at := h.tailAt
		h.mu.Unlock()
		ended := false
		select {
		case <-h.ended:
			ended = true
		default:
		}
		if f == nil {
			f, _ = os.Open(filepath.Join(h.dir, fileOut))
		}
		var data []byte
		if f != nil {
			buf := make([]byte, 1<<20)
			n, _ := f.ReadAt(buf, at)
			data = buf[:n]
			if i := strings.LastIndexByte(string(data), '\n'); i >= 0 {
				data = data[:i+1]
			} else if !ended || n == 0 {
				data = nil
			}
		}
		if len(data) > 0 {
			if err := c.Notify(ctx, "out", map[string]any{"hold": h.id, "off": at, "data": string(data)}); err != nil {
				return
			}
			h.mu.Lock()
			if h.tailN == n {
				h.tailAt = at + int64(len(data))
			}
			h.mu.Unlock()
			continue
		}
		if ended {
			h.mu.Lock()
			x := *h.exit
			h.mu.Unlock()
			_ = c.Notify(ctx, "exit", map[string]any{"hold": h.id, "from": x.From, "code": x.Code, "signal": x.Signal, "errTail": errTail(h.dir)})
			return
		}
		select {
		case <-h.kick:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (h *holder) stop(kill bool) {
	h.mu.Lock()
	cmd, stdin, grace := h.cmd, h.stdin, h.grace
	h.mu.Unlock()
	if cmd == nil {
		return
	}
	if kill {
		killTree(cmd, true)
		return
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	go func() {
		select {
		case <-h.ended:
			return
		case <-time.After(grace):
		}
		killTree(cmd, false)
		select {
		case <-h.ended:
		case <-time.After(grace):
			killTree(cmd, true)
		}
	}()
}

func (e *Executor) forget(h *holder) error {
	select {
	case <-h.ended:
	default:
		return &wire.Error{Code: wire.CodeFailed, Message: "still running"}
	}
	e.mu.Lock()
	delete(e.holds, h.id)
	e.mu.Unlock()
	return os.RemoveAll(h.dir)
}

func errTail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, fileErr))
	if err != nil {
		return ""
	}
	if len(b) > 2048 {
		b = b[len(b)-2048:]
	}
	return string(b)
}

// ---- files ----

// fileRead narrows one file read call (fs.*.browser): scope bounds the resolved path to a login's
// roots, and browser enforces the web file browser's dot-name filter.
type fileRead struct {
	scope   []string
	browser bool
}

// fileDenial records whether a denied path can lead to open roots, and what to report.
type fileDenial struct {
	err          *wire.Error
	outsideRoots bool
	notice       string // Denial.What, or empty if no notice is due
}

func (d *fileDenial) Error() string { return d.err.Error() }
func (d *fileDenial) Unwrap() error { return d.err }

// reportFileDenial tells the terminal only about an actual request refused by this computer.
func (e *Executor) reportFileDenial(path string, err error) error {
	var d *fileDenial
	if errors.As(err, &d) && d.notice != "" {
		e.deny(Denial{What: d.notice, Path: filepath.Clean(path)})
	}
	return err
}

// allowed is path cleaned, its links resolved, if under a root and not denied.
func (e *Executor) allowed(path string) (string, error) {
	p, err := e.checkAllowed(path, nil)
	return p, e.reportFileDenial(path, err)
}

// readable is allowed for reading: also under the folders its agents are installed in (an
// agent's Scope: dsh's packages, which the Iridium reads its presets from to start it). They
// are the programs this computer offered to run, not a person's files, and often outside the
// folders it serves (kumo, 2026-11-18: dsh under /opt/homebrew would not start, home alone
// served). When read.scope is given, the resolved path must also lie inside one of those roots;
// when read.browser is set, dot-prefixed paths are denied.
func (e *Executor) readable(path string, read fileRead) (string, error) {
	p, err := e.checkReadable(path, read)
	return p, e.reportFileDenial(path, err)
}

// checkReadable makes the same check without notifying the terminal.
func (e *Executor) checkReadable(path string, read fileRead) (string, error) {
	var more []string
	for _, a := range e.o.Agents {
		if a.Scope != "" {
			more = append(more, a.Scope)
		}
	}
	real, err := e.checkAllowed(path, more)
	if err != nil {
		return "", err
	}
	if len(read.scope) > 0 && !pathsafe.InRoots(real, read.scope) {
		return "", &fileDenial{
			err:          &wire.Error{Code: wire.CodeDenied, Message: path + " is out of this login's scope"},
			outsideRoots: true,
		}
	}
	if read.browser {
		if err := pathsafe.CheckBrowser(path, real); err != nil {
			return "", &wire.Error{Code: wire.CodeDenied, Message: err.Error()}
		}
	}
	return real, nil
}

// checkAllowed checks the computer's roots and the extra roots in more, without notifying.
func (e *Executor) checkAllowed(path string, more []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", bad("not absolute: %s", path)
	}
	path = filepath.Clean(path)
	real := path
	// The deepest part that exists, resolved; the rest as given.
	for dir, rest := path, ""; ; {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			real = filepath.Join(r, rest)
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
	under := func(root string) bool {
		rel, err := filepath.Rel(root, real)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	for _, d := range e.denied() {
		if under(d) {
			return "", &fileDenial{
				err:    &wire.Error{Code: wire.CodeDenied, Message: path + ": never served (keys and logins)"},
				notice: DeniedSecret,
			}
		}
	}
	roots := e.Roots()
	if len(roots) == 0 {
		return real, nil
	}
	for _, r := range append(roots, more...) {
		if rr, err := filepath.EvalSymlinks(r); err == nil && under(rr) {
			return real, nil
		}
	}
	msg := fmt.Sprintf("%s: not under the folders this computer serves (%s); a person may allow more on the Iridium's page, confirmed at that computer", path, strings.Join(roots, ", "))
	return "", &fileDenial{
		err:          &wire.Error{Code: wire.CodeDenied, Message: msg},
		outsideRoots: true,
		notice:       DeniedPath,
	}
}

func typeOf(m fs.FileMode) string {
	switch {
	case m.IsRegular():
		return "file"
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "link"
	}
	return "other"
}

func notFound(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &wire.Error{Code: wire.CodeNotFound, Message: err.Error()}
	}
	return err
}

func (e *Executor) stat(path string, head int, read fileRead) (any, error) {
	p, err := e.readable(path, read)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, notFound(err)
	}
	out := wire.Stat{Size: st.Size(), Mtime: st.ModTime().UnixMilli(), Type: typeOf(st.Mode())}
	if head > 0 && st.Mode().IsRegular() {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		sum := sha256.New()
		_, _ = io.CopyN(sum, f, int64(head))
		out.Head = hex.EncodeToString(sum.Sum(nil))
	}
	return out, nil
}

func (e *Executor) list(path string, read fileRead) (any, error) {
	p, err := e.readable(path, read)
	if err != nil {
		return nil, err
	}
	entries, err := listEntries(p, read.browser)
	if err != nil {
		return nil, err
	}
	return wire.ListReply{Entries: entries}, nil
}

// browse serves fs.list.browser: it lists an open directory (Open true), or for an ancestor
// outside the open roots returns only the next path components leading to readable roots
// (Open false) without scanning the ancestor or reporting probe denials.
func (e *Executor) browse(path string, scope []string) (wire.BrowseReply, error) {
	read := fileRead{scope: scope, browser: true}
	p, err := e.checkReadable(path, read)
	if err == nil {
		entries, err := listEntries(p, true)
		if err != nil {
			return wire.BrowseReply{}, err
		}
		return wire.BrowseReply{Entries: entries, Open: true}, nil
	}
	var d *fileDenial
	if errors.As(err, &d) && d.outsideRoots {
		// The narrower root can come from the login or this computer. Check each
		// against both scopes without reporting denials for these internal probes.
		children := pathsafe.BrowserChildren(path, slices.Concat(scope, e.Roots()), func(root string) (string, error) {
			return e.checkReadable(root, read)
		})
		if len(children) > 0 {
			out := wire.BrowseReply{Entries: make([]wire.Entry, 0, len(children))}
			for _, name := range children {
				out.Entries = append(out.Entries, wire.Entry{Name: name, Type: "dir"})
			}
			return out, nil
		}
	}
	return wire.BrowseReply{}, e.reportFileDenial(path, err)
}

func listEntries(p string, browser bool) ([]wire.Entry, error) {
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, notFound(err)
	}
	out := []wire.Entry{}
	for _, d := range ents {
		if browser && pathsafe.BrowserHidden(d.Name()) {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		out = append(out, wire.Entry{Name: d.Name(), Size: info.Size(), Mtime: info.ModTime().UnixMilli(), Type: typeOf(info.Mode())})
	}
	return out, nil
}

func (e *Executor) read(path string, off, n int64, read fileRead) (any, error) {
	p, err := e.readable(path, read)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, notFound(err)
	}
	defer f.Close()
	buf := make([]byte, min(max(n, 0), wire.MaxRead))
	k, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return map[string][]byte{"data": buf[:k]}, nil
}

func (e *Executor) write(path string, data []byte, sum string, replace bool) error {
	p, err := e.allowed(path)
	if err != nil {
		return err
	}
	if s := sha256.Sum256(data); hex.EncodeToString(s[:]) != sum {
		return bad("sha256 does not match")
	}
	if _, err := os.Stat(p); err == nil && !replace {
		return &wire.Error{Code: wire.CodeExists, Message: path}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ---- helpers ----

// A holder's files, as a local holder's (internal/hold).
const (
	fileOut  = "out.log"
	fileErr  = "err.log"
	fileExit = "exit"
)

const (
	defaultGrace    = 5 * time.Second
	codeCannotStart = 127
)

type spec struct {
	Argv        []string
	Env         []string
	Cwd         string
	StopGraceMS int
}

func readExit(dir string) (*wire.Exit, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileExit))
	if err != nil {
		return nil, err
	}
	var x wire.Exit
	return &x, json.Unmarshal(b, &x)
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func ptr[T any](v T) *T { return &v }

// lookPath finds a bare program on env's PATH (not the executor's own), "" if not there or not
// bare.
func lookPath(name string, env []string) string {
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	path := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			path = v
		}
	}
	for _, d := range filepath.SplitList(path) {
		for _, ext := range exts() {
			p := filepath.Join(d, name+ext)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func exitOf(ps *os.ProcessState, err error) wire.Exit {
	if ps == nil {
		return wire.Exit{From: "holder"}
	}
	if sig := signalOf(ps); sig != "" {
		return wire.Exit{From: "agent", Signal: &sig}
	}
	return wire.Exit{From: "agent", Code: ptr(ps.ExitCode())}
}

// Link is the connection to the Iridium as it is now: up or not, its last round trip (0 before
// the first ping), for the desktop app's list (kumo, 2026-11-22: connection and its steadiness
// are what matter of a join).
func (e *Executor) Link() (up bool, rtt time.Duration) {
	e.mu.Lock()
	c := e.conn
	e.mu.Unlock()
	if c == nil {
		return false, 0
	}
	return true, c.RTT()
}
