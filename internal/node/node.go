// Package node puts Iridium where another node runs (plan "节点"): iridium push sends this binary
// to an account over ssh and installs it there or restarts it; iridium node new makes a new
// account on this machine into a node of its own (sudo once), then pushes to it. Both are this
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
	// Listen is the address a node installed now listens on; "" is install's default (7810).
	Listen string
	Exe    string // the binary sent; "" is this process's own
	SSH    string // the ssh program; "" is ssh
	Out    io.Writer
}

// remote finishes a push on the other side: the binary sent runs once, takes its place in
// ~/.local/opt/iridium, and the service is restarted onto it, or installed and started on a first
// push. Holders run in units of their own and keep running.
const remote = `set -eu
opt=$HOME/.local/opt/iridium
new=$opt/.iridium-push
"$new" version
mv -f "$new" "$opt/iridium"
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

// NewOptions says which account to make a node of.
type NewOptions struct {
	User string
	// Listen is its address; "" is the first free 127.0.0.1 port from 7811.
	Listen string
	// Key is this account's key into it; "" is ~/.ssh/iridium_<user>_ed25519, made if missing.
	Key string
	Exe string
	SSH string
	Out io.Writer
	// Run runs a command with this terminal (sudo asks for the password there); nil runs it.
	Run func(ctx context.Context, stdin io.Reader, name string, args ...string) error
	// Lookup finds an account; nil is os/user.
	Lookup func(name string) (*user.User, error)
	// Push is the push at the end; nil is Push.
	Push func(context.Context, PushOptions) error
}

var userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// New makes the account o.User on this machine a node: the account (sudo useradd) unless it is
// there, linger so its services run without a login, this account's ssh key let in, then a push,
// which installs and starts Iridium there. Logging in to its agents and ir public are done in it.
func New(ctx context.Context, o NewOptions) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Run == nil {
		o.Run = run
	}
	if o.Lookup == nil {
		o.Lookup = user.Lookup
	}
	if o.Push == nil {
		o.Push = Push
	}
	if !userName.MatchString(o.User) {
		return fmt.Errorf("node new: %q is not an account name (a-z, 0-9, _ and -)", o.User)
	}
	if me, err := user.Current(); err == nil && me.Username == o.User {
		return errors.New("node new: that is this account; a node is another one")
	}
	if o.Listen == "" {
		port, err := freePort(7811)
		if err != nil {
			return err
		}
		o.Listen = fmt.Sprintf("127.0.0.1:%d", port)
	}
	home, _ := os.UserHomeDir()
	if o.Key == "" {
		o.Key = filepath.Join(home, ".ssh", "iridium_"+o.User+"_ed25519")
	}
	sudo := func(args ...string) error { return o.Run(ctx, nil, "sudo", args...) }
	fmt.Fprintf(o.Out, "把账号 %s 做成一个节点（sudo 会问一次你的密码）。\n", o.User)
	u, err := o.Lookup(o.User)
	if err != nil {
		if err := sudo("useradd", "-m", "-s", "/bin/bash", o.User); err != nil {
			return fmt.Errorf("node new: useradd: %w", err)
		}
		if u, err = o.Lookup(o.User); err != nil {
			return err
		}
		fmt.Fprintf(o.Out, "建了账号 %s（%s）。\n", o.User, u.HomeDir)
	}
	if err := sudo("loginctl", "enable-linger", o.User); err != nil {
		return fmt.Errorf("node new: linger: %w", err)
	}
	if _, err := os.Stat(o.Key + ".pub"); err != nil {
		if err := os.MkdirAll(filepath.Dir(o.Key), 0o700); err != nil {
			return err
		}
		host, _ := os.Hostname()
		me := "me"
		if cu, err := user.Current(); err == nil {
			me = cu.Username
		}
		if err := o.Run(ctx, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", me+"@"+host+" -> "+o.User, "-f", o.Key); err != nil {
			return fmt.Errorf("node new: ssh-keygen: %w", err)
		}
	}
	pub, err := os.ReadFile(o.Key + ".pub")
	if err != nil {
		return err
	}
	// Let the key in, once: ~/.ssh 0700, authorized_keys 0600, both the account's.
	const letIn = `set -eu
d=$1/.ssh f=$1/.ssh/authorized_keys
install -d -m 700 -o "$2" -g "$2" "$d"
grep -qxF "$3" "$f" 2>/dev/null || printf '%s\n' "$3" >>"$f"
chown "$2:$2" "$f"
chmod 600 "$f"`
	if err := sudo("sh", "-c", letIn, "sh", u.HomeDir, o.User, strings.TrimSpace(string(pub))); err != nil {
		return fmt.Errorf("node new: the ssh key: %w", err)
	}
	if err := o.Push(ctx, PushOptions{Target: o.User + "@localhost", Key: o.Key, Listen: o.Listen, Exe: o.Exe, SSH: o.SSH, Out: o.Out}); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, `节点 %s 跑起来了：http://%s（只在本机）。
以后推新的二进制：iridium push -i %s %s@localhost
接下来在那个账号里（ssh -i %s %s@localhost）：
  装好、登录要用的 agent（Claude Code、Codex……）
  ir public：给它一个公网地址和第一个面板
`, o.User, o.Listen, o.Key, o.User, o.Key, o.User)
	return nil
}

func run(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if stdin != nil {
		c.Stdin = stdin
	}
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
