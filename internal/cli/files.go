package cli

import (
	"bufio"
	"encoding/json"
	"fmt"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir cp <from> <to>: Iridium copies, ir only watches; run again after an interruption, it goes
// on where it was. One line at the end: path, size, sha256.
func cmdCp(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 2 {
		return e.failf("usage: ir cp <源> <目标>")
	}
	var req api.Copy
	if req.FromMachine, req.From, err = pathArg(o.pos[0]); err != nil {
		return e.fail(err)
	}
	if req.ToMachine, req.To, err = pathArg(o.pos[1]); err != nil {
		return e.fail(err)
	}
	resp, err := e.request("POST", "/v1/internal/files/copy", req)
	if err != nil {
		return e.fail(err)
	}
	defer resp.Body.Close()
	p := e.progress()
	defer p.clear()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var line api.CopyProgress
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		switch {
		case line.Error != "":
			p.clear()
			return e.failf("%s", line.Error)
		case line.Path != "":
			p.clear()
			fmt.Fprintf(e.out, "%s\t%d\t%s\n", line.Path, line.Size, line.Sha256)
			return exitDone
		default:
			p.bar(line.Done, line.Size)
		}
	}
	p.clear()
	return e.failf("the copy's progress stopped short; run the same command again to go on")
}

// ir attach <path>...: each file a link for a reply (docs/api.md "文件链接"), one line each: the
// Markdown to put in the reply, then its kind and size. A file that cannot be given says why, and
// the rest still are.
func cmdAttach(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) == 0 {
		return e.failf("usage: ir attach <文件>...")
	}
	code := exitDone
	for _, a := range o.pos {
		p, err := localPath(a)
		if err == nil {
			var l api.FileLink
			if err = e.do("POST", "/v1/internal/links", api.AttachPath{Path: p}, &l); err == nil {
				what := string(l.File.Kind)
				if l.File.W > 0 && l.File.H > 0 {
					what += fmt.Sprintf(" %d×%d", l.File.W, l.File.H)
				}
				fmt.Fprintf(e.out, "%s\t%s\t%s\n", l.Markdown, what, sizeOf(l.File.Size))
				continue
			}
		}
		fmt.Fprintf(e.err, "ir attach: %s: %v\n", a, err)
		code = exitError
	}
	return code
}

// sizeOf is n bytes as a person reads it.
func sizeOf(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}
