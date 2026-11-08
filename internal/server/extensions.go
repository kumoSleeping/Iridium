package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/version"
)

// Official extensions (plan "插件"): plugins of Iridium's own release that it does not build in.
// Installed when asked (the page's 终端管理, +), they are binaries next to Iridium's; Iridium runs
// their command lines and puts their TOKEN in place, and knows nothing of what they talk to.

type extension struct {
	id, name, bin string
}

var officialExtensions = []extension{{id: "qq", name: "QQ Bot", bin: "iridium-qq"}}

func findExtension(id string) (extension, bool) {
	for _, e := range officialExtensions {
		if e.id == id {
			return e, true
		}
	}
	return extension{}, false
}

// extensions is what is under way with each.
type extensions struct {
	mu  sync.Mutex
	all map[string]*extState
}

type extState struct {
	busy, err string
	bind      *api.ExtensionBind
	cancel    context.CancelFunc
}

func (x *extensions) get(id string) *extState {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.all == nil {
		x.all = map[string]*extState{}
	}
	st := x.all[id]
	if st == nil {
		st = &extState{}
		x.all[id] = st
	}
	return st
}

// ExtDir is where the extensions' binaries are: next to Iridium's own.
func (s *Server) extDir() string {
	if s.ExtDir != "" {
		return s.ExtDir
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

func (s *Server) extBin(e extension) string { return filepath.Join(s.extDir(), e.bin) }

func (s *Server) extensionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/extensions", s.listExtensions)
	mux.HandleFunc("POST /api/extensions/{id}/install", s.extAction(s.extInstall))
	mux.HandleFunc("POST /api/extensions/{id}/bind", s.extAction(s.extBind))
	mux.HandleFunc("DELETE /api/extensions/{id}/bind", s.extAction(s.extUnbind))
	mux.HandleFunc("POST /api/extensions/{id}/start", s.extAction(func(ctx context.Context, e extension, _ caller) error { return s.extRun(ctx, e, "start") }))
	mux.HandleFunc("POST /api/extensions/{id}/stop", s.extAction(func(ctx context.Context, e extension, _ caller) error { return s.extRun(ctx, e, "stop") }))
	mux.HandleFunc("POST /api/extensions/{id}/pair", s.extAction(func(ctx context.Context, e extension, _ caller) error { return s.extRun(ctx, e, "pair") }))
	mux.HandleFunc("DELETE /api/extensions/{id}", s.extAction(s.extRemove))
}

func (s *Server) listExtensions(w http.ResponseWriter, r *http.Request) {
	out := []api.Extension{}
	for _, e := range officialExtensions {
		out = append(out, s.extensionState(r.Context(), e))
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) extensionState(ctx context.Context, e extension) api.Extension {
	st := s.ext.get(e.id)
	s.ext.mu.Lock()
	out := api.Extension{ID: e.id, Name: e.name, Busy: st.busy, Error: st.err, Bind: st.bind}
	s.ext.mu.Unlock()
	if _, err := os.Stat(s.extBin(e)); err != nil {
		return out
	}
	out.Installed = true
	if es, err := s.extStatus(ctx, e); err == nil {
		out.Status = &es
		if es.Name != "" {
			for _, t := range s.gate().Access().Endpoints {
				if t.Kind == gate.KindChannel && strings.EqualFold(t.Name, es.Name) {
					out.Online = t.Online
				}
			}
		}
	} else if out.Error == "" {
		out.Error = err.Error()
	}
	return out
}

func (s *Server) extStatus(ctx context.Context, e extension) (api.ExtensionStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var st api.ExtensionStatus
	b, err := exec.CommandContext(ctx, s.extBin(e), "status", "--json").Output()
	if err != nil {
		return st, fmt.Errorf("%s status: %w", e.bin, err)
	}
	return st, json.Unmarshal(b, &st)
}

// extRun runs the extension's command; its last line of output is the error if it fails.
func (s *Server) extRun(ctx context.Context, e extension, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.extBin(e), args...).CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if msg := strings.TrimSpace(lines[len(lines)-1]); msg != "" {
			return errors.New(strings.TrimPrefix(msg, e.bin+": "))
		}
		return err
	}
	s.logf("server: %s %s", e.bin, strings.Join(args, " "))
	return nil
}

// extAction runs an action on an extension for a person (a panel, or ir at the machine): an
// agent runs the extension's own command line instead. The answer is the extension's state.
func (s *Server) extAction(f func(context.Context, extension, caller) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, ok := findExtension(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, "no such extension")
			return
		}
		c := callerOf(r)
		if !c.panel() {
			forbid(w, errors.New("an agent runs the extension's own command (iridium-qq setup)"))
			return
		}
		st := s.ext.get(e.id)
		s.ext.mu.Lock()
		if st.busy != "" && st.busy != "bind" {
			s.ext.mu.Unlock()
			fail(w, http.StatusConflict, "busy: "+st.busy)
			return
		}
		st.err = ""
		s.ext.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		if err := f(ctx, e, c); err != nil {
			s.ext.mu.Lock()
			st.err = err.Error()
			s.ext.mu.Unlock()
			s.logf("server: extension %s: %v", e.id, err)
		}
		reply(w, http.StatusOK, s.extensionState(r.Context(), e))
	}
}

func (s *Server) setBusy(e extension, busy string) {
	st := s.ext.get(e.id)
	s.ext.mu.Lock()
	st.busy = busy
	s.ext.mu.Unlock()
}

// extInstall puts the extension's binary of this very version next to Iridium's: from the
// release, or, on a build of no release, one placed there already.
func (s *Server) extInstall(ctx context.Context, e extension, _ caller) error {
	s.setBusy(e, "install")
	defer s.setBusy(e, "")
	return s.installExtension(ctx, e)
}

func (s *Server) installExtension(ctx context.Context, e extension) error {
	dir := s.extDir()
	if dir == "" {
		return errors.New("cannot tell where Iridium's binary is")
	}
	if !isRelease(version.Version) {
		// A development build has no release: it takes the copy put beside it (scripts/push-test.sh).
		if _, err := os.Stat(s.extBin(e)); err == nil {
			return nil
		}
		b, err := os.ReadFile(s.extBin(e) + ".dist")
		if err != nil {
			return fmt.Errorf("a development build (%s) has no release to download %s from: put it in %s", version.Version, e.bin, dir)
		}
		tmp := s.extBin(e) + ".new"
		if err := os.WriteFile(tmp, b, 0o755); err != nil {
			return err
		}
		if err := os.Rename(tmp, s.extBin(e)); err != nil {
			return err
		}
		s.linkExtension(e)
		return nil
	}
	if s.Updater == nil {
		return errors.New("no update source to download from")
	}
	tmp, err := s.Updater.Extension(ctx, e.bin, version.Version, dir)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, s.extBin(e)); err != nil {
		os.Remove(tmp)
		return err
	}
	s.linkExtension(e)
	s.logf("server: installed %s %s in %s", e.bin, version.Version, dir)
	return nil
}

// linkExtension puts a link to the binary beside ir's, so a shell (an agent's too) finds it.
func (s *Server) linkExtension(e extension) {
	ir, err := exec.LookPath("ir")
	if err != nil {
		return
	}
	link := filepath.Join(filepath.Dir(ir), e.bin)
	if link == s.extBin(e) {
		return
	}
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		return // a file of someone else's
	}
	tmp := link + ".new"
	os.Remove(tmp)
	if os.Symlink(s.extBin(e), tmp) == nil {
		_ = os.Rename(tmp, link)
	}
}

// isRelease: a version a release is tagged with (v1.2.3, v1.2.3-rc.1), not a build between them
// (v1.2.3-dev.0+gabcdef, dev).
func isRelease(v string) bool {
	if !strings.HasPrefix(v, "v") || strings.ContainsAny(v, "+") || strings.Contains(v, "dev") || strings.Contains(v, "dirty") {
		return false
	}
	return strings.Count(v, ".") >= 2
}

// SyncExtensions brings each installed extension to Iridium's version (after an update), and
// restarts its service if it ran.
func (s *Server) SyncExtensions(ctx context.Context) {
	if !isRelease(version.Version) {
		return
	}
	for _, e := range officialExtensions {
		bin := s.extBin(e)
		if _, err := os.Stat(bin); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, bin, "version").Output()
		if err == nil && strings.TrimSpace(string(out)) == version.Version {
			continue
		}
		st, _ := s.extStatus(ctx, e)
		if err := s.installExtension(ctx, e); err != nil {
			s.logf("server: bringing %s to %s: %v", e.bin, version.Version, err)
			continue
		}
		if st.Service == "active" {
			if err := s.extRun(ctx, e, "restart"); err != nil {
				s.logf("server: restarting %s: %v", e.bin, err)
			}
		}
	}
}

// extBind starts a scan: the extension's bind gives QR codes till the phone scans one; then the
// TOKEN goes into its directory (with Claude when the person may say it is the owner's own) and
// its service starts.
func (s *Server) extBind(_ context.Context, e extension, c caller) error {
	if _, err := os.Stat(s.extBin(e)); err != nil {
		return errors.New("not installed")
	}
	st := s.ext.get(e.id)
	s.ext.mu.Lock()
	if st.busy == "bind" {
		s.ext.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	st.busy, st.bind, st.cancel = "bind", nil, cancel
	s.ext.mu.Unlock()
	by := s.ownerSays(c)
	cmd := exec.CommandContext(ctx, s.extBin(e), "bind", "--iridium", s.localURL())
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		s.setBusy(e, "")
		return err
	}
	s.logf("server: %s bind started", e.bin)
	ready := make(chan struct{})
	go func() {
		defer cancel()
		var once sync.Once
		done := func() { once.Do(func() { close(ready) }) }
		defer done()
		var result struct {
			State string `json:"state"`
			URL   string `json:"url"`
			Error string `json:"error"`
		}
		failed := ""
		bound := false
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if json.Unmarshal(sc.Bytes(), &result) != nil {
				continue
			}
			switch result.State {
			case "qr":
				s.logf("server: %s bind: a QR code", e.bin)
				qr, _ := qrSVG(result.URL)
				s.ext.mu.Lock()
				st.bind = &api.ExtensionBind{URL: result.URL, QR: qr}
				s.ext.mu.Unlock()
				done()
			case "bound":
				bound = true
			case "error":
				failed = result.Error
			}
		}
		werr := cmd.Wait()
		switch {
		case bound:
			s.logf("server: %s bind: scanned", e.bin)
		case ctx.Err() == context.Canceled:
			s.logf("server: %s bind: cancelled", e.bin)
		default:
			s.logf("server: %s bind ended: %s %v", e.bin, failed, werr)
		}
		if bound {
			s.ext.mu.Lock()
			st.bind = nil
			s.ext.mu.Unlock()
			if err := s.extToken(context.Background(), e, by); err != nil {
				failed = err.Error()
			} else if err := s.extRun(context.Background(), e, "start"); err != nil {
				failed = err.Error()
			}
		}
		if bound && failed != "" {
			s.logf("server: %s after the scan: %s", e.bin, failed)
		}
		s.ext.mu.Lock()
		st.busy, st.bind, st.cancel = "", nil, nil
		if failed != "" && ctx.Err() != context.Canceled {
			st.err = failed
		}
		s.ext.mu.Unlock()
	}()
	select { // the first QR code, to answer with
	case <-ready:
	case <-time.After(20 * time.Second):
	}
	return nil
}

func (s *Server) extUnbind(_ context.Context, e extension, _ caller) error {
	st := s.ext.get(e.id)
	s.ext.mu.Lock()
	cancel := st.cancel
	s.ext.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for i := 0; i < 50; i++ {
		s.ext.mu.Lock()
		busy := st.busy
		s.ext.mu.Unlock()
		if busy != "bind" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// extToken makes the extension's terminal's TOKEN and writes it into its directory.
func (s *Server) extToken(ctx context.Context, e extension, by string) error {
	es, err := s.extStatus(ctx, e)
	if err != nil {
		return err
	}
	name := es.Name
	if name == "" {
		name = "QQ"
	}
	tok, err := s.makeToken(name, by)
	if errors.Is(err, errClaudeFixed) {
		tok, err = s.makeToken(name, "") // a terminal there already keeps what it had
	}
	if err != nil {
		return err
	}
	return s.tokenInto(tok, filepath.Join(es.Home, "token"))
}

func (s *Server) extRemove(ctx context.Context, e extension, _ caller) error {
	s.setBusy(e, "remove")
	defer s.setBusy(e, "")
	if _, err := os.Stat(s.extBin(e)); err != nil {
		return nil
	}
	es, _ := s.extStatus(ctx, e)
	if err := s.extRun(ctx, e, "remove"); err != nil {
		return err
	}
	if es.Name != "" {
		if t, ok := s.gate().Find(es.Name); ok && t.Kind == gate.KindChannel {
			if err := s.gate().Revoke(t.ID); err != nil {
				s.logf("server: revoking %s: %v", es.Name, err)
			}
		}
	}
	if ir, err := exec.LookPath("ir"); err == nil {
		link := filepath.Join(filepath.Dir(ir), e.bin)
		if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			os.Remove(link)
		}
	}
	return os.Remove(s.extBin(e))
}

// localURL is this daemon's HTTP address as a local plugin reaches it.
func (s *Server) localURL() string {
	host, port, err := net.SplitHostPort(s.cfg.Listen)
	if err != nil {
		return "http://" + s.cfg.Listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// extDetach: a Bot terminal removed (the list's own remove, ir revoke) that an official
// extension runs as: the extension goes with it, its service stopped and its home cleared; its
// binary stays (the user, 2026-11-08: a Bot is a Bot, whoever made it).
func (s *Server) extDetach(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, e := range officialExtensions {
		if _, err := os.Stat(s.extBin(e)); err != nil {
			continue
		}
		if es, err := s.extStatus(ctx, e); err != nil || es.Name != name {
			continue
		}
		s.setBusy(e, "remove")
		if err := s.extRun(ctx, e, "remove"); err != nil {
			s.logf("server: %s: %v", e.bin, err)
		}
		s.setBusy(e, "")
	}
}
