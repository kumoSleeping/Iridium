package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/satori"
)

// workspace is the id of the group arg names (ir new --workspace), as guild.list has them: its
// id, else its name (any case).
func (e *env) workspace(arg string) (string, error) {
	c := &apiCall{method: "POST", path: "/v1/guild.list", satori: true, body: map[string]any{}}
	raw, err := e.call(c)
	if err == nil {
		raw, err = e.rest(c, raw)
	}
	if err != nil {
		return "", err
	}
	var l satori.List[satori.Guild]
	if err := json.Unmarshal(raw, &l); err != nil {
		return "", err
	}
	gs := l.Data
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
