package session

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// shellAll reads run until it is done: all its output and its end.
func shellAll(t *testing.T, m *Manager, run string) (string, api.ShellOutput) {
	t.Helper()
	var b strings.Builder
	var from int64
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		out, err := m.ShellRead(run, from)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(out.Data)
		from = out.Next
		if out.Done {
			return b.String(), out
		}
	}
	t.Fatalf("%s did not end: %q", run, b.String())
	return "", api.ShellOutput{}
}

// testShell runs command lines at session id, whose folder is cwd.
func testShell(t *testing.T, m *Manager, id, cwd string) {
	t.Helper()
	ctx := context.Background()
	st, err := m.Shell(ctx, id, "echo out; echo err >&2; pwd", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Run == "" || st.Cwd != cwd {
		t.Fatalf("started %+v", st)
	}
	data, end := shellAll(t, m, st.Run)
	if !strings.Contains(data, "out\nerr\n") || !strings.Contains(data, filepath.Base(cwd)) || end.Code == nil || *end.Code != 0 {
		t.Errorf("output %q, end %+v", data, end)
	}

	st, err = m.Shell(ctx, id, "exit 3", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, end := shellAll(t, m, st.Run); end.Code == nil || *end.Code != 3 {
		t.Errorf("exit 3 ended %+v", end)
	}

	// cd runs nothing; a folder that is not there is refused.
	st, err = m.Shell(ctx, id, "cd ..", cwd)
	if err != nil || st.Run != "" || st.Cwd != filepath.Dir(cwd) {
		t.Errorf("cd ..: %+v %v", st, err)
	}
	if _, err := m.Shell(ctx, id, "cd nowhere", cwd); err == nil {
		t.Error("cd to no folder was taken")
	}

	// A run stopped ends.
	st, err = m.Shell(ctx, id, "sleep 30", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := m.ShellStop(ctx, st.Run); err != nil {
		t.Fatal(err)
	}
	if _, end := shellAll(t, m, st.Run); end.Code != nil && *end.Code == 0 {
		t.Errorf("stopped run ended %+v", end)
	}
	if _, err := m.ShellRead("sh-none", 0); err == nil {
		t.Error("an unknown run was read")
	}
}

func TestShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	r := newRig(t, nil)
	id, err := r.m.Create(bg, api.NewSession{Agent: api.AgentClaude, Cwd: r.cwd}, "web")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "started", func(s *api.SessionState) bool { return s.Live })
	testShell(t, r.m, id, r.cwd)
	if _, err := r.m.MachineShell(bg, "", "true", ""); err == nil {
		t.Error("ir sh ran with no machine")
	}
}
