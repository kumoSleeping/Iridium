package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// ir ws rename <group> <name>: a folder group renamed (the user, 2026-11-08: no workspaces to make
// or delete; a group is the folder its sessions are in). ir api guild.list lists them; a group is
// named by its id or its name.

const wsUsage = "usage: ir ws rename <组> <新名>"

func cmdWs(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil || len(o.pos) != 3 || o.pos[0] != "rename" {
		return e.failf("%s", wsUsage)
	}
	id, err := e.workspace(o.pos[1])
	if err != nil {
		return e.fail(err)
	}
	if err := e.do("PATCH", "/v1/internal/workspaces/"+id, api.WorkspacePatch{Name: &o.pos[2]}, nil); err != nil {
		return e.fail(err)
	}
	return exitDone
}

func tildeOf(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// workspace is the id of the group arg names, as guild.list has them: its id, else its name (any
// case).
func (e *env) workspace(arg string) (string, error) {
	raw, err := e.satori("guild.list", nil)
	if err != nil {
		return "", err
	}
	gs, err := allPages(e, &apiCall{method: "guild.list", body: map[string]any{}}, raw, func(b []byte) ([]satori.Guild, string, error) {
		var l satori.List[satori.Guild]
		return l.Data, l.Next, json.Unmarshal(b, &l)
	})
	if err != nil {
		return "", err
	}
	gs = slices.DeleteFunc(gs, func(g satori.Guild) bool { return g.ID == satori.Loose })
	for _, g := range gs {
		if g.ID == arg {
			return g.ID, nil
		}
	}
	var hits []string
	for _, g := range gs {
		if strings.EqualFold(g.Name, arg) {
			hits = append(hits, g.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no workspace %s", arg)
	case 1:
		return hits[0], nil
	}
	return "", errors.New(arg + " names " + strconv.Itoa(len(hits)) + " workspaces; give its id (ir api guild.list)")
}
