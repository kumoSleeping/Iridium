package server

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Who was named (docs/channels.md "有人 @ 我"): a message that names a login ("@name", Satori's
// <at id/>) or all the members ("@全体成员", <at type="all"/>) is kept as that login's mark on
// the channel (store "channel_ats": the first it has not read), till it reads that far; a quote
// of its words is not (kumo, 2026-11-21). The pages carry their own marks (the state "ats"), and
// the login is pushed the message whatever its rules say.

// person is a login as words may name it. member: what its place in a channel is kept under
// (readerOf: "<app>/<login>", the host's "local/*"); id: its Satori user id, a message's author.
type person struct {
	member, app, login, id, name string
}

// readerOf is the member c's marks are kept under: memberOf, the host's one however it came
// (Satori names its login, /v1/internal does not).
func readerOf(c caller) string {
	if c.Host() {
		return channel.AppMember(gate.HostID)
	}
	return memberOf(c)
}

// persons are the logins there are: the host, ir_web's, the other apps' approved ones.
func (s *Server) persons() []person {
	out := []person{{member: channel.AppMember(gate.HostID), app: gate.HostID, id: gate.HostID, name: s.Tag(gate.HostID)}}
	g := s.gate()
	if g == nil {
		return out
	}
	for _, e := range g.Access().Endpoints {
		if e.Kind == gate.KindPanel {
			out = append(out, person{member: channel.PersonMember(gate.WebApp, e.ID), app: gate.WebApp, login: e.ID, id: e.ID, name: e.Name})
		}
	}
	ls, err := s.st.Logins()
	if err != nil {
		s.logf("server: logins: %v", err)
	}
	for _, l := range ls {
		if !l.ApprovedAt.IsZero() {
			out = append(out, person{member: channel.PersonMember(l.App, l.ID), app: l.App, login: l.ID, id: l.ID, name: l.Name})
		}
	}
	return out
}

// sees: p sees channel ch; in: it is one of its people too.
func (s *Server) personSees(p person, ch api.Session) (sees, in bool) {
	c := caller{Identity: gate.Identity{App: p.app, Login: p.login, Name: p.name}}
	if !s.seenBy(c, ch) {
		return false, false
	}
	return true, s.inChannel(c, ch.ID) == nil
}

// namedIn are the names (of those given, lower case) that text calls: "@name" standing by
// itself, outside code and quoted lines, as an agent's nick is called (deliver.Mentions); a
// person's name may have what a nick may not (a space, a dot), so the longest that fits is the
// one named (web/src/chat/channel/lib.ts nickSpans).
func namedIn(text string, names []string) map[string]bool {
	out := map[string]bool{}
	if len(names) == 0 || !strings.Contains(text, "@") {
		return out
	}
	low := make([][]rune, 0, len(names))
	for _, n := range names {
		if n != "" {
			low = append(low, []rune(strings.ToLower(n)))
		}
	}
	slices.SortFunc(low, func(a, b []rune) int { return cmp.Compare(len(b), len(a)) })
	word := func(r rune) bool { return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(t, ">") {
			continue
		}
		rs := []rune(strings.ToLower(line))
		code := false
		for i := 0; i < len(rs); i++ {
			if rs[i] == '`' {
				code = !code
			}
			if code || rs[i] != '@' || (i > 0 && (word(rs[i-1]) || rs[i-1] == '@' || rs[i-1] == '/' || rs[i-1] == '.')) {
				continue
			}
			for _, n := range low {
				end := i + 1 + len(n)
				if end <= len(rs) && slices.Equal(rs[i+1:end], n) && (end == len(rs) || !word(rs[end])) {
					out[string(n)] = true
					i = end - 1
					break
				}
			}
		}
	}
	return out
}

// names notes whom message seq of channel ch names, among the logins that see it: text, its
// words; author, the member that said it ("": an agent), which its own words do not name. The
// message it answers names no one (kumo, 2026-11-21: a quote is no @). Each is pushed it, and the pages are told. It gives
// the members noted, for unnames. Called as the message is said, ahead of what answers it: the
// first that names a login is the one kept.
func (s *Server) names(ch string, seq int64, text string, author string) []string {
	if seq <= 0 || (!strings.Contains(text, "@") && !strings.Contains(text, "<at")) {
		return nil
	}
	st, err := s.m.State(ch)
	if err != nil {
		return nil
	}
	people := s.persons()
	var all bool
	ids, called := map[string]bool{}, []string{}
	for _, e := range satori.Ats(text) {
		switch {
		case e.Attr("type") == "all":
			all = true
		case e.Attr("id") != "":
			ids[e.Attr("id")] = true
		case e.Attr("name") != "":
			called = append(called, e.Attr("name"))
		}
	}
	words := satori.AtWords(text, s.ch.Nick)
	for _, n := range called {
		words += "\n@" + n
	}
	names := []string{deliver.AllMembers}
	for _, p := range people {
		names = append(names, p.name)
	}
	said := namedIn(words, names)
	all = all || said[strings.ToLower(deliver.AllMembers)]
	var told []person
	for _, p := range people {
		if p.member == author {
			continue
		}
		one := ids[p.id] || ids[p.member] || (p.name != "" && said[strings.ToLower(p.name)])
		if !one && !all {
			continue
		}
		sees, in := s.personSees(p, st.Session)
		if !sees || (!one && !in) {
			continue
		}
		if err := s.st.AddChannelAt(ch, p.member, seq, !one); err != nil {
			s.logf("server: channel %s: message %d names %s: %v", ch, seq, p.member, err)
			continue
		}
		told = append(told, p)
	}
	if len(told) == 0 {
		return nil
	}
	s.atsChanged()
	s.pushNamed(st, seq, told)
	out := make([]string, len(told))
	for i, p := range told {
		out[i] = p.member
	}
	return out
}

// unnames takes back what names noted for message seq of channel ch: it was not said after
// all (not sent). A mark of an earlier message stays.
func (s *Server) unnames(ch string, seq int64, members []string) {
	any := false
	for _, m := range members {
		if a, ok := s.atsOf(m)[ch]; ok && a.Seq == seq {
			had, _ := s.st.ClearChannelAts(ch, m, seq)
			any = any || had
		}
	}
	if any {
		s.atsChanged()
	}
}

// pushNamed pushes message seq of the channel to the logins it names, whatever their rules for
// it say (a quiet channel too); not while Web Push is off.
func (s *Server) pushNamed(st *api.SessionState, seq int64, told []person) {
	if s.Push == nil || !s.Push.Enabled() {
		return
	}
	m, ok := s.ch.Get(st.ID, seq)
	if !ok {
		return
	}
	who := m.Name
	if id, ok := store.SessionOfMember(m.Author); ok {
		who = cmp.Or(s.ch.Nick(id), string(s.harnessOf(id)))
	}
	body := s.MaskText(strings.TrimSpace(satori.AtWords(m.Text, s.ch.Nick)))
	if who != "" {
		body = who + ": " + body
	}
	if r := []rune(body); len(r) > 200 {
		body = string(r[:199]) + "…"
	}
	msg := api.PushMessage{Kind: "at", Session: st.ID, Title: s.MaskText(s.channelName(st.ID)), Body: body, Tag: st.ID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, p := range told {
			// A browser's subscription is kept under its login's id, the host's under the host's.
			if p.app == gate.WebApp || p.app == gate.HostID {
				s.Push.Send(ctx, p.id, msg)
			}
		}
	}()
}

// readTo: reader has read channel ch up to message seq; what named it up to there is no mark
// any more. end: that is all of the channel, so nothing left names it (a message taken back).
func (s *Server) readTo(ch, reader string, seq int64, end bool) {
	if end {
		seq = math.MaxInt64
	}
	had, err := s.st.ClearChannelAts(ch, reader, seq)
	if err != nil {
		s.logf("server: channel %s: marks of %s: %v", ch, reader, err)
	}
	if had {
		s.atsChanged()
	}
}

// atsOf are reader's marks, by channel.
func (s *Server) atsOf(reader string) api.ChannelAts {
	out := api.ChannelAts{}
	rows, err := s.st.ChannelAts(reader)
	if err != nil {
		s.logf("server: marks of %s: %v", reader, err)
	}
	for _, a := range rows {
		out[a.Channel] = api.ChannelAt{Seq: a.Seq, Everyone: a.Everyone}
	}
	return out
}

// atsChanged tells the pages the marks: the carried state "ats", every login's by its member;
// a stream carries its caller's alone (atsFor).
func (s *Server) atsChanged() {
	all := map[string]api.ChannelAts{}
	for _, p := range s.persons() {
		if a := s.atsOf(p.member); len(a) > 0 {
			all[p.member] = a
		}
	}
	s.hub.Set("ats", all)
}

// atsFor is the state "ats" as c's stream carries it: its own marks; an agent has none.
func (s *Server) atsFor(c caller, v []byte) []byte {
	var all map[string]json.RawMessage
	if c.byAgent() || json.Unmarshal(v, &all) != nil || all[readerOf(c)] == nil {
		return []byte("{}")
	}
	return all[readerOf(c)]
}

// atsGet is GET /v1/internal/ats: the caller's marks.
func (s *Server) atsGet(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.byAgent() {
		reply(w, http.StatusOK, api.ChannelAts{})
		return
	}
	reply(w, http.StatusOK, s.atsOf(readerOf(c)))
}
