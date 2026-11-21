package server

// The channels' scripts (docs/channels.md "脚本"): each channel's IR (Iridium itself) keeps
// scripts, command lines it runs at the host, in the channel's folder, when one of their
// triggers fires: a message, a command, a time, a line of the channel's own. What a script
// prints is said in the channel under its name; with ir (IRIDIUM_SCRIPT) it says what it likes
// as itself. Its words wake only the agents they name, and go on the run that set it off: they
// count towards deliver.MaxHops as an agent's do.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/cron"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// scriptMemberPrefix begins the member a script of a channel speaks as: "script:<name>".
const scriptMemberPrefix = "script:"

func scriptMember(name string) string { return scriptMemberPrefix + name }

// HeaderScript: the script of a channel the local ir runs in ("<channel>/<name>"), as Iridium
// set IRIDIUM_SCRIPT for it.
const HeaderScript = "X-Iridium-Script"

// Limits of a script.
const (
	ScriptTimeout  = 60 * time.Second
	scriptOutMax   = 16 << 10
	scriptErrMax   = 4 << 10
	scriptSayMax   = 4000
	scriptRunMax   = 4000
	scriptIntroMax = 300
)

// scriptEvents are the lines of a channel's own an event trigger may name.
var scriptEvents = []string{"join", "leave", "rename", "recv", "people"}

func (s *Server) scriptRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/channels/{id}/scripts", s.scriptsGet)
	mux.HandleFunc("POST /v1/internal/channels/{id}/scripts", s.scriptAdd)
	mux.HandleFunc("PATCH /v1/internal/channels/{id}/scripts/{name}", s.scriptPatch)
	mux.HandleFunc("DELETE /v1/internal/channels/{id}/scripts/{name}", s.scriptRemove)
	mux.HandleFunc("GET /v1/internal/channels/{id}/scripts/{name}/runs", s.scriptRunsGet)
	mux.HandleFunc("POST /v1/internal/channels/{id}/scripts/{name}/run", s.scriptTry)
}

// ---- the API ----

// scriptChannel is the channel of a request on its scripts: one c sees and is in (a person of
// it, or an agent of it).
func (s *Server) scriptChannel(w http.ResponseWriter, r *http.Request) (caller, string, bool) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return c, "", false
	}
	if err := s.inChannel(c, ch); err != nil {
		s.error(w, err)
		return c, "", false
	}
	return c, ch, true
}

func (s *Server) scriptsGet(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.scriptChannel(w, r)
	if !ok {
		return
	}
	xs, err := s.st.Scripts(ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	out := []api.Script{}
	for _, x := range xs {
		out = append(out, s.scriptOut(x))
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) scriptRunsGet(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.scriptChannel(w, r)
	if !ok {
		return
	}
	x, ok := s.scriptFound(w, ch, r.PathValue("name"))
	if !ok {
		return
	}
	runs, err := s.st.ScriptRuns(ch, x.Name, store.ScriptRunsKept)
	if err != nil {
		s.fault(w, err)
		return
	}
	out := []api.ScriptRun{}
	for _, run := range runs {
		out = append(out, runOut(run))
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) scriptFound(w http.ResponseWriter, ch, name string) (store.Script, bool) {
	x, ok, err := s.st.Script(ch, name)
	if err != nil {
		s.fault(w, err)
		return x, false
	}
	if !ok {
		refuse(w, http.StatusUnprocessableEntity, "no_such_script", "no script "+name+" in the channel")
		return x, false
	}
	return x, true
}

func (s *Server) scriptAdd(w http.ResponseWriter, r *http.Request) {
	c, ch, ok := s.scriptChannel(w, r)
	if !ok {
		return
	}
	var req api.NewScript
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.scriptHere(ch); err != nil {
		refuse(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	x := store.Script{Channel: ch, Name: strings.TrimSpace(req.Name), Intro: strings.TrimSpace(req.Intro), Avatar: strings.TrimSpace(req.Avatar), Run: strings.TrimSpace(req.Run), Enabled: req.Enabled == nil || *req.Enabled}
	on, err := cleanTriggers(req.On)
	if err == nil {
		err = checkScript(x)
	}
	if err != nil {
		refuse(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	x.Triggers = encodeTriggers(on)
	by := s.scriptBy(c)
	x.Made, x.Changed = by, by
	if err := s.st.AddScript(x); errors.Is(err, store.ErrScriptTaken) {
		refuse(w, http.StatusConflict, "name_taken", err.Error())
		return
	} else if err != nil {
		s.fault(w, err)
		return
	}
	s.tellMembers(c, ch, fmt.Sprintf("添加了脚本「%s」", x.Name), channel.Event{Type: "script", Name: x.Name, What: []string{"add"}})
	s.scriptsChanged(ch)
	x, _, _ = s.st.Script(ch, x.Name)
	reply(w, http.StatusOK, s.scriptOut(x))
}

func (s *Server) scriptPatch(w http.ResponseWriter, r *http.Request) {
	c, ch, ok := s.scriptChannel(w, r)
	if !ok {
		return
	}
	x, ok := s.scriptFound(w, ch, r.PathValue("name"))
	if !ok {
		return
	}
	var p api.ScriptPatch
	if !readJSON(w, r, &p) {
		return
	}
	was := x
	var what []string
	set := func(field string, dst *string, v *string) {
		if v != nil && strings.TrimSpace(*v) != *dst {
			*dst = strings.TrimSpace(*v)
			what = append(what, field)
		}
	}
	set("name", &x.Name, p.Name)
	set("intro", &x.Intro, p.Intro)
	set("avatar", &x.Avatar, p.Avatar)
	set("run", &x.Run, p.Run)
	if p.On != nil {
		on, err := cleanTriggers(*p.On)
		if err != nil {
			refuse(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if t := encodeTriggers(on); t != x.Triggers {
			x.Triggers = t
			what = append(what, "on")
		}
	}
	if p.Enabled != nil && *p.Enabled != x.Enabled {
		x.Enabled = *p.Enabled
		what = append(what, cond(x.Enabled, "enabled", "disabled"))
	}
	if len(what) == 0 {
		reply(w, http.StatusOK, s.scriptOut(x))
		return
	}
	if err := checkScript(x); err != nil {
		refuse(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	x.Changed, x.ChangedAt = s.scriptBy(c), time.Now().UnixMilli()
	if err := s.st.PutScript(was.Name, x); errors.Is(err, store.ErrScriptTaken) {
		refuse(w, http.StatusConflict, "name_taken", err.Error())
		return
	} else if err != nil {
		s.fault(w, err)
		return
	}
	text := fmt.Sprintf("修改了脚本「%s」", x.Name)
	switch {
	case slices.Equal(what, []string{"enabled"}):
		text = fmt.Sprintf("启用了脚本「%s」", x.Name)
	case slices.Equal(what, []string{"disabled"}):
		text = fmt.Sprintf("停用了脚本「%s」", x.Name)
	case slices.Contains(what, "name"):
		text = fmt.Sprintf("把脚本「%s」改名为「%s」", was.Name, x.Name)
	}
	s.tellMembers(c, ch, text, channel.Event{Type: "script", Name: x.Name, From: cond(was.Name != x.Name, was.Name, ""), What: what})
	s.scriptsChanged(ch)
	x, _, _ = s.st.Script(ch, x.Name)
	reply(w, http.StatusOK, s.scriptOut(x))
}

func cond[T any](ok bool, a, b T) T {
	if ok {
		return a
	}
	return b
}

func (s *Server) scriptRemove(w http.ResponseWriter, r *http.Request) {
	c, ch, ok := s.scriptChannel(w, r)
	if !ok {
		return
	}
	x, ok := s.scriptFound(w, ch, r.PathValue("name"))
	if !ok {
		return
	}
	if _, err := s.st.RemoveScript(ch, x.Name); err != nil {
		s.fault(w, err)
		return
	}
	s.tellMembers(c, ch, fmt.Sprintf("删除了脚本「%s」", x.Name), channel.Event{Type: "script", Name: x.Name, What: []string{"remove"}})
	s.scriptsChanged(ch)
	reply(w, http.StatusOK, map[string]bool{"ok": true})
}

// scriptTry runs a script now, by hand, and answers with what the run did.
func (s *Server) scriptTry(w http.ResponseWriter, r *http.Request) {
	c, ch, ok := s.scriptChannel(w, r)
	if !ok {
		return
	}
	x, ok := s.scriptFound(w, ch, r.PathValue("name"))
	if !ok {
		return
	}
	var req api.ScriptTry
	if r.ContentLength > 0 && !readJSON(w, r, &req) {
		return
	}
	who := s.satoriUser(c)
	run := s.runScript(x, scriptJob{trigger: "hand", text: req.Text, member: readerOf(c), name: who.Name, lk: s.chain.of(ch, "")})
	reply(w, http.StatusOK, runOut(run))
}

// scriptHere: scripts run at the host, in the channel's folder; a channel of another computer
// has none.
func (s *Server) scriptHere(ch string) error {
	st, err := s.m.State(ch)
	if err != nil {
		return err
	}
	if st.Machine != "" {
		return errScriptAway
	}
	return nil
}

var errScriptAway = errors.New("scripts run at the host: this channel's folder is at another computer")

// checkScript: a script's own fields are as they may be.
func checkScript(x store.Script) error {
	switch {
	case !deliver.ValidNick(x.Name):
		return errors.New("name: letters, digits, _ and -, at most 32")
	case x.Run == "":
		return errors.New("run: the command line to run")
	case utf8.RuneCountInString(x.Run) > scriptRunMax:
		return fmt.Errorf("run: at most %d characters", scriptRunMax)
	case utf8.RuneCountInString(x.Intro) > scriptIntroMax:
		return fmt.Errorf("intro: at most %d characters", scriptIntroMax)
	case utf8.RuneCountInString(x.Avatar) > 16:
		return errors.New("avatar: an emoji or a colour (#rrggbb)")
	}
	return nil
}

// cleanTriggers checks a script's triggers: at least one, each of one kind.
func cleanTriggers(on []api.Trigger) ([]api.Trigger, error) {
	if len(on) == 0 {
		return nil, errors.New("on: at least one trigger: {message}, {command}, {cron} or {event}")
	}
	out := make([]api.Trigger, 0, len(on))
	for _, t := range on {
		kinds := 0
		if t.Message != nil {
			kinds++
			from, err := channel.CleanRecv(t.Message.From, "")
			if err != nil {
				return nil, fmt.Errorf("on.message.from: %w", err)
			}
			var words []string
			for _, w := range t.Message.Words {
				if w = strings.TrimSpace(w); w != "" && !slices.Contains(words, w) {
					words = append(words, w)
				}
			}
			t.Message = &api.MessageTrigger{Words: words}
			if len(from) > 0 {
				t.Message.From = from
			}
		}
		if t.Command != "" {
			kinds++
			t.Command = strings.TrimPrefix(strings.TrimSpace(t.Command), "/")
			if !deliver.ValidNick(t.Command) {
				return nil, errors.New("on.command: a name, letters, digits, _ and -")
			}
		}
		if t.Cron != "" {
			kinds++
			t.Cron = strings.Join(strings.Fields(t.Cron), " ")
			if _, err := cron.Parse(t.Cron); err != nil {
				return nil, fmt.Errorf("on.cron: %w", err)
			}
		}
		if len(t.Event) > 0 {
			kinds++
			for _, e := range t.Event {
				if !slices.Contains(scriptEvents, e) {
					return nil, fmt.Errorf("on.event: of %s", strings.Join(scriptEvents, ", "))
				}
			}
		}
		if kinds != 1 {
			return nil, errors.New("on: each trigger one of message, command, cron, event")
		}
		out = append(out, t)
	}
	return out, nil
}

func encodeTriggers(on []api.Trigger) string {
	b, _ := json.Marshal(on)
	return string(b)
}

func triggersOf(x store.Script) []api.Trigger {
	var on []api.Trigger
	_ = json.Unmarshal([]byte(x.Triggers), &on)
	if on == nil {
		on = []api.Trigger{}
	}
	return on
}

// scriptBy is who c is, as a script keeps who made or changed it: an agent with its session,
// model and role.
func (s *Server) scriptBy(c caller) string {
	by := api.ScriptBy{Member: readerOf(c), Name: s.satoriUser(c).Name, At: time.Now().UnixMilli()}
	if c.session != "" {
		by.Member, by.Session, by.Nick = store.AgentMember(c.session), c.session, s.ch.Nick(c.session)
		by.Name = by.Nick
		if st, err := s.m.State(c.session); err == nil {
			by.Model = cmp.Or(st.Model, st.Pick)
			by.Name = cmp.Or(by.Nick, s.sessionName(st))
		}
		if id := s.st.RoleOf(c.session); id != "" {
			if roles, err := s.st.Roles(); err == nil {
				for _, r := range roles {
					if r.ID == id {
						by.Role = r.Name
					}
				}
			}
		}
	}
	b, _ := json.Marshal(by)
	return string(b)
}

func byOf(raw string) api.ScriptBy {
	var by api.ScriptBy
	_ = json.Unmarshal([]byte(raw), &by)
	return by
}

func (s *Server) scriptOut(x store.Script) api.Script {
	out := api.Script{Channel: x.Channel, Name: x.Name, Intro: x.Intro, Avatar: x.Avatar, Run: x.Run, On: triggersOf(x), Enabled: x.Enabled, Made: byOf(x.Made), Changed: byOf(x.Changed)}
	if out.Made.At == 0 {
		out.Made.At = x.CreatedAt
	}
	if out.Changed.At == 0 {
		out.Changed.At = x.ChangedAt
	}
	if runs, err := s.st.ScriptRuns(x.Channel, x.Name, 1); err == nil && len(runs) > 0 {
		r := runOut(runs[0])
		out.Last = &r
	}
	return out
}

func runOut(r store.ScriptRun) api.ScriptRun {
	return api.ScriptRun{ID: r.ID, At: r.At, Ms: r.Ms, Trigger: r.Trigger, Seq: r.Seq, Code: r.Code, Said: r.Said, Out: r.Out, Err: r.Err}
}

// scriptsChanged: channel ch's scripts changed, or ran; the pages that show them read them
// again (the carried state "scripts:<channel>").
func (s *Server) scriptsChanged(string) {
	s.hub.Set("scripts", s.scriptsSeq.Add(1))
}

// ---- what runs them ----

// scriptJob is what a run of a script is given: what fired it (trigger) and the message that
// did (seq, its words text, said by member under name), the arguments of a command, the event
// of a line, and where on the channel's run it goes (lk).
type scriptJob struct {
	trigger string
	seq     int64
	text    string
	member  string
	name    string
	args    string
	event   *channel.Event
	lk      link
}

// scriptRuns keeps one run of a script at a time, and at most one waiting behind it: the
// latest (the ones between are dropped). links: the run each script running now goes on.
type scriptRuns struct {
	mu    sync.Mutex
	busy  map[string]bool
	next  map[string]*scriptJob
	links map[string]link
	cron  map[string]time.Time // the minute each script's cron last fired
}

func scriptKey(ch, name string) string { return ch + "\x00" + strings.ToLower(name) }

// fire runs x for j, now or after the run of it going on.
func (s *Server) fire(x store.Script, j scriptJob) {
	k := scriptKey(x.Channel, x.Name)
	r := &s.scripts
	r.mu.Lock()
	if r.busy == nil {
		r.busy, r.next = map[string]bool{}, map[string]*scriptJob{}
	}
	if r.busy[k] {
		r.next[k] = &j
		r.mu.Unlock()
		return
	}
	r.busy[k] = true
	r.mu.Unlock()
	go func() {
		for {
			s.runScript(x, j)
			r.mu.Lock()
			n := r.next[k]
			delete(r.next, k)
			if n == nil {
				delete(r.busy, k)
				r.mu.Unlock()
				return
			}
			r.mu.Unlock()
			j = *n
			cur, ok, err := s.st.Script(x.Channel, x.Name)
			if err != nil || !ok || !cur.Enabled {
				r.mu.Lock()
				delete(r.busy, k)
				r.mu.Unlock()
				return
			}
			x = cur
		}
	}()
}

// cappedBuffer keeps the first max bytes written to it.
type cappedBuffer struct {
	bytes.Buffer
	max int
	cut bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room < len(p) {
		b.cut = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func (b *cappedBuffer) text() string {
	t := strings.ToValidUTF8(b.String(), "")
	if b.cut {
		t += "\n…"
	}
	return t
}

// runScript runs x for j: in the channel's folder, given j as JSON on its standard input and
// in IR_* variables; what it prints, if it ends well, is said in the channel. What the run did
// is kept.
func (s *Server) runScript(x store.Script, j scriptJob) store.ScriptRun {
	run := store.ScriptRun{Channel: x.Channel, Name: x.Name, At: time.Now().UnixMilli(), Trigger: j.trigger, Seq: j.seq}
	keep := func() store.ScriptRun {
		run.Ms = time.Now().UnixMilli() - run.At
		id, err := s.st.AddScriptRun(run)
		if err != nil {
			s.logf("server: script %s of %s: keeping its run: %v", x.Name, x.Channel, err)
		}
		run.ID = id
		s.scriptsChanged(x.Channel)
		return run
	}
	st, err := s.m.State(x.Channel)
	if err != nil {
		run.Code, run.Err = -1, err.Error()
		return keep()
	}
	if st.Machine != "" {
		run.Code, run.Err = -1, errScriptAway.Error()
		return keep()
	}
	k := scriptKey(x.Channel, x.Name)
	s.scripts.mu.Lock()
	if s.scripts.links == nil {
		s.scripts.links = map[string]link{}
	}
	s.scripts.links[k] = j.lk
	s.scripts.mu.Unlock()
	defer func() {
		s.scripts.mu.Lock()
		delete(s.scripts.links, k)
		s.scripts.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), ScriptTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", x.Run)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", x.Run)
	}
	cmd.Dir = st.Cwd
	if fi, err := os.Stat(cmd.Dir); err != nil || !fi.IsDir() {
		cmd.Dir, _ = os.UserHomeDir()
	}
	cmd.WaitDelay = 2 * time.Second
	env := agent.CleanEnv(os.Environ())
	if exe, err := os.Executable(); err == nil {
		env = agent.SetEnv(env, "PATH", filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for _, kv := range [][2]string{
		{"IRIDIUM_SCRIPT", x.Channel + "/" + x.Name},
		{"IRIDIUM_CHANNEL", x.Channel},
		{"IR_TRIGGER", j.trigger},
		{"IR_SEQ", fmt.Sprint(j.seq)},
		{"IR_TEXT", j.text},
		{"IR_FROM", j.name},
		{"IR_FROM_ID", j.member},
		{"IR_ARGS", j.args},
	} {
		env = agent.SetEnv(env, kv[0], kv[1])
	}
	cmd.Env = env
	in := map[string]any{"trigger": j.trigger, "channel": x.Channel, "script": x.Name}
	if j.seq != 0 || j.text != "" {
		in["message"] = map[string]any{"seq": j.seq, "text": j.text, "from": j.member, "name": j.name}
	}
	if j.trigger == "command" {
		in["args"] = j.args
	}
	if j.event != nil {
		in["event"] = j.event
	}
	stdin, _ := json.Marshal(in)
	cmd.Stdin = bytes.NewReader(stdin)
	out, errs := &cappedBuffer{max: scriptOutMax}, &cappedBuffer{max: scriptErrMax}
	cmd.Stdout, cmd.Stderr = out, errs
	err = cmd.Run()
	run.Out, run.Err = out.text(), errs.text()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		run.Code = -1
		run.Err = strings.TrimSpace(run.Err + fmt.Sprintf("\nstopped after %s", ScriptTimeout))
	case errors.As(err, &exit):
		run.Code = exit.ExitCode()
	case err != nil:
		run.Code, run.Err = -1, strings.TrimSpace(run.Err+"\n"+err.Error())
	}
	if said := strings.TrimSpace(run.Out); run.Code == 0 && said != "" {
		if r := []rune(said); len(r) > scriptSayMax {
			said = string(r[:scriptSayMax]) + "…"
		}
		seq, err := s.scriptSay(x, said, j.lk, 0)
		if err != nil {
			run.Err = strings.TrimSpace(run.Err + "\nnot said: " + err.Error())
		}
		run.Said = seq
	}
	return keep()
}

// scriptCaller is whom script x acts for where a right is asked (an agent it wakes): who changed
// it last, as an agent's ir acts for its session's turn.
func (s *Server) scriptCaller(x store.Script) caller {
	by := byOf(x.Changed)
	if by.Session != "" {
		if o, ok := s.originOf(by.Session); ok {
			return caller{Identity: o, agent: true}
		}
		return caller{Identity: localIdentity, agent: true}
	}
	if app, login, ok := strings.Cut(by.Member, "/"); ok && app != gate.HostID {
		if login == "*" {
			login = ""
		}
		return caller{Identity: gate.Identity{App: app, Login: login, Name: by.Name}}
	}
	return caller{Identity: localIdentity}
}

// scriptSay says text in x's channel as x, on the run at lk, answering quote (0: none), and
// gives it to the agents it names (and that x may wake).
func (s *Server) scriptSay(x store.Script, text string, lk link, quote int64) (int64, error) {
	ch := x.Channel
	st, err := s.m.State(ch)
	if err != nil {
		return 0, err
	}
	u := satori.User{ID: scriptMember(x.Name), Name: x.Name}
	id := session.NewInputID()
	seq := s.sat.Input(st.Session, id, text, u, lk.origin, channel.Data{Script: x.Name, Quote: quote})
	if seq == 0 {
		return 0, errors.New("the channel did not take the message")
	}
	s.names(ch, seq, text, "")
	list, _ := s.agentsOf(ch)
	who := deliver.From{Member: u.ID, Quiet: true}
	wake, _, limit := s.wakes(text, who, lk, list)
	if limit > 0 {
		s.stopped(ch, seq, limit, lk.origin)
		return seq, nil
	}
	c := s.scriptCaller(x)
	from := session.From(gate.HostID, x.Name)
	for _, t := range wake {
		if _, err := s.using(c, t); err != nil {
			nick := s.ch.Nick(t)
			s.sat.Note(ch, fmt.Sprintf("未送达 #%d → @%s：脚本「%s」无权叫起它", seq, nick, x.Name), lk.origin, &channel.Event{Type: "undelivered", Seq: seq, Nick: nick})
			continue
		}
		go func(t string) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if _, err := s.deliverTo(ctx, ch, t, seq, text, "", from, nil, reach(who, lk)); err != nil {
				s.logf("server: script %s of %s: message %d to %s: %v", x.Name, ch, seq, t, err)
			}
		}(t)
	}
	return seq, nil
}

// scriptOfCaller is the script the local ir runs in (HeaderScript), if it is one.
func (s *Server) scriptOfCaller(c caller) (store.Script, bool) {
	if c.script == "" {
		return store.Script{}, false
	}
	ch, name, ok := strings.Cut(c.script, "/")
	if !ok {
		return store.Script{}, false
	}
	x, ok, err := s.st.Script(ch, name)
	return x, ok && err == nil
}

// scriptSend: a script's ir says req in channel ch, as the script, on the run its run goes on.
func (s *Server) scriptSend(c caller, ch string, req api.Send) (api.SendReply, error) {
	x, ok := s.scriptOfCaller(c)
	if !ok {
		return api.SendReply{}, fmt.Errorf("%w: no such script", session.ErrBadRequest)
	}
	if x.Channel != ch {
		return api.SendReply{}, fmt.Errorf("%w: a script speaks in its own channel alone", session.ErrBadRequest)
	}
	s.scripts.mu.Lock()
	lk, ok := s.scripts.links[scriptKey(x.Channel, x.Name)]
	s.scripts.mu.Unlock()
	if !ok {
		lk = s.chain.of(ch, "")
	}
	seq, err := s.scriptSay(x, req.Text, lk, req.Quote)
	if err != nil {
		return api.SendReply{}, err
	}
	return api.SendReply{Seq: seq, Said: true}, nil
}

// ---- what fires them ----

// heard: a message said in channel ch (number seq, words text) by member (a login
// "<app>/<login>", an agent "agent:<session>") under name, a person's or an agent's turn's last
// words, standing at lk on the channel's run: the scripts it fires run.
func (s *Server) heard(ch string, seq int64, text, member, name string, lk link) {
	if strings.HasPrefix(member, scriptMemberPrefix) {
		return
	}
	xs, err := s.st.Scripts(ch)
	if err != nil || len(xs) == 0 {
		return
	}
	if st, err := s.m.State(ch); err != nil || st.Archived {
		return
	}
	body := strings.TrimSpace(text)
	agentSaid := strings.HasPrefix(member, "agent:")
	_, isCommand := s.scriptCommandOf(ch, body)
	type fired struct {
		x store.Script
		j scriptJob
	}
	var due []fired
	for _, x := range xs {
		if !x.Enabled {
			continue
		}
		for _, t := range triggersOf(x) {
			j := scriptJob{seq: seq, text: text, member: member, name: name, lk: lk}
			if t.Command != "" {
				args, ok := commandArgs(body, t.Command)
				if !ok || agentSaid {
					continue
				}
				j.trigger, j.args = "command", args
			} else if t.Message != nil {
				if isCommand {
					continue // a command is its script's alone
				}
				if len(t.Message.From) > 0 && !deliver.Receives(t.Message.From, member) {
					continue
				}
				if len(t.Message.Words) > 0 && !slices.ContainsFunc(t.Message.Words, func(w string) bool {
					return strings.Contains(strings.ToLower(text), strings.ToLower(w))
				}) {
					continue
				}
				j.trigger = "message"
			} else {
				continue
			}
			due = append(due, fired{x, j})
			break
		}
	}
	// Past the limit, an agent's words set no script off: the channel says so.
	if len(due) > 0 && passes(deliver.From{Member: member, Session: sessionOfMember(member)}) && lk.hop >= deliver.MaxHops {
		s.stopped(ch, seq, deliver.MaxHops, lk.origin)
		return
	}
	for _, f := range due {
		s.fire(f.x, f.j)
	}
}

func sessionOfMember(m string) string {
	id, _ := store.SessionOfMember(m)
	return id
}

// commandArgs: body is "/<command>" alone or with words after a space; args, those words.
func commandArgs(body, command string) (string, bool) {
	rest, ok := strings.CutPrefix(body, "/")
	if !ok || command == "" {
		return "", false
	}
	word, args, _ := strings.Cut(rest, " ")
	word, more, _ := strings.Cut(word, "\n")
	if !strings.EqualFold(word, command) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSpace(more) + " " + args), true
}

// scriptCommandOf is the script of channel ch whose command body is, if any.
func (s *Server) scriptCommandOf(ch, body string) (store.Script, bool) {
	if !strings.HasPrefix(body, "/") {
		return store.Script{}, false
	}
	xs, err := s.st.Scripts(ch)
	if err != nil {
		return store.Script{}, false
	}
	for _, x := range xs {
		if !x.Enabled {
			continue
		}
		for _, t := range triggersOf(x) {
			if _, ok := commandArgs(body, t.Command); ok {
				return x, true
			}
		}
	}
	return store.Script{}, false
}

// eventHeard: a line of channel ch's own (number seq, words text) told of ev; the scripts that
// wait for it run.
func (s *Server) eventHeard(ch string, seq int64, ev *channel.Event, text string) {
	if ev == nil || !slices.Contains(scriptEvents, ev.Type) {
		return
	}
	xs, err := s.st.Scripts(ch)
	if err != nil || len(xs) == 0 {
		return
	}
	if st, err := s.m.State(ch); err != nil || st.Archived {
		return
	}
	for _, x := range xs {
		if !x.Enabled {
			continue
		}
		for _, t := range triggersOf(x) {
			if slices.Contains(t.Event, ev.Type) {
				s.fire(x, scriptJob{trigger: "event", seq: seq, text: text, member: satori.NoteUser, name: "Iridium", event: ev, lk: s.chain.of(ch, "")})
				break
			}
		}
	}
}

// sayCommand: a person sent a script's command in channel ch: said there, given to no agent;
// the script runs on it.
func (s *Server) sayCommand(c caller, from, ch string, req api.Send) (api.SendReply, error) {
	if err := s.inChannel(c, ch); err != nil {
		return api.SendReply{}, err
	}
	st, err := s.m.State(ch)
	if err != nil {
		return api.SendReply{}, err
	}
	if req.ID == "" {
		req.ID = session.NewInputID()
	}
	u := s.satoriUser(c)
	if tag := s.m.SourceTag(from); tag != "" {
		u.Name = tag
	}
	seq := s.sat.Input(st.Session, req.ID, req.Text, u, c.ID(), channel.Data{Device: s.deviceOfCaller(c)})
	if seq == 0 {
		return api.SendReply{}, errors.New("the channel did not take the message")
	}
	lk := s.chain.person(ch, c.ID())
	s.heard(ch, seq, req.Text, readerOf(c), u.Name, lk)
	return api.SendReply{Input: req.ID, Seq: seq, Said: true}, nil
}

// cronLoop fires the scripts whose time it is, at the start of each minute, until ctx ends.
func (s *Server) cronLoop(ctx context.Context) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
		}
		s.cronTick(next)
	}
}

// cronTick fires the scripts whose cron is minute at; each at most once a minute. A channel in
// the archive has none fired; what was missed meanwhile is not made up for. A time fired begins
// a run of the channel, as a person speaking does.
func (s *Server) cronTick(at time.Time) {
	xs, err := s.st.Scripts("")
	if err != nil {
		s.logf("server: scripts: %v", err)
		return
	}
	r := &s.scripts
	for _, x := range xs {
		if !x.Enabled {
			continue
		}
		for _, t := range triggersOf(x) {
			if t.Cron == "" {
				continue
			}
			sp, err := cron.Parse(t.Cron)
			if err != nil || !sp.Match(at) {
				continue
			}
			k := scriptKey(x.Channel, x.Name)
			r.mu.Lock()
			if r.cron == nil {
				r.cron = map[string]time.Time{}
			}
			done := r.cron[k].Equal(at)
			r.cron[k] = at
			r.mu.Unlock()
			if done {
				break
			}
			if st, err := s.m.State(x.Channel); err != nil || st.Archived {
				break
			}
			s.fire(x, scriptJob{trigger: "cron", lk: s.chain.person(x.Channel, satori.NoteUser)})
			break
		}
	}
}
