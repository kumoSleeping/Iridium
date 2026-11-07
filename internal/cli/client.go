package cli

import (
	"bytes"
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
)

// env is one run of ir: where it writes, and its way to the daemon.
type env struct {
	out, err io.Writer
	sock     string
	hc       *http.Client
	tty      bool // stderr is a terminal: progress may be drawn there
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
// IRIDIUM_SESSION; Claude and Codex set theirs.
func caller() string {
	for _, k := range []string{"IRIDIUM_SESSION", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "PI_SESSION_ID"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// apiError is the daemon's answer to a request that failed.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func (e *env) request(method, path string, body any) (*http.Response, error) {
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
	// The session ir runs in: what an agent does through it counts to that session's terminal.
	if c := caller(); c != "" {
		req.Header.Set(headerSession, c)
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("iridium is not running (no answer on %s)", e.sock)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		var ae api.Error
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, &ae) != nil || ae.Error == "" {
			ae.Error = strings.TrimSpace(string(b))
		}
		return nil, &apiError{status: resp.StatusCode, msg: ae.Error}
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
	full    bool // ir read --full: the whole page as written
	n       int  // ir read -n: how many messages
	model   string
	approve string
	name    string // ir ws new --name
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
		if name == "wait" || name == "steer" || name == "queue" || name == "all" || name == "full" {
			if hasVal {
				return o, fmt.Errorf("%s takes no value", a)
			}
			o.wait = o.wait || name == "wait"
			o.steer = o.steer || name == "steer"
			o.queue = o.queue || name == "queue"
			o.all = o.all || name == "all"
			o.full = o.full || name == "full"
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
		case "name":
			o.name = val
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

var peerPath = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]{0,31}):(.*)$`)

// errPeer: a machine:path or machine/id names a peer, and peers are not connected yet.
type errPeer string

func (e errPeer) Error() string { return "peer not connected: " + string(e) }

// localPath is arg as an absolute local path; a machine:path is an errPeer.
func localPath(arg string) (string, error) {
	if !strings.HasPrefix(arg, "/") && !strings.HasPrefix(arg, ".") && !strings.HasPrefix(arg, "~") {
		if m := peerPath.FindStringSubmatch(arg); m != nil {
			return "", errPeer(m[1])
		}
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

// resolve is the full id of the session a unique prefix names.
func (e *env) resolve(arg string) (string, error) {
	if m, _, ok := strings.Cut(arg, "/"); ok {
		return "", errPeer(m)
	}
	if arg == "" {
		return "", errors.New("no session id")
	}
	var list []api.Session
	if err := e.do("GET", "/api/sessions", nil, &list); err != nil {
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
	_ = e.do("GET", "/api/sessions", nil, &list)
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
