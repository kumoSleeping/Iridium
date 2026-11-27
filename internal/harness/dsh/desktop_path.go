package dsh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	desktopInstallFile = "dsh-desktop-install.json"
	desktopManualFile  = "dsh-desktop-manual.json"
)

type manualDesktopConfig struct {
	Path string `json:"path,omitempty"`
}

// LoadManualDesktopPath reads the user-configured fallback Desktop path from StateDir.
func LoadManualDesktopPath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(stateDir, desktopManualFile))
	if err != nil {
		return ""
	}
	var cfg manualDesktopConfig
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Path)
}

// SaveManualDesktopPath persists (or clears when p == "") the fallback Desktop path in StateDir.
func SaveManualDesktopPath(stateDir, p string) error {
	if stateDir == "" {
		return nil
	}
	path := filepath.Join(stateDir, desktopManualFile)
	p = strings.TrimSpace(p)
	if p == "" {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(manualDesktopConfig{Path: p})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DesktopInstall describes a local DSH Desktop installation capable of launching dsh-desktop-host
// headless via ELECTRON_RUN_AS_NODE=1.
type DesktopInstall struct {
	Exe        string `json:"exe"`
	HostEntry  string `json:"hostEntry"`
	RuntimeDir string `json:"runtimeDir,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Source     string `json:"source,omitempty"` // "hello", "proc", "os", "default", "manual"
}

// DesktopDiscoverOptions configures local DSH Desktop path discovery.
type DesktopDiscoverOptions struct {
	StateDir   string
	ManualPath string
	// ScanProcs optionally inspects running local processes for dsh-desktop-host.
	ScanProcs func() (DesktopInstall, bool)
}

// DiscoverDesktop locates a local DSH Desktop installation in priority order:
//  1. Persisted plugin hello path (<StateDir>/dsh-desktop-install.json)
//  2. Running process discovery (persisted when found)
//  3. OS application index (Windows Uninstall registry / macOS LaunchServices / Linux .desktop)
//  4. OS default install paths
//  5. ManualPath fallback (if configured)
func DiscoverDesktop(o DesktopDiscoverOptions) (DesktopInstall, bool) {
	if o.StateDir != "" {
		if inst, ok := LoadDesktopInstall(o.StateDir); ok && inst.Valid() {
			if inst.Source == "" {
				inst.Source = "hello"
			}
			return inst, true
		}
	}
	if o.ScanProcs != nil {
		if inst, ok := o.ScanProcs(); ok && inst.Valid() {
			inst.Source = "proc"
			if o.StateDir != "" {
				_ = SaveDesktopInstall(o.StateDir, inst)
			}
			return inst, true
		}
	}
	if inst, ok := discoverDesktopOS(); ok && inst.Valid() {
		inst.Source = "os"
		return inst, true
	}
	for _, root := range defaultDesktopRoots() {
		if inst, ok := ResolveDesktopRoot(root); ok {
			inst.Source = "default"
			return inst, true
		}
	}
	if o.ManualPath != "" {
		if inst, ok := ResolveDesktopRoot(o.ManualPath); ok {
			inst.Source = "manual"
			return inst, true
		}
	}
	return DesktopInstall{}, false
}

// Valid reports whether Exe exists as a regular file and belongs to a valid DSH Desktop install
// (either packed app.asar + runtime/primary-runtime, or an unpacked dsh-desktop-host entry).
func (d DesktopInstall) Valid() bool {
	if d.Exe == "" {
		return false
	}
	st, err := os.Stat(d.Exe)
	if err != nil || st.IsDir() {
		return false
	}
	if _, ok := desktopResources(d.Exe); ok {
		return true
	}
	if d.HostEntry != "" {
		st2, err2 := os.Stat(d.HostEntry)
		return err2 == nil && !st2.IsDir()
	}
	return false
}

// SaveDesktopInstall persists a verified Desktop installation to <stateDir>/dsh-desktop-install.json.
func SaveDesktopInstall(stateDir string, inst DesktopInstall) error {
	if stateDir == "" || !inst.Valid() {
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(inst)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(stateDir, desktopInstallFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadDesktopInstall reads <stateDir>/dsh-desktop-install.json.
func LoadDesktopInstall(stateDir string) (DesktopInstall, bool) {
	var inst DesktopInstall
	if stateDir == "" {
		return inst, false
	}
	b, err := os.ReadFile(filepath.Join(stateDir, desktopInstallFile))
	if err != nil || json.Unmarshal(b, &inst) != nil {
		return DesktopInstall{}, false
	}
	return inst, inst.Valid()
}

// ResolveDesktopRoot inspects a candidate root directory, macOS .app bundle, or executable path
// and extracts a valid DesktopInstall if it contains an Electron executable and dsh-desktop-host.
func ResolveDesktopRoot(candidate string) (DesktopInstall, bool) {
	candidate = strings.TrimSpace(strings.Trim(candidate, `"`))
	if candidate == "" {
		return DesktopInstall{}, false
	}
	st, err := os.Stat(candidate)
	if err != nil {
		return DesktopInstall{}, false
	}
	var root, exe string
	if !st.IsDir() {
		exe = candidate
		root = filepath.Dir(candidate)
		// On macOS, .../DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness -> bundle root.
		if strings.EqualFold(filepath.Base(root), "MacOS") && strings.EqualFold(filepath.Base(filepath.Dir(root)), "Contents") {
			root = filepath.Dir(filepath.Dir(root))
		}
	} else {
		root = candidate
		exe = findDesktopExe(root)
	}
	if exe == "" {
		return DesktopInstall{}, false
	}
	hostEntry, runtimeDir, scope := findDesktopHost(root)
	if hostEntry == "" {
		return DesktopInstall{}, false
	}
	return DesktopInstall{
		Exe:        exe,
		HostEntry:  hostEntry,
		RuntimeDir: runtimeDir,
		Scope:      scope,
	}, true
}

func findDesktopExe(root string) string {
	candidates := []string{
		filepath.Join(root, "DeepSeek Harness.exe"),
		filepath.Join(root, "dsh-desktop.exe"),
		filepath.Join(root, "Contents", "MacOS", "DeepSeek Harness"),
		filepath.Join(root, "Contents", "MacOS", "dsh-desktop"),
		filepath.Join(root, "deepseek-harness"),
		filepath.Join(root, "DeepSeek Harness"),
		filepath.Join(root, "dsh-desktop"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func findDesktopHost(root string) (hostEntry, runtimeDir, scope string) {
	// Packed Electron layout (real DSH Desktop installs): resources/app.asar + resources/runtime/primary-runtime.
	// dsh-desktop-host/lib/index.js lives inside app.asar, which Electron's patched fs reads directly
	// while os.Stat cannot traverse into the archive.
	for _, res := range []string{filepath.Join(root, "resources"), filepath.Join(root, "Contents", "Resources")} {
		asar, err1 := os.Stat(filepath.Join(res, "app.asar"))
		rt, err2 := os.Stat(filepath.Join(res, "runtime", "primary-runtime"))
		if err1 == nil && asar.Mode().IsRegular() && err2 == nil && rt.IsDir() {
			sc := filepath.Join(res, "app.asar", "dsh", "node_modules", "@deepseek-ai")
			entry := filepath.Join(sc, "dsh-desktop-host", "lib", "index.js")
			return entry, filepath.Join(res, "runtime", "primary-runtime"), sc
		}
	}
	// Unpacked layout fallback (dev builds / custom setups).
	runtimeCandidates := []string{
		filepath.Join(root, "resources", "runtime"),
		filepath.Join(root, "resources", "app"),
		filepath.Join(root, "resources", "app.asar.unpacked"),
		filepath.Join(root, "Contents", "Resources", "runtime"),
		filepath.Join(root, "Contents", "Resources", "app"),
		filepath.Join(root, "Contents", "Resources", "app.asar.unpacked"),
	}
	for _, rt := range runtimeCandidates {
		sc := filepath.Join(rt, "node_modules", "@deepseek-ai")
		entry := filepath.Join(sc, "dsh-desktop-host", "lib", "index.js")
		if st, err := os.Stat(entry); err == nil && !st.IsDir() {
			return entry, rt, sc
		}
	}
	return "", "", ""
}

// DesktopInstallFromArgv extracts a DesktopInstall from a running process's executable and argv
// (e.g. from hello AgentInfo or local process inspection).
func DesktopInstallFromArgv(exe string, argv []string) (DesktopInstall, bool) {
	if exe == "" && len(argv) > 0 {
		exe = argv[0]
	}
	for _, a := range argv {
		norm := strings.ReplaceAll(a, `\`, "/")
		if strings.HasSuffix(norm, "/@deepseek-ai/dsh-desktop-host/lib/index.js") {
			inst := DesktopInstall{
				Exe:        exe,
				HostEntry:  a,
				Scope:      filepath.Dir(filepath.Dir(filepath.Dir(a))),
				RuntimeDir: filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(a))))),
			}
			if inst.Valid() {
				return inst, true
			}
		}
	}
	if exe != "" {
		return ResolveDesktopRoot(exe)
	}
	return DesktopInstall{}, false
}
