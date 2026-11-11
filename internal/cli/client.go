package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/place"
)

// env is one run of ir: where it writes, and its way to the daemon.
type env struct {
	out, err io.Writer
	sock     string
	hc       *http.Client
	tty      bool // stderr is a terminal: progress may be drawn there
	// at: ir runs for a session at another machine (POST /v1/internal/cli, remote.go); nil on the socket.
	at *At
	// after: where the page ended (its last block's at) when the message waited on was sent ("":
	// not known), for a transcript that does not keep its id (dsh gives each prompt its own).
	after string
}

func newEnv(stdout, stderr io.Writer) *env {
	sock := os.Getenv("IRIDIUM_SOCKET")
	if sock == "" {
		path := os.Getenv("IRIDIUM_CONFIG")
		if path == "" {
			path = config.DefaultPath()
		}
		cfg, err := config.Load(path)
		if err != nil {
			cfg = config.Default()
		}
		sock = cfg.SocketPath()
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	return &env{out: stdout, err: stderr, sock: sock, hc: hc, tty: isTTY(stderr)}
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// caller is the session ir runs in, if it runs in one: Iridium's own hosted sessions set
// IRIDIUM_SESSION; the agents set theirs (agents.SessionVars).
func caller() string {
	for _, k := range agents.SessionVars() {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// caller is the session ir runs for: the one at another machine it was sent by, else caller().
func (e *env) caller() string {
	if e.at != nil {
		return e.at.Session
	}
	return caller()
}

// apiError is the daemon's answer to a request that failed.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// request is a request to the daemon; hdr, headers' names and values in turn.
func (e *env) request(method, path string, body any, hdr ...string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://ir"+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	// The session ir runs in: what an agent does through it counts to that session's terminal.
	if c := e.caller(); c != "" && e.at == nil {
		req.Header.Set(headerSession, c)
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		if e.at != nil {
			return nil, err
		}
		return nil, fmt.Errorf("iridium is not running (no answer on %s)", e.sock)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		var ae api.Error
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, &ae) != nil || ae.Text() == "" {
			ae.Message = strings.TrimSpace(string(b))
		}
		return nil, &apiError{status: resp.StatusCode, msg: ae.Text()}
	}
	return resp, nil
}

// do is a request whose answer is JSON, decoded into out unless it is nil.
func (e *env) do(method, path string, body, out any) error {
	resp, err := e.request(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e *env) fail(err error) int {
	fmt.Fprintf(e.err, "ir: %v\n", err)
	return 1
}

func (e *env) failf(format string, a ...any) int {
	fmt.Fprintf(e.err, "ir: "+format+"\n", a...)
	return 1
}

// ---- arguments ----

type opts struct {
	wait    bool
	steer   bool
	queue   bool // ir send --queue: wait for the running turn to end instead of joining it
	all     bool // ir ls, find --all: the archive and the subagents done too
	here    bool // ir ls --here: the caller's group only
	full    bool // ir read --full: the whole page as written
	n       int  // ir read -n: how many messages
	model   string
	approve string
	machine string // ir new --machine (hidden, the older way): the app whose computer it runs at
	group   string // ir new --workspace: the group it goes in
	attach  []string
	pos     []string
}

// parse takes the flags anywhere among the arguments; after "--" everything is positional.
func parse(args []string, allowed string) (opts, error) {
	var o opts
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			o.pos = append(o.pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			o.pos = append(o.pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name == "a" {
			name = "attach"
		}
		if !strings.Contains(" "+allowed+" ", " "+name+" ") {
			return o, fmt.Errorf("no flag %s here", a)
		}
		if name == "wait" || name == "steer" || name == "queue" || name == "all" || name == "full" || name == "here" {
			if hasVal {
				return o, fmt.Errorf("%s takes no value", a)
			}
			o.wait = o.wait || name == "wait"
			o.steer = o.steer || name == "steer"
			o.queue = o.queue || name == "queue"
			o.all = o.all || name == "all"
			o.full = o.full || name == "full"
			o.here = o.here || name == "here"
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a value", a)
			}
			i++
			val = args[i]
		}
		switch name {
		case "model":
			o.model = val
		case "approve":
			o.approve = val
		case "machine":
			o.machine = val
		case "workspace":
			o.group = val
		case "attach":
			o.attach = append(o.attach, val)
		case "n":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return o, fmt.Errorf("-n takes a count, not %q", val)
			}
			o.n = n
		}
	}
	return o, nil
}

// errPeer: a place at another app's computer (<name>:<path>, internal/place) where only one here
// is taken.
type errPeer string

func (e errPeer) Error() string { return "a place at " + string(e) + ", not here" }

// localPath is arg as an absolute path here (host:… too, the host's own name); a place at
// another app's computer is an errPeer.
func localPath(arg string) (string, error) {
	if name, path, ok := place.Split(arg); ok {
		if !place.IsHost(name) {
			return "", errPeer(name)
		}
		arg = cmp.Or(path, "~")
	}
	if arg == "~" || strings.HasPrefix(arg, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		arg = filepath.Join(home, strings.TrimPrefix(arg, "~"))
	}
	return filepath.Abs(arg)
}

// pathArg is a path ir cp and files take: <app>:<path>, an app's computer by its name and its own
// path there (as given; "" its home), or one here (host:… too), made absolute.
func pathArg(arg string) (machine, path string, err error) {
	path, err = localPath(arg)
	var peer errPeer
	if errors.As(err, &peer) {
		_, path, _ = place.Split(arg)
		return string(peer), path, nil
	}
	return "", path, err
}

// resolve is the full id of the session a unique prefix names.
func (e *env) resolve(arg string) (string, error) {
	if arg == "" {
		return "", errors.New("no session id")
	}
	var list []api.Session
	if err := e.do("GET", "/v1/internal/sessions", nil, &list); err != nil {
		return "", err
	}
	var hits []string
	for _, s := range list {
		if s.ID == arg {
			return s.ID, nil
		}
		if strings.HasPrefix(s.ID, arg) {
			hits = append(hits, s.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no session %s", arg)
	case 1:
		return hits[0], nil
	}
	return "", fmt.Errorf("%s is the start of %d sessions' ids; give more of it", arg, len(hits))
}

// short is the shortest prefix of id, at least 8 characters, no other id of ids shares.
func short(id string, ids []string) string {
	for n := 8; n < len(id); n++ {
		unique := true
		for _, o := range ids {
			if o != id && strings.HasPrefix(o, id[:n]) {
				unique = false
				break
			}
		}
		if unique {
			return id[:n]
		}
	}
	return id
}

func (e *env) ids() []string {
	var list []api.Session
	_ = e.do("GET", "/v1/internal/sessions", nil, &list)
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}

// ---- progress, on a terminal only ----

type progress struct {
	e     *env
	start time.Time
	drawn bool
}

func (e *env) progress() *progress { return &progress{e: e, start: time.Now()} }

func (p *progress) status(what string) {
	if !p.e.tty {
		return
	}
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	f := frames[int(time.Since(p.start)/(100*time.Millisecond))%len(frames)]
	fmt.Fprintf(p.e.err, "\r\033[K%c %s %ds", f, what, int(time.Since(p.start).Seconds()))
	p.drawn = true
}

func (p *progress) bar(done, size int64) {
	if !p.e.tty || size <= 0 {
		return
	}
	const width = 30
	n := int(done * width / size)
	fmt.Fprintf(p.e.err, "\r\033[K[%s%s] %3d%% %s / %s", strings.Repeat("█", n), strings.Repeat("·", width-n), done*100/size, bytesStr(done), bytesStr(size))
	p.drawn = true
}

func (p *progress) clear() {
	if p.drawn {
		fmt.Fprint(p.e.err, "\r\033[K")
		p.drawn = false
	}
}

func bytesStr(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func query(k, v string) string { return "?" + url.Values{k: {v}}.Encode() }
