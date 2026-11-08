// Package launchd is macOS's counterpart of a systemd user unit: a LaunchAgent, a plist in
// ~/Library/LaunchAgents that launchd starts at login and keeps running, loaded and unloaded with
// launchctl. Iridium's daemon (iridium install), its tunnel (ir public) and the QQ plugin run as
// one on macOS.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Job is one LaunchAgent.
type Job struct {
	Label string
	Argv  []string // absolute path first
	Env   []string // NAME=VALUE
	Log   string   // where its stdout and stderr go; "" nowhere
	// Always restarts it however it ends; false only when it fails (systemd's Restart=on-failure).
	Always bool
}

// Plist is the job's property list. The processes it leaves (Iridium's holders) are not killed
// with it, and it runs as an app does: not throttled as a background job is.
func (j Job) Plist() string {
	var b strings.Builder
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	str := func(s string) { fmt.Fprintf(&b, "\t\t<string>%s</string>\n", esc(s)) }
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by Iridium. -->
<plist version="1.0">
<dict>
`)
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", esc(j.Label))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range j.Argv {
		str(a)
	}
	b.WriteString("\t</array>\n")
	if len(j.Env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, e := range j.Env {
			k, v, _ := strings.Cut(e, "=")
			fmt.Fprintf(&b, "\t\t<key>%s</key>\n", esc(k))
			str(v)
		}
		b.WriteString("\t</dict>\n")
	}
	if j.Log != "" {
		fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%[1]s</string>\n\t<key>StandardErrorPath</key>\n\t<string>%[1]s</string>\n", esc(j.Log))
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	if j.Always {
		b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	} else {
		b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	}
	b.WriteString(`	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`)
	return b.String()
}

// Dir is where the user's agents are: ~/Library/LaunchAgents.
func Dir(home string) string { return filepath.Join(home, "Library", "LaunchAgents") }

// File is the plist of label in dir.
func File(dir, label string) string { return filepath.Join(dir, label+".plist") }

// LogFile is where a job of Iridium's writes: ~/Library/Logs/<label>.log.
func LogFile(home, label string) string {
	return filepath.Join(home, "Library", "Logs", label+".log")
}

// Run runs launchctl with args, its complaint in the error.
func Run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Domain is where the user's agents load: gui/<uid> while the user is logged in at the screen,
// else user/<uid> (over ssh, say).
func Domain(ctx context.Context) string {
	uid := strconv.Itoa(os.Getuid())
	if exec.CommandContext(ctx, "launchctl", "print", "gui/"+uid).Run() == nil {
		return "gui/" + uid
	}
	return "user/" + uid
}

// Launchctl runs launchctl with args (Run, or a fake in tests).
type Launchctl func(ctx context.Context, args ...string) error

// Write writes the job's plist into dir, made if missing, and gives its path.
func Write(dir string, j Job) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if j.Log != "" {
		if err := os.MkdirAll(filepath.Dir(j.Log), 0o755); err != nil {
			return "", err
		}
	}
	path := File(dir, j.Label)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(j.Plist()), 0o644); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// Load (re)loads the plist at path into domain, which starts it: any job of its label loaded
// before is unloaded first.
func Load(ctx context.Context, run Launchctl, domain, label, path string) error {
	_ = run(ctx, "bootout", domain+"/"+label)
	return run(ctx, "bootstrap", domain, path)
}

// Unload stops the job and unloads it; one not loaded is no error.
func Unload(ctx context.Context, run Launchctl, domain, label string) {
	_ = run(ctx, "bootout", domain+"/"+label)
}

// Restart kills the job and starts it again at once.
func Restart(ctx context.Context, run Launchctl, domain, label string) error {
	return run(ctx, "kickstart", "-k", domain+"/"+label)
}

// State is the job's state as launchctl print says it (running, not running...); "" when it is
// not loaded.
func State(ctx context.Context, domain, label string) string {
	out, err := exec.CommandContext(ctx, "launchctl", "print", domain+"/"+label).Output()
	if err != nil {
		return ""
	}
	for l := range strings.Lines(string(out)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "state = "); ok {
			return strings.TrimSpace(v)
		}
	}
	return "loaded"
}
