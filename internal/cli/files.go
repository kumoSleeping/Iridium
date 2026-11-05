package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir cp <from> <to>: Iridium copies, ir only watches; run again after an interruption, it goes
// on where it was. One line at the end: path, size, sha256.
func cmdCp(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 2 {
		return e.failf("usage: ir cp <源> <目标>")
	}
	from, err := localPath(o.pos[0])
	if err != nil {
		return e.fail(err)
	}
	to, err := localPath(o.pos[1])
	if err != nil {
		return e.fail(err)
	}
	resp, err := e.request("POST", "/api/files/copy", api.Copy{From: from, To: to})
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

// ir files <dir>: one level, one entry a line: name, size, time, type.
func cmdFiles(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) > 1 {
		return e.failf("usage: ir files <目录>")
	}
	dir := "."
	if len(o.pos) == 1 {
		dir = o.pos[0]
	}
	path, err := localPath(dir)
	if err != nil {
		return e.fail(err)
	}
	var l api.DirListing
	if err := e.do("GET", "/api/files"+query("path", path), nil, &l); err != nil {
		return e.fail(err)
	}
	for _, en := range l.Entries {
		name := en.Name
		if en.Type == "dir" {
			name += "/"
		}
		fmt.Fprintf(e.out, "%s\t%d\t%s\t%s\n", name, en.Size, time.UnixMilli(en.Mtime).Format("2006-01-02 15:04"), en.Type)
	}
	return exitDone
}
