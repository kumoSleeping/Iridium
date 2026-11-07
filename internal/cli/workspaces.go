package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir ws: the workspaces, as the sidebar's "+" and a workspace's menu make, rename and delete them,
// and a session moved into one, as dragging it does. A workspace is named by its id or its name.

const wsUsage = "usage: ir ws [ls] | new <目录> [--name 名] | rename <ws> <新名> | rm <ws> | mv <id> <ws>"

func cmdWs(e *env, args []string) int {
	o, err := parse(args, "name")
	if err != nil {
		return e.failf("%s", wsUsage)
	}
	verb, rest := "ls", o.pos
	if len(rest) > 0 {
		verb, rest = rest[0], rest[1:]
	}
	if o.name != "" && verb != "new" {
		return e.failf("%s", wsUsage)
	}
	switch {
	case verb == "ls" && len(rest) == 0:
		return wsLs(e)
	case verb == "new" && len(rest) == 1:
		path, err := localPath(rest[0])
		if err != nil {
			return e.fail(err)
		}
		var w api.Workspace
		if err := e.do("POST", "/api/workspaces", api.NewWorkspace{Path: path, Name: o.name}, &w); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.out, "%d\t%s\n", w.ID, w.Name)
		return exitDone
	case verb == "rename" && len(rest) == 2:
		w, err := e.workspace(rest[0], false)
		if err != nil {
			return e.fail(err)
		}
		if err := e.do("PATCH", fmt.Sprintf("/api/workspaces/%d", w.ID), api.WorkspacePatch{Name: rest[1]}, nil); err != nil {
			return e.fail(err)
		}
		return exitDone
	case verb == "rm" && len(rest) == 1:
		w, err := e.workspace(rest[0], false)
		if err != nil {
			return e.fail(err)
		}
		if err := e.do("DELETE", fmt.Sprintf("/api/workspaces/%d", w.ID), nil, nil); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.out, "删了 %s，会话在未归类。\n", w.Name)
		return exitDone
	case verb == "mv" && len(rest) == 2:
		id, err := e.resolve(rest[0])
		if err != nil {
			return e.fail(err)
		}
		w, err := e.workspace(rest[1], true)
		if err != nil {
			return e.fail(err)
		}
		if err := e.do("PATCH", "/api/sessions/"+id, api.SessionPatch{Workspace: &w.ID}, nil); err != nil {
			return e.fail(err)
		}
		return exitDone
	}
	return e.failf("%s", wsUsage)
}

// ir ws ls: id, name, folder, how many sessions it holds (not the archive), one a line.
func wsLs(e *env) int {
	var list []api.Workspace
	if err := e.do("GET", "/api/workspaces", nil, &list); err != nil {
		return e.fail(err)
	}
	var sessions []api.Session
	if err := e.do("GET", "/api/sessions", nil, &sessions); err != nil {
		return e.fail(err)
	}
	n := map[int64]int{}
	for _, s := range sessions {
		if !s.Archived && !s.Subagent {
			n[s.Workspace]++
		}
	}
	home, _ := os.UserHomeDir()
	for _, w := range list {
		fmt.Fprintf(e.out, "%d\t%s\t%s\t%d\n", w.ID, w.Name, tildeOf(w.Path, home), n[w.ID])
	}
	if n[0] > 0 {
		fmt.Fprintf(e.out, "0\t未归类\t\t%d\n", n[0])
	}
	return exitDone
}

func tildeOf(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// workspace is the one arg names: its id, else its name (any case); none (0, 未归类) only where
// it may be a session's.
func (e *env) workspace(arg string, none bool) (api.Workspace, error) {
	if none && (arg == "0" || arg == "未归类") {
		return api.Workspace{ID: 0, Name: "未归类"}, nil
	}
	var list []api.Workspace
	if err := e.do("GET", "/api/workspaces", nil, &list); err != nil {
		return api.Workspace{}, err
	}
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		for _, w := range list {
			if w.ID == id {
				return w, nil
			}
		}
	}
	var hits []api.Workspace
	for _, w := range list {
		if strings.EqualFold(w.Name, arg) {
			hits = append(hits, w)
		}
	}
	switch len(hits) {
	case 0:
		return api.Workspace{}, fmt.Errorf("no workspace %s", arg)
	case 1:
		return hits[0], nil
	}
	return api.Workspace{}, errors.New(arg + " names " + strconv.Itoa(len(hits)) + " workspaces; give its id (ir ws ls)")
}
