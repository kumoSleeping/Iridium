// Package node puts another Iridium instance in place (plan "别的机器"): iridium push sends this binary
// to an account over ssh and installs it there or restarts it; iridium node new makes a new
// account on this machine into an instance of its own (sudo once), then pushes to it. Both are this
// one binary: nothing is downloaded and no source is needed.
package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// PushOptions says where to put this binary.
type PushOptions struct {
	Target string // the ssh destination, user@host
	Key    string // the ssh identity; "" is ssh's own
	// Listen is the address an instance installed now listens on; "" is install's default (7810).
	Listen string
	Exe    string // the binary sent; "" is this process's own
	SSH    string // the ssh program; "" is ssh
	Out    io.Writer
}

// remote finishes a push on the other side: the binary sent runs once, takes its place in
// ~/.local/opt/iridium, and the service is restarted onto it, or installed and started on a first
// push: a systemd user unit on Linux, a LaunchAgent on macOS. Holders run in units of their own
// and keep running.
const remote = `set -eu
opt=$HOME/.local/opt/iridium
new=$opt/.iridium-push
"$new" version
mv -f "$new" "$opt/iridium"
# Claude Code and the like install in ~/.local/bin; Homebrew's tools are in /opt/homebrew/bin.
if [ "$(uname -s)" = Darwin ]; then
	export PATH=$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH
	uid=$(id -u)
	domain=gui/$uid
	launchctl print "$domain" >/dev/null 2>&1 || domain=user/$uid
	if launchctl print "$domain/iridium" >/dev/null 2>&1; then
		launchctl kickstart -k "$domain/iridium"
	else
		"$opt/iridium" install -now ${LISTEN:+-listen "$LISTEN"}
	fi
	for i in 1 2 3 4 5 6 7 8 9 10; do
		launchctl print "$domain/iridium" 2>/dev/null | grep -q 'state = running' && exit 0
		sleep 1
	done
	echo 'iridium is not running' >&2
	exit 1
fi
export XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}
# The agents' PATH, which the unit keeps: Claude Code and the like install in ~/.local/bin.
export PATH=$HOME/.local/bin:$PATH
if systemctl --user is-enabled -q iridium.service 2>/dev/null; then
	systemctl --user restart iridium.service
else
	"$opt/iridium" install -now ${LISTEN:+-listen "$LISTEN"}
fi
systemctl --user is-active iridium.service
`

// Push sends the binary to o.Target and makes it what runs there.
func Push(ctx context.Context, o PushOptions) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.SSH == "" {
		o.SSH = "ssh"
	}
	if o.Target == "" || strings.HasPrefix(o.Target, "-") {
		return errors.New("push: an ssh destination such as ir@localhost")
	}
	exe, err := own(o.Exe)
	if err != nil {
		return err
	}
	ssh := func(stdin io.Reader, out io.Writer, cmd string) error {
		args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new"}
		if o.Key != "" {
			args = append(args, "-i", o.Key, "-o", "IdentitiesOnly=yes")
		}
		c := exec.CommandContext(ctx, o.SSH, append(args, o.Target, cmd)...)
		var errb bytes.Buffer
		c.Stdin, c.Stdout, c.Stderr = stdin, out, &errb
		if err := c.Run(); err != nil {
			return fmt.Errorf("push to %s: %v: %s", o.Target, err, strings.TrimSpace(errb.String()))
		}
		return nil
	}
	var uname bytes.Buffer
	if err := ssh(nil, &uname, "uname -sm"); err != nil {
		return err
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; platformOf(uname.String()) != want {
		return fmt.Errorf("push: %s is %q; this binary is for %s", o.Target, strings.TrimSpace(uname.String()), want)
	}
	f, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(o.Out, "把 %s 发到 %s……\n", exe, o.Target)
	if err := ssh(f, io.Discard, "mkdir -p ~/.local/opt/iridium && cat > ~/.local/opt/iridium/.iridium-push && chmod 755 ~/.local/opt/iridium/.iridium-push"); err != nil {
		return err
	}
	cmd := "sh -s"
	if o.Listen != "" {
		cmd = "LISTEN=" + shellQuote(o.Listen) + " sh -s"
	}
	return ssh(strings.NewReader(remote), o.Out, cmd)
}

// platformOf is GOOS/GOARCH of uname -sm.
func platformOf(uname string) string {
	f := strings.Fields(uname)
	if len(f) != 2 {
		return ""
	}
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[f[1]]
	return strings.ToLower(f[0]) + "/" + arch
}

func own(exe string) (string, error) {
	if exe != "" {
		return exe, nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	return exe, err
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// NewOptions says which account to install an instance in.
type NewOptions struct {
	User string
	// Listen is its address; "" is the first free 127.0.0.1 port from 7811.
	Listen string
	// Key is this account's key into it; "" is ~/.ssh/iridium_<user>_ed25519, made if missing.
	Key string
	Exe string
	SSH string
	Out io.Writer
	// Elevate is how the root part runs: "root" (this is root), "sudo", or "su" (root's
	// password); "" picks: root, sudo for a member of sudo, wheel or admin, else su.
	Elevate string
	// Run runs a command with this terminal (sudo or su asks for the password there); nil runs it.
	Run func(ctx context.Context, name string, args ...string) error
	// Push is the push at the end; nil is Push.
	Push func(context.Context, PushOptions) error
}

var userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// rootPart is all New needs root for, run once ($1 the account, $2 the key): the account unless
// it is there, linger so its services run without a login, the key let in (~/.ssh 0700,
// authorized_keys 0600, the account's).
const rootPart = `set -eu
# su -c keeps the caller's PATH, which often lacks the sbin directories.
PATH=$PATH:/usr/local/sbin:/usr/sbin:/sbin
u=$1 pub=$2
id "$u" >/dev/null 2>&1 || useradd -m -s /bin/bash "$u"
loginctl enable-linger "$u"
home=$(getent passwd "$u" | cut -d: -f6)
install -d -m 700 -o "$u" -g "$u" "$home/.ssh"
f=$home/.ssh/authorized_keys
grep -qxF "$pub" "$f" 2>/dev/null || printf '%s\n' "$pub" >>"$f"
chown "$u:$u" "$f"
chmod 600 "$f"
echo "账号 $u 好了（$home）。"`

// New makes the account o.User on this machine an instance: its root part (rootPart) with one
// password, then a push, which installs and starts Iridium there. Logging in to its agents and
// ir public are done in it.
func New(ctx context.Context, o NewOptions) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Run == nil {
		o.Run = run
	}
	if o.Push == nil {
		o.Push = Push
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("node new: Linux only (useradd, systemd); on %s make the account by hand and run iridium push <account>@localhost", runtime.GOOS)
	}
	if !userName.MatchString(o.User) {
		return fmt.Errorf("node new: %q is not an account name (a-z, 0-9, _ and -)", o.User)
	}
	me, err := user.Current()
	if err != nil {
		return err
	}
	if me.Username == o.User {
		return errors.New("node new: that is this account; an instance goes in another one")
	}
	if o.Elevate == "" {
		o.Elevate = elevation(me)
	}
	if o.Listen == "" {
		port, err := freePort(7811)
		if err != nil {
			return err
		}
		o.Listen = fmt.Sprintf("127.0.0.1:%d", port)
	}
	if o.Key == "" {
		o.Key = filepath.Join(me.HomeDir, ".ssh", "iridium_"+o.User+"_ed25519")
	}
	if _, err := os.Stat(o.Key + ".pub"); err != nil {
		if err := os.MkdirAll(filepath.Dir(o.Key), 0o700); err != nil {
			return err
		}
		host, _ := os.Hostname()
		if err := o.Run(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", me.Username+"@"+host+" -> "+o.User, "-f", o.Key); err != nil {
			return fmt.Errorf("node new: ssh-keygen: %w", err)
		}
	}
	pub, err := os.ReadFile(o.Key + ".pub")
	if err != nil {
		return err
	}
	args := []string{"sh", "-c", rootPart, "sh", o.User, strings.TrimSpace(string(pub))}
	switch o.Elevate {
	case "root":
		err = o.Run(ctx, args[0], args[1:]...)
	case "sudo":
		fmt.Fprintf(o.Out, "给账号 %s 装一个 Iridium 实例：要 root，sudo 会问你的密码。\n", o.User)
		err = o.Run(ctx, "sudo", args...)
	case "su":
		fmt.Fprintf(o.Out, "给账号 %s 装一个 Iridium 实例：要 root，这个账号不能 sudo，su 会问 root 的密码。\n", o.User)
		q := make([]string, len(args))
		for i, a := range args {
			q[i] = shellQuote(a)
		}
		err = o.Run(ctx, "su", "-c", strings.Join(q, " "), "root")
	default:
		return fmt.Errorf("node new: no way %q to root", o.Elevate)
	}
	if err != nil {
		return fmt.Errorf("node new: the root part (%s): %w", o.Elevate, err)
	}
	if err := o.Push(ctx, PushOptions{Target: o.User + "@localhost", Key: o.Key, Listen: o.Listen, Exe: o.Exe, SSH: o.SSH, Out: o.Out}); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, `实例 %s 跑起来了：http://%s（只在本机）。
以后推新的二进制：iridium push -i %s %s@localhost
接下来在那个账号里（ssh -i %s %s@localhost）：
  装好、登录要用的 agent（Claude Code、Codex……）
  ir public：给它一个公网地址，最后给你第一个面板的邀请
  已经有公网地址的（配置里有 public.origin）：ir invite --owner 拿邀请
`, o.User, o.Listen, o.Key, o.User, o.Key, o.User)
	return nil
}

// elevation is how this account gets root: it is root, it may sudo (in sudo, wheel or admin), or su.
func elevation(me *user.User) string {
	if me.Uid == "0" {
		return "root"
	}
	ids, _ := me.GroupIds()
	for _, id := range ids {
		if g, err := user.LookupGroupId(id); err == nil && (g.Name == "sudo" || g.Name == "wheel" || g.Name == "admin") {
			return "sudo"
		}
	}
	return "su"
}

func run(ctx context.Context, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// freePort is the first port from p that nothing listens on at 127.0.0.1.
func freePort(p int) (int, error) {
	for ; p < 7900; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			l.Close()
			return p, nil
		}
	}
	return 0, errors.New("node new: no free port from 7811 to 7899")
}
