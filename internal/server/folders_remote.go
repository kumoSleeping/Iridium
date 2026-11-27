package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Folders at another machine's executor (docs/exec.md), for the folder picker: the page sees its
// paths '/'-separated (C:/Users/kumo), the executor gets its own separator. "" and "~" are its home.

// machineAt is executor mid, connected; it has answered when nil.
func (s *Server) machineAt(w http.ResponseWriter, mid string) (*remote.Machine, *wire.Hello) {
	if s.Remote == nil {
		refuse(w, http.StatusUnprocessableEntity, "not_here", "no executors here")
		return nil, nil
	}
	mc := s.Remote.Find(mid)
	if mc == nil {
		refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such computer")
		return nil, nil
	}
	hi, ok := mc.Hello()
	if !ok {
		fail(w, http.StatusConflict, "machine not connected")
		return nil, nil
	}
	return mc, hi
}

// nativePath is page path p as the machine writes it: its separator, ~ for its home, a bare drive
// its root.
func nativePath(hi *wire.Hello, p string) string {
	switch {
	case p == "" || p == "~":
		return hi.Home
	case strings.HasPrefix(p, "~/"):
		p = strings.TrimSuffix(hi.Home, hi.Sep) + "/" + p[2:]
	}
	if hi.Sep == `\` {
		p = strings.ReplaceAll(p, "/", `\`)
		if len(p) == 2 && p[1] == ':' {
			p += `\`
		}
	}
	return p
}

func pagePath(hi *wire.Hello, p string) string {
	if hi.Sep == `\` {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}

// parentPath is p's parent there, "" at a root.
func parentPath(hi *wire.Hello, p string) string {
	t := strings.TrimSuffix(p, hi.Sep)
	i := strings.LastIndex(t, hi.Sep)
	switch {
	case i < 0 || t == "" || hi.Sep == `\` && len(t) == 2 && t[1] == ':':
		return ""
	case i == 0:
		return hi.Sep
	case hi.Sep == `\` && i == 2 && t[1] == ':':
		return t[:3]
	}
	return t[:i]
}

// pageMachinePath is executor mid, connected, and page path as that machine writes it (~ for its
// home, absolute); it has answered when nil.
func (s *Server) pageMachinePath(w http.ResponseWriter, mid, path string) (*remote.Machine, *wire.Hello, string) {
	mc, hi := s.machineAt(w, mid)
	if mc == nil {
		return nil, nil, ""
	}
	dir := nativePath(hi, path)
	if !remote.IsAbs(hi.Sep, dir) {
		fail(w, http.StatusBadRequest, "not an absolute path")
		return nil, nil, ""
	}
	return mc, hi, dir
}

func (s *Server) remoteFolders(w http.ResponseWriter, r *http.Request, mid, path string) {
	mc, hi, dir := s.pageMachinePath(w, mid, path)
	if mc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ents, err := mc.List(ctx, dir)
	if err != nil {
		s.remoteError(w, err)
		return
	}
	out := api.Folders{Path: pagePath(hi, dir), Folders: []string{}}
	for _, e := range ents {
		if e.Type == "dir" && !strings.HasPrefix(e.Name, ".") {
			out.Folders = append(out.Folders, e.Name)
		}
	}
	slices.SortFunc(out.Folders, func(a, b string) int {
		if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	if p := parentPath(hi, dir); p != "" {
		out.Parent = pagePath(hi, p)
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) remoteMkdir(w http.ResponseWriter, r *http.Request, req api.NewFolder) {
	mc, hi := s.machineAt(w, req.Machine)
	if mc == nil {
		return
	}
	if req.Name == "" || req.Name == "." || req.Name == ".." || strings.ContainsAny(req.Name, `/\:*?"<>|`+"\x00") {
		fail(w, http.StatusBadRequest, "not a folder name")
		return
	}
	dir := nativePath(hi, req.Path)
	if !remote.IsAbs(hi.Sep, dir) {
		fail(w, http.StatusBadRequest, "not an absolute path")
		return
	}
	p := remote.Join(hi.Sep, dir, req.Name)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := mc.Stat(ctx, p); err == nil {
		fail(w, http.StatusConflict, "already there")
		return
	}
	if err := mc.Mkdir(ctx, p); err != nil {
		s.remoteError(w, err)
		return
	}
	reply(w, http.StatusOK, api.FolderReply{Path: pagePath(hi, p)})
}

// agentWorkspaces is GET /v1/internal/computers/{id}/{agent}/workspaces: the folders a new session
// of agent there may open in, the host's own workspaces first.
func (s *Server) agentWorkspaces(w http.ResponseWriter, r *http.Request) {
	mc, hi := s.machineAt(w, r.PathValue("id"))
	if mc == nil {
		return
	}
	if s.Discover == nil {
		reply(w, http.StatusOK, []api.DshWorkspace{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	ws, err := s.Discover.DshWorkspaces(ctx, mc.ID())
	if err != nil {
		s.remoteError(w, err)
		return
	}
	for i := range ws {
		ws[i].Path = pagePath(hi, ws[i].Path)
	}
	reply(w, http.StatusOK, ws)
}

// agentSync is POST /v1/internal/computers/{id}/{agent}/sync: refreshes workspaces, models, and
// presets for agent at the remote computer.
func (s *Server) agentSync(w http.ResponseWriter, r *http.Request) {
	mc, _ := s.machineAt(w, r.PathValue("id"))
	if mc == nil {
		return
	}
	ag := api.Agent(r.PathValue("agent"))
	if _, ok := mc.Agent(string(ag)); !ok {
		refuse(w, http.StatusUnprocessableEntity, "no_such_agent", "no "+string(ag)+" at "+mc.ID())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.m.Sync(ctx, ag, mc.ID()); err != nil {
		s.remoteError(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// putAgentConfig is PUT /v1/internal/computers/{id}/{agent}/config: updates per-computer agent
// configuration (e.g. "sync new sessions" toggle or host manual app path fallback).
func (s *Server) putAgentConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	machine := id
	if id == api.HostID {
		machine = ""
	} else {
		if info, ok := s.gate().Find(id); !ok || s.computerOf(info.ID) == nil {
			refuse(w, http.StatusUnprocessableEntity, "no_such_computer", "no such computer")
			return
		} else {
			machine = info.ID
		}
	}
	ag := api.Agent(r.PathValue("agent"))
	if _, ok := agents.Get(ag); !ok {
		refuse(w, http.StatusUnprocessableEntity, "no_such_agent", "no agent "+string(ag))
		return
	}
	var req api.ComputerAgentConfig
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	st, err := s.m.SetAgentComputerConfig(ctx, ag, machine, harness.ComputerConfig{Sync: req.Sync, Path: req.Path})
	if err != nil {
		if errors.Is(err, harness.ErrBadRequest) {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		s.error(w, err)
		return
	}
	s.terminalsChanged()
	reply(w, http.StatusOK, api.ComputerAgentState{Sync: st.Sync, PathNeeded: st.PathNeeded, Path: st.Path})
}

// searchExternal is GET /v1/internal/external/sessions?q&limit: searches unsynced sessions in
// external host apps on demand.
func (s *Server) searchExternal(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, err := s.m.SearchExternal(ctx, q, limit)
	if err != nil {
		s.error(w, err)
		return
	}
	if items == nil {
		items = []api.ExternalSession{}
	}
	reply(w, http.StatusOK, items)
}

// adoptExternal is POST /v1/internal/external/adopt: brings an unsynced external session picked in
// search into Iridium's session list.
func (s *Server) adoptExternal(w http.ResponseWriter, r *http.Request) {
	var req api.ExternalSession
	if !readJSON(w, r, &req) {
		return
	}
	if req.ID == "" || req.Agent == "" {
		fail(w, http.StatusBadRequest, "id and agent required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.m.AdoptExternal(ctx, req); err != nil {
		switch {
		case errors.Is(err, harness.ErrBadRequest):
			fail(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, os.ErrNotExist):
			refuse(w, http.StatusUnprocessableEntity, "no_such_session", err.Error())
		default:
			s.remoteError(w, err)
		}
		return
	}
	reply(w, http.StatusOK, map[string]any{"id": req.ID, "ok": true})
}

func (s *Server) remoteError(w http.ResponseWriter, err error) {
	switch {
	case wire.IsCode(err, wire.CodeNotFound):
		refuse(w, http.StatusUnprocessableEntity, "no_such_path", err.Error())
	case wire.IsCode(err, wire.CodeDenied):
		refuse(w, http.StatusForbidden, "denied", err.Error())
	case wire.IsCode(err, wire.CodeBadRequest):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		fail(w, http.StatusBadGateway, err.Error())
	}
}
