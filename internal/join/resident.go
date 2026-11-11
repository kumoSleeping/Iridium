package join

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

// resident is ir join started at login, as this system does it (docs/exec.md "平台差异"):
// Windows, a scheduled task at the user's logon (not a service: the SYSTEM account has neither
// the user's PATH nor the agents' logins); macOS, a launchd LaunchAgent; Linux, a systemd user
// unit. It runs with --resident: it never pairs (no one is there to match the code) and exits
// cleanly once the TOKEN is gone, so it is not started again and again.
type resident struct {
	goos string
	name string   // the unit, task or label
	argv []string // what runs
	url  string
	path string // PATH it runs with (launchd, systemd)
	log  string // where it writes; "" the journal (Linux)
	dir  string // where its file goes: the systemd user units, LaunchAgents, the state (Windows)
	user string // Windows: DOMAIN\user, whose logon starts it
}

// runner runs a command, failing with its output.
type runner func(name string, args ...string) error

func runCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

func residentFor(o Options, unit string) resident {
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
	}
	r := resident{goos: runtime.GOOS, name: unit, url: o.URL, path: os.Getenv("PATH")}
	if r.name == "" {
		r.name = "iridium-join-" + o.key()
	}
	r.argv = []string{exe, "join", o.URL, "--resident", "--state", o.Base}
	if o.Name != "" {
		r.argv = append(r.argv, "--name", o.Name)
	}
	if len(o.Roots) > 0 {
		r.argv = append(r.argv, "--roots", strings.Join(o.Roots, ","))
	}
	if o.Shell {
		r.argv = append(r.argv, "--shell")
	}
	home, _ := os.UserHomeDir()
	switch r.goos {
	case "windows":
		r.dir = o.stateDir()
		r.log = filepath.Join(r.dir, "join.log")
		r.argv = append(r.argv, "--log", r.log)
		if u, err := user.Current(); err == nil {
			r.user = u.Username
		}
	case "darwin":
		r.dir = filepath.Join(home, "Library", "LaunchAgents")
		r.log = filepath.Join(o.stateDir(), "join.log")
	default:
		r.dir = filepath.Join(home, ".config", "systemd", "user")
		if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
			r.dir = filepath.Join(d, "systemd", "user")
		}
	}
	return r
}

// file is where its definition goes.
func (r resident) file() string {
	switch r.goos {
	case "windows":
		return filepath.Join(r.dir, "task.xml")
	case "darwin":
		return filepath.Join(r.dir, r.name+".plist")
	}
	return filepath.Join(r.dir, r.name+".service")
}

func (r resident) install(run runner) error {
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	if r.log != "" {
		if err := os.MkdirAll(filepath.Dir(r.log), 0o700); err != nil {
			return err
		}
	}
	switch r.goos {
	case "windows":
		if err := os.WriteFile(r.file(), r.taskXML(), 0o600); err != nil {
			return err
		}
		defer os.Remove(r.file())
		if err := run("schtasks", "/Create", "/TN", r.name, "/XML", r.file(), "/F"); err != nil {
			return err
		}
		_ = run("schtasks", "/End", "/TN", r.name)
		return run("schtasks", "/Run", "/TN", r.name)
	case "darwin":
		if err := os.WriteFile(r.file(), []byte(r.plist()), 0o644); err != nil {
			return err
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = run("launchctl", "bootout", domain+"/"+r.name)
		return run("launchctl", "bootstrap", domain, r.file())
	}
	if err := os.WriteFile(r.file(), []byte(r.unit()), 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "enable", r.name+".service"); err != nil {
		return err
	}
	return run("systemctl", "--user", "restart", r.name+".service")
}

// errNotInstalled: there is nothing to uninstall.
var errNotInstalled = errors.New("未设为登录时运行")

func (r resident) uninstall(run runner) error {
	switch r.goos {
	case "windows":
		_ = run("schtasks", "/End", "/TN", r.name)
		if err := run("schtasks", "/Delete", "/TN", r.name, "/F"); err != nil {
			return fmt.Errorf("%w: %v", errNotInstalled, err)
		}
		return nil
	case "darwin":
		if _, err := os.Stat(r.file()); err != nil {
			return errNotInstalled
		}
		_ = run("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+r.name)
		return os.Remove(r.file())
	}
	if _, err := os.Stat(r.file()); err != nil {
		return errNotInstalled
	}
	_ = run("systemctl", "--user", "disable", "--now", r.name+".service")
	if err := os.Remove(r.file()); err != nil {
		return err
	}
	return run("systemctl", "--user", "daemon-reload")
}

// unit is the systemd user unit.
func (r resident) unit() string {
	words := make([]string, len(r.argv))
	for i, a := range r.argv {
		words[i] = unitQuote(a)
	}
	env := ""
	if r.path != "" {
		env = "Environment=" + unitQuote("PATH="+r.path) + "\n"
	}
	return fmt.Sprintf(`# Written by ir join.
[Unit]
Description=Iridium executor for %s

[Service]
Type=exec
ExecStart=%s
%sRestart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, r.url, strings.Join(words, " "), env)
}

// unitQuote quotes a word for a unit file when it needs it.
func unitQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\%$;") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// plist is the LaunchAgent: at login, again if it fails.
func (r resident) plist() string {
	var args strings.Builder
	for _, a := range r.argv {
		args.WriteString("\n\t\t<string>" + xmlText(a) + "</string>")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, xmlText(r.name), args.String(), xmlText(r.path), xmlText(r.log), xmlText(r.log))
}

// taskXML is the scheduled task, as schtasks /XML reads it (UTF-16): at this user's logon, as
// this user, no time limit, again if it fails. conhost --headless gives it a console no one sees,
// which the agents it starts share (no window each).
func (r resident) taskXML() []byte {
	conhost := "conhost.exe"
	if d := os.Getenv("SystemRoot"); d != "" {
		conhost = filepath.Join(d, "System32", "conhost.exe")
	}
	args := []string{"--headless"}
	for _, a := range r.argv {
		args = append(args, winQuote(a))
	}
	who := ""
	if r.user != "" {
		who = "<UserId>" + xmlText(r.user) + "</UserId>"
	}
	s := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Iridium executor for %s (ir join)</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      %s
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      %s
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlText(r.url), who, who, xmlText(conhost), xmlText(strings.Join(args, " ")))
	s = strings.ReplaceAll(s, "\n", "\r\n")
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2+2*len(u))
	binary.LittleEndian.PutUint16(b, 0xFEFF)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2+2*i:], c)
	}
	return b
}

// winQuote quotes an argument for a Windows command line, as CommandLineToArgvW reads it.
func winQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, c := range s {
		switch c {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(c)
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}
