package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Session ids as the caller writes them (docs/api.md "会话 ID"): whole, or a unique start of
// one. ir passes what it is given (ir api); the server takes it here, for the routes under
// /v1/internal/sessions/ and Satori's channel_id alike.

// Words that once stood for an id (self: the caller's session; here: the channel its agent works
// for), taken no more (kumo, 2026-11-27): the same word named the agent in one route and the
// channel it speaks in in another, and what an agent meant for the channel went to its own
// session. A name is a name: an id is written as it is.
const self, here = "self", "here"

// fullID: an id this long is taken as it is (the page always sends whole ids).
const fullID = 32

// noAlias is the refusal of self or here (arg) from c: the ids to write instead.
func (s *Server) noAlias(c caller, arg string) error {
	if c.session == "" {
		return fmt.Errorf("%w: %s is not an id; write the id", session.ErrBadRequest, arg)
	}
	msg := fmt.Sprintf("%s is not an id; write the id: this session is %s", arg, c.session)
	if ch := s.hereOf(c); ch != c.session {
		msg += ", the channel you are called in " + ch
	}
	return fmt.Errorf("%w: %s", session.ErrBadRequest, msg)
}

// sessionArg is the session arg names for c: the one whose id it is or starts, else arg as it
// is (the route says it is not there). Two ids starting so, self or here: an error.
func (s *Server) sessionArg(ctx context.Context, c caller, arg string) (string, error) {
	if arg == self || arg == here {
		return "", s.noAlias(c, arg)
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
		// An agent naming another of a channel it is in by its nick (an interrupt; kumo,
		// 2026-11-26: no id to look up first, in a hurry).
		if id := s.peerByNick(c, arg); id != "" {
			return id, nil
		}
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

// peerByNick is the session of the agent called nick (an @ before it or not) in a channel the
// caller's agent is in, its own or another; "" if none is.
func (s *Server) peerByNick(c caller, nick string) string {
	nick = strings.TrimPrefix(nick, "@")
	if c.session == "" || nick == "" {
		return ""
	}
	for _, ch := range append([]string{c.session}, s.ch.In(c.session)...) {
		for _, a := range s.ch.Agents(ch) {
			if a != c.session && s.ch.Nick(a) == nick {
				return a
			}
		}
	}
	return ""
}

// peers: sessions a and b are agents of one channel.
func (s *Server) peers(a, b string) bool {
	if a == "" || b == "" || a == b {
		return false
	}
	for _, ch := range append([]string{a}, s.ch.In(a)...) {
		if s.ch.Speaks(ch, b) {
			return true
		}
	}
	return false
}
