package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Folders at another machine's executor (docs/exec.md), for the folder picker: the page sees its
// paths '/'-separated (C:/Users/kumo), the executor gets its own separator. "" and "~" are its home.

// machineAt is executor mid, connected; it has answered when nil.
func (s *Server) machineAt(w http.ResponseWriter, mid string) (*remote.Machine, *wire.Hello) {
	if s.Remote == nil {
		fail(w, http.StatusNotFound, "no executors here")
		return nil, nil
	}
	mc := s.Remote.Find(mid)
	if mc == nil {
		fail(w, http.StatusNotFound, "no such machine")
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

func (s *Server) remoteFolders(w http.ResponseWriter, r *http.Request, mid, path string) {
	mc, hi := s.machineAt(w, mid)
	if mc == nil {
		return
	}
	dir := nativePath(hi, path)
	if !remote.IsAbs(hi.Sep, dir) {
		fail(w, http.StatusBadRequest, "not an absolute path")
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

func (s *Server) remoteError(w http.ResponseWriter, err error) {
	switch {
	case wire.IsCode(err, wire.CodeNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case wire.IsCode(err, wire.CodeDenied):
		fail(w, http.StatusForbidden, err.Error())
	case wire.IsCode(err, wire.CodeBadRequest):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		fail(w, http.StatusBadGateway, err.Error())
	}
}
