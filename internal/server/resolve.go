package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Session ids as the caller writes them (docs/api.md "会话 ID"): a unique start of one, or self,
// the session the caller's ir runs in. ir passes what it is given (ir api); the server takes it
// here, for the routes under /v1/internal/sessions/ and Satori's channel_id alike.

// self names the caller's own session.
const self = "self"

// fullID: an id this long is taken as it is (the page always sends whole ids).
const fullID = 32

// errSelf: self, from a caller with no session.
var errSelf = fmt.Errorf("%w: self is the session ir runs in; this caller runs in none", session.ErrBadRequest)

// sessionArg is the session arg names for c: its own for self, the one whose id it is or starts,
// else arg as it is (the route says it is not there). Two ids starting so: an error.
func (s *Server) sessionArg(ctx context.Context, c caller, arg string) (string, error) {
	if arg == self {
		if c.session == "" {
			return "", errSelf
		}
		return c.session, nil
	}
	if arg == "" || len(arg) >= fullID {
		return arg, nil
	}
	list, err := s.m.List(ctx)
	if err != nil {
		return arg, nil
	}
	var hits []string
	for _, ss := range list {
		if ss.ID == arg {
			return arg, nil
		}
		if strings.HasPrefix(ss.ID, arg) {
			hits = append(hits, ss.ID)
		}
	}
	switch len(hits) {
	case 0:
		return arg, nil
	case 1:
		return hits[0], nil
	}
	return "", fmt.Errorf("%w: %s is the start of %d sessions' ids; give more of it", session.ErrBadRequest, arg, len(hits))
}

// withSessionArg rewrites /v1/internal/sessions/<arg>/… to the whole id sessionArg gives.
func (s *Server) withSessionArg(r *http.Request) (*http.Request, error) {
	const prefix = "/v1/internal/sessions/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		return r, nil
	}
	arg, tail, _ := strings.Cut(rest, "/")
	id, err := s.sessionArg(r.Context(), callerOf(r), arg)
	if err != nil || id == arg {
		return r, err
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = prefix + id
	if strings.Contains(rest, "/") {
		r2.URL.Path += "/" + tail
	}
	r2.URL.RawPath = ""
	return r2, nil
}

// Resolve: satori.Resolver, channel_id as sessionArg takes it.
func (b satoriBackend) Resolve(ctx context.Context, c satori.Caller, id string) (string, error) {
	return b.s.sessionArg(ctx, callerAs(c), id)
}
