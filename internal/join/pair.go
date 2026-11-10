package join

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
)

// PairUsage is ir pair's help.
const PairUsage = `ir pair <地址> --name <名字> --into <文件> [--type <类型>] [--program <程序>]
  向那个 Iridium 申请接入一个应用：打印配对码，对方核对后允许，
  TOKEN 写进 --into 的文件（0600），不打印。之后程序带 Authorization: Bearer <TOKEN> 访问。
  对方没开放配对时一直等（ir apps open，或应用管理里的“新建应用”）。
  --type  显示用的类型，如 bot、script
`

// PairMain runs ir pair with args (after "pair"): an app's TOKEN by pairing (POST /api/pair), into
// a file; never printed, so an agent that runs it does not see it.
func PairMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "")
	into := fs.String("into", "", "")
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
	if len(pos) != 1 || *name == "" || *into == "" {
		fmt.Fprint(stderr, PairUsage)
		return 2
	}
	u, err := Address(pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 2
	}
	path, err := filepath.Abs(*into)
	if err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	o := Options{URL: u, Name: *name, Bot: true, Type: *typ, Program: *program, Out: stdout}
	tok, err := o.pair(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 1
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		fmt.Fprintf(stderr, "ir pair: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "已接入 %s，TOKEN 在 %s\n", u, path)
	return 0
}
