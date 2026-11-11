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
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/satori"
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
	// as: the login ir speaks Satori as, once meta named it (ir api).
	as *satori.Login
	// agent: an agent runs ir (its environment says so, or no person at a terminal): every request
	// says so (X-Iridium-Agent), so the daemon gives it no secret and takes no approval from it.
	agent bool
}

// runByAgent: ir is run by an agent, as the admin commands tell (detect): the environment names
// an agent's session, or stdin and stdout are not both a terminal. A var for the tests.
var runByAgent = func() bool {
	return os.Getenv("CLAUDECODE") != "" || caller() != "" || !isTerminal(os.Stdin) || !isTerminal(os.Stdout)
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
	return &env{out: stdout, err: stderr, sock: sock, hc: hc, tty: isTTY(stderr), agent: runByAgent()}
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
	if e.agent && e.at == nil {
		req.Header.Set(headerAgent, "1")
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
		msg := ae.Text()
		if ae.NameError != "" {
			msg += " (" + ae.NameError + ")"
		}
		return nil, &apiError{status: resp.StatusCode, msg: msg}
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

// resolve is the full id of the session a unique prefix names (the server takes the start of one).
func (e *env) resolve(arg string) (string, error) {
	if arg == "" {
		return "", errors.New("no session id")
	}
	var st api.SessionState
	if err := e.do("GET", "/v1/internal/sessions/"+url.PathEscape(arg), nil, &st); err != nil {
		return "", err
	}
	return st.ID, nil
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

// ids are every session's id (channel.list, archived ones too).
func (e *env) ids() []string {
	var out []string
	next := ""
	for {
		body := map[string]any{"_data": map[string]any{"all": true}}
		if next != "" {
			body["next"] = next
		}
		raw, err := e.satori("channel.list", body)
		var l satori.List[satori.Channel]
		if err != nil || json.Unmarshal(raw, &l) != nil {
			return out
		}
		for _, c := range l.Data {
			if c.Type == satori.ChannelText {
				out = append(out, c.ID)
			}
		}
		if next = l.Next; next == "" {
			return out
		}
	}
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
