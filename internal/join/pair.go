package join

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/config"
)

// PairUsage is ir pair's help.
const PairUsage = `ir pair [<地址>] --name <名字> (--into <文件> | --env <文件>:<变量>) [--login <ID>] [--type <类型>] [--program <程序>]
  替一个程序向 Iridium 申请接入：打印配对码，对方核对后允许，TOKEN 写进文件（0600），不打印。
  之后程序带 Authorization: Bearer <TOKEN> 访问，以登录 <ID> 说话（Satori-User-ID）。
  不给地址：本机的 Iridium（经 socket，不用打开配对）。给了地址而对方没开配对时一直等。
  --into   文件里只有 TOKEN
  --env    在 .env 文件里写入或替换一行 <变量>=<TOKEN>，如 --env .env:IR_TOKEN
  --login  随应用一并批准的登录，默认 bot；--login= 不带登录
  --type   显示用的类型，如 bot、script
`

// PairMain runs ir pair with args (after "pair"): an app's TOKEN by pairing (POST /v1/internal/pair), into
// a file; never printed, so an agent that runs it does not see it. No address: this host's Iridium,
// through its socket.
func PairMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "")
	into := fs.String("into", "", "")
	envTo := fs.String("env", "", "")
	login := fs.String("login", "bot", "")
	program := fs.String("program", "", "")
	typ := fs.String("type", "", "")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(stdout, PairUsage)
				return 0
			}
			fmt.Fprintf(stderr, "ir pair: %v\n\n%s", err, PairUsage)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) > 1 || *name == "" || (*into == "") == (*envTo == "") {
		fmt.Fprint(stderr, PairUsage)
		return 2
	}
	var path, envName string
	var err error
	if *into != "" {
		path, err = filepath.Abs(*into)
	} else {
		i := strings.LastIndexByte(*envTo, ':')
		if i <= 0 || !envVar((*envTo)[i+1:]) {
			fmt.Fprintf(stderr, "ir pair: --env <文件>:<变量>, as .env:IR_TOKEN\n")
			return 2
		}
		envName = (*envTo)[i+1:]
		path, err = filepath.Abs((*envTo)[:i])
	}
	if err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 2
	}
	o := Options{Name: *name, Bot: true, Type: *typ, Program: *program, Login: *login, Out: stdout}
	endpoint := ""
	if len(pos) == 1 {
		if o.URL, err = Address(pos[0]); err != nil {
			fmt.Fprintf(stderr, "ir pair: %v\n", err)
			return 2
		}
		endpoint = o.URL
	} else {
		// The host's Iridium: its socket asks with pairing shut; the app then speaks to its address.
		cfg := hostConfig()
		sock := cmp.Or(os.Getenv("IRIDIUM_SOCKET"), cfg.SocketPath())
		o.URL, o.Where, endpoint = "http://ir", "本机", "http://"+cfg.Listen
		o.HTTP = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	tok, err := o.pair(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 1
	}
	if envName != "" {
		err = setEnvLine(path, envName, tok)
	} else if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		err = os.WriteFile(path, []byte(tok+"\n"), 0o600)
	}
	if err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 1
	}
	where := path
	if envName != "" {
		where = path + " 的 " + envName
	}
	fmt.Fprintf(stdout, "已接入。地址 %s，TOKEN 在 %s", endpoint, where)
	if o.Login != "" {
		fmt.Fprintf(stdout, "，登录 %s", o.Login)
	}
	fmt.Fprintln(stdout)
	return 0
}

// hostConfig is this host's Iridium's config, as ir reads it (IRIDIUM_CONFIG, else the default).
func hostConfig() config.Config {
	cfg, err := config.Load(cmp.Or(os.Getenv("IRIDIUM_CONFIG"), config.DefaultPath()))
	if err != nil {
		cfg = config.Default()
	}
	return cfg
}

// envVar: name may be an environment variable's.
func envVar(name string) bool {
	for i, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return name != ""
}

// setEnvLine puts name=value into the .env file at path: the line for name replaced, else added;
// the file kept 0600, as it holds a TOKEN.
func setEnvLine(path, name, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	line := name + "=" + value
	var lines []string
	set := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if k, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(l), "export "), "="); ok && strings.TrimSpace(k) == name {
			if !set {
				lines, set = append(lines, line), true
			}
			continue
		}
		if l != "" || len(lines) > 0 {
			lines = append(lines, l)
		}
	}
	if !set {
		lines = append(lines, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
