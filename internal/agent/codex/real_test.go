package codex

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestRealModels lists the real codex's models, no thread opened, no model called: run with
// IRIDIUM_REAL_CODEX_MODELS=1 to pick one for a real run.
func TestRealModels(t *testing.T) {
	if os.Getenv("IRIDIUM_REAL_CODEX_MODELS") == "" {
		t.Skip("set IRIDIUM_REAL_CODEX_MODELS=1 to ask the real codex")
	}
	cmd := exec.Command("codex", "app-server", "--listen", "stdio://")
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); cmd.Wait() }()
	d := New(Options{Logf: t.Logf})
	d.Attach(func(line []byte) error { _, err := in.Write(append(line, '\n')); return err }, 0)
	go func() {
		s := bufio.NewScanner(out)
		s.Buffer(nil, MaxLine)
		for s.Scan() {
			d.Feed(s.Bytes(), false)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := d.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "iridium", "title": "Iridium", "version": Version}}); err != nil {
		t.Fatal(err)
	}
	d.write(map[string]any{"method": "initialized"})
	models, err := d.listModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if thread := os.Getenv("IRIDIUM_REAL_CODEX_THREAD"); thread != "" {
		r, err := d.call(ctx, "account/usage/read", map[string]any{"threadId": thread})
		t.Logf("account/usage/read: %s %v", r, err)
	}
	for _, m := range models {
		var efforts []string
		for _, e := range m.Efforts {
			efforts = append(efforts, e.Effort)
		}
		t.Logf("%s %q default=%v efforts=%v (default %s)", m.ID, m.DisplayName, m.IsDefault, efforts, m.DefaultEffort)
	}
}
