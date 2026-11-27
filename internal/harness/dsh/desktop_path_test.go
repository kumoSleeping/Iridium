package dsh

import (
	"os"
	"path/filepath"
	"testing"
)

func makeFakeDesktopRoot(t *testing.T, root, exeName string) (exe, entry string) {
	t.Helper()
	exe = filepath.Join(root, exeName)
	res := filepath.Join(root, "resources")
	if err := os.MkdirAll(filepath.Join(res, "runtime", "primary-runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res, "app.asar"), []byte("asar"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry = filepath.Join(res, "app.asar", "dsh", "node_modules", "@deepseek-ai", "dsh-desktop-host", "lib", "index.js")
	return exe, entry
}

func TestDesktopInstallPriorityAndPersistence(t *testing.T) {
	state := t.TempDir()
	helloRoot := filepath.Join(t.TempDir(), "custom-hello")
	procRoot := filepath.Join(t.TempDir(), "custom-proc")
	manualRoot := filepath.Join(t.TempDir(), "custom-manual")

	helloExe, helloEntry := makeFakeDesktopRoot(t, helloRoot, "deepseek-harness")
	procExe, procEntry := makeFakeDesktopRoot(t, procRoot, "deepseek-harness")
	manualExe, _ := makeFakeDesktopRoot(t, manualRoot, "deepseek-harness")

	// 1. Manual fallback when nothing persisted and no proc.
	inst, ok := DiscoverDesktop(DesktopDiscoverOptions{
		StateDir:   state,
		ManualPath: manualRoot,
	})
	if !ok || inst.Exe != manualExe || inst.Source != "manual" {
		t.Fatalf("expected manual install, got ok=%v inst=%+v", ok, inst)
	}

	// 2. Running process beats manual and persists to StateDir.
	procInst, ok := ResolveDesktopRoot(procRoot)
	if !ok {
		t.Fatal("ResolveDesktopRoot(procRoot) failed")
	}
	inst, ok = DiscoverDesktop(DesktopDiscoverOptions{
		StateDir:   state,
		ManualPath: manualRoot,
		ScanProcs:  func() (DesktopInstall, bool) { return procInst, true },
	})
	if !ok || inst.Exe != procExe || inst.HostEntry != procEntry || inst.Source != "proc" {
		t.Fatalf("expected proc install, got ok=%v inst=%+v", ok, inst)
	}

	// 3. Subsequent discovery reads persisted file even without ScanProcs.
	inst, ok = DiscoverDesktop(DesktopDiscoverOptions{StateDir: state})
	if !ok || inst.Exe != procExe {
		t.Fatalf("expected persisted install, got ok=%v inst=%+v", ok, inst)
	}

	// 4. Persisted hello replaces persisted proc and wins priority.
	helloInst, ok := ResolveDesktopRoot(helloRoot)
	if !ok {
		t.Fatal("ResolveDesktopRoot(helloRoot) failed")
	}
	helloInst.Source = "hello"
	if err := SaveDesktopInstall(state, helloInst); err != nil {
		t.Fatalf("SaveDesktopInstall: %v", err)
	}
	inst, ok = DiscoverDesktop(DesktopDiscoverOptions{
		StateDir:   state,
		ManualPath: manualRoot,
		ScanProcs:  func() (DesktopInstall, bool) { return procInst, true },
	})
	if !ok || inst.Exe != helloExe || inst.HostEntry != helloEntry || inst.Source != "hello" {
		t.Fatalf("expected hello install, got ok=%v inst=%+v", ok, inst)
	}
}

func TestResolveDesktopMacBundle(t *testing.T) {
	app := filepath.Join(t.TempDir(), "DeepSeek Harness.app")
	exe := filepath.Join(app, "Contents", "MacOS", "DeepSeek Harness")
	entry := filepath.Join(app, "Contents", "Resources", "runtime", "node_modules", "@deepseek-ai", "dsh-desktop-host", "lib", "index.js")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("// host"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, ok := ResolveDesktopRoot(app)
	if !ok || inst.Exe != exe || inst.HostEntry != entry {
		t.Fatalf("ResolveDesktopRoot(.app) failed: ok=%v inst=%+v", ok, inst)
	}
	inst2, ok := ResolveDesktopRoot(exe)
	if !ok || inst2.HostEntry != entry {
		t.Fatalf("ResolveDesktopRoot(mac exe) failed: ok=%v inst=%+v", ok, inst2)
	}
}
