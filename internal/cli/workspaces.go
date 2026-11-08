package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir ws: the folder groups (the user, 2026-11-08: no workspaces to make or delete; a group is the
// folder its sessions are in, made as a session goes there): listed, renamed. A group is named by
// its id or its name.

const wsUsage = "usage: ir ws [ls] | rename <组> <新名>"

func cmdWs(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil {
		return e.failf("%s", wsUsage)
	}
	verb, rest := "ls", o.pos
	if len(rest) > 0 {
		verb, rest = rest[0], rest[1:]
	}
	switch {
	case verb == "ls" && len(rest) == 0:
		return wsLs(e)
	case verb == "rename" && len(rest) == 2:
		w, err := e.workspace(rest[0])
		if err != nil {
			return e.fail(err)
		}
		if err := e.do("PATCH", fmt.Sprintf("/api/workspaces/%d", w.ID), api.WorkspacePatch{Name: &rest[1]}, nil); err != nil {
			return e.fail(err)
		}
		return exitDone
	}
	return e.failf("%s", wsUsage)
}

// ir ws ls: id, name, folder, how many sessions it holds (not the archive), one a line; a group
// with none shown is not listed, as in the sidebar.
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
		if n[w.ID] > 0 {
			fmt.Fprintf(e.out, "%d\t%s\t%s\t%d\n", w.ID, w.Name, tildeOf(w.Path, home), n[w.ID])
		}
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

// workspace is the group arg names: its id, else its name (any case).
func (e *env) workspace(arg string) (api.Workspace, error) {
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
