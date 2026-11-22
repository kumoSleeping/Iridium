package server

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/ctx/assemble"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Roles (docs/channels.md "角色"): an agent, a model and a card kept to start sessions as; no
// effort and no mode, which are the task's (otae, 2026-11-21). A
// session started as a role takes its card (a copy: changed in the session, the role's stays)
// and its name as its nick (with hex digits of its session's id after it while another session
// has it: 克劳德大小姐-e444); what it
// chooses to remember is the role's, shared by all its sessions.

func (s *Server) roleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/roles", s.rolesGet)
	mux.HandleFunc("POST /v1/internal/roles", s.rolePost)
	mux.HandleFunc("PATCH /v1/internal/roles/{id}", s.rolePatch)
	mux.HandleFunc("DELETE /v1/internal/roles/{id}", s.roleDelete)
	mux.HandleFunc("DELETE /v1/internal/sessions/{id}/role", s.roleLeave)
	mux.HandleFunc("GET /v1/internal/roles/{id}/avatar", s.roleFace)
	mux.HandleFunc("GET /v1/internal/roles/{id}/background", s.roleFace)
	mux.HandleFunc("GET /v1/internal/personas", s.personasGet)
}

// roleOut is role r as the API gives it.
func (s *Server) roleOut(r store.Role) api.Role {
	key := store.RoleProfileKey(r.ID)
	out := api.Role{ID: r.ID, Name: r.Name, Agent: api.Agent(r.Agent), Model: r.Model, Preset: r.Preset, Machine: r.Machine}
	p := s.st.Profile(key)
	out.Intro, out.Persona = p.Intro, p.Persona
	info, _ := agents.Get(out.Agent)
	out.ModelName = strings.TrimSpace(cmp.Or(info.Name, r.Agent) + " " + r.Model)
	if _, v, ok := s.face(key, false, roleModelKey(r)); ok {
		out.Avatar = "/v1/internal/roles/" + r.ID + "/avatar?v=" + v
	}
	if _, v, ok := s.face(key, true, ""); ok {
		out.Background = "/v1/internal/roles/" + r.ID + "/background?v=" + v
	}
	if ms, err := s.st.Memories(key, 0); err == nil {
		out.Memories = len(ms)
	}
	return out
}

// roleBy is the role ref names: its id, or its name.
func (s *Server) roleBy(ref string) (store.Role, bool) {
	rs, _ := s.st.Roles()
	for _, r := range rs {
		if r.ID == ref {
			return r, true
		}
	}
	for _, r := range rs {
		if strings.EqualFold(r.Name, ref) {
			return r, true
		}
	}
	return store.Role{}, false
}

func (s *Server) rolesGet(w http.ResponseWriter, r *http.Request) {
	rs, err := s.st.Roles()
	if err != nil {
		s.fault(w, err)
		return
	}
	out := make([]api.Role, 0, len(rs))
	for _, x := range rs {
		out = append(out, s.roleOut(x))
	}
	reply(w, http.StatusOK, out)
}

// roleModelKey is where the face of the sessions of role r's model is kept: a role with no face
// of its own has it, as a session does.
func roleModelKey(r store.Role) string {
	if r.Agent == "" || r.Model == "" || r.Model == "default" {
		return ""
	}
	return "m:" + r.Agent + ":" + r.Model
}

func (s *Server) roleFace(w http.ResponseWriter, r *http.Request) {
	fallback := ""
	if role, ok := s.roleBy(r.PathValue("id")); ok {
		fallback = roleModelKey(role)
	}
	img, v, ok := s.face(store.RoleProfileKey(r.PathValue("id")), strings.HasSuffix(r.URL.Path, "/background"), fallback)
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "none")
		return
	}
	serveFace(w, r, img, v)
}

// roleFault answers with an error of a role's.
func (s *Server) roleFault(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrRoleName) {
		refuse(w, http.StatusConflict, "role_name", err.Error())
		return
	}
	s.error(w, err)
}

// roleFrom fills r with what session id runs as now: its agent, model, preset and machine.
func (s *Server) roleFrom(r *store.Role, id string) error {
	st, err := s.m.State(id)
	if err != nil {
		return err
	}
	if st.Bare || st.Agent == "" {
		return fmt.Errorf("%w: a channel with no agent of its own is no role", session.ErrBadRequest)
	}
	model := cmp.Or(st.Pick, st.Model)
	if model == "default" {
		model = st.Model
	}
	r.Agent, r.Model, r.Preset, r.Machine = string(st.Agent), model, st.Preset, st.Machine
	return nil
}

// rolePost keeps a role: from a session (its card and what it runs, as they are now; the
// session is the role's from then on, and what it remembered goes with it), or as given.
func (s *Server) rolePost(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.byAgent() {
		forbid(w, errors.New("roles are a person's to keep"))
		return
	}
	var req api.NewRole
	if !readJSON(w, r, &req) {
		return
	}
	role := store.Role{ID: session.NewChannelID(), Name: strings.TrimSpace(req.Name), Agent: string(req.Agent), Model: req.Model, Preset: req.Preset, Machine: req.Machine,
		Sort: float64(s.rolesSeq.Add(1))}
	if rs, err := s.st.Roles(); err == nil && len(rs) > 0 {
		role.Sort = rs[len(rs)-1].Sort + 1
	}
	id := ""
	if req.Session != "" {
		var err error
		if id, err = s.sessionArg(r.Context(), c, req.Session); err != nil {
			s.error(w, err)
			return
		}
		if _, err := s.using(c, id); err != nil {
			s.error(w, err)
			return
		}
		if err := s.roleFrom(&role, id); err != nil {
			s.error(w, err)
			return
		}
		role.Name = cmp.Or(role.Name, s.ch.Nick(id))
	}
	if !deliver.ValidNick(role.Name) || role.Agent == "" {
		refuse(w, http.StatusBadRequest, "bad_request", "name: letters, digits, _ and -, at most 32; agent: which agent it is")
		return
	}
	if err := s.st.PutRole(role); err != nil {
		s.roleFault(w, err)
		return
	}
	key := store.RoleProfileKey(role.ID)
	if id != "" {
		if err := s.keepCard(id, key); err != nil {
			s.fault(w, err)
			return
		}
		if err := s.st.SetRoleOf(id, role.ID); err != nil {
			s.fault(w, err)
			return
		}
	} else {
		prof := store.Profile{}
		var err error
		if prof.Intro, err = intro(req.Intro); err == nil {
			prof.Persona, err = persona(req.Persona)
		}
		if err == nil {
			_, err = s.setFaces(c, key, &prof, strp(req.Avatar), strp(req.Background), "", "")
		}
		if err == nil {
			err = s.st.SetProfile(key, prof)
		}
		if err != nil {
			_ = s.st.DeleteRole(role.ID)
			s.error(w, err)
			return
		}
	}
	s.rolesChanged()
	reply(w, http.StatusOK, s.roleOut(role))
}

// keepCard makes session id's card the one kept under key (a role's): its intro, persona, background
// and the face it is drawn with, its own or, with none, its model's (kumo, 2026-11-17: a role
// kept from a session that had its model's face had none).
func (s *Server) keepCard(id, key string) error {
	if err := s.st.CopyProfile(store.ChannelProfileKey(id), key); err != nil {
		return err
	}
	if _, _, ok := s.face(key, false, ""); ok {
		return nil
	}
	st, err := s.m.State(id)
	if err != nil {
		return nil
	}
	m := modelAvatarKey(st)
	if m == "" {
		return nil
	}
	img, v, ok, err := s.st.Avatar(m)
	if err != nil || !ok {
		return err
	}
	return s.st.PutAvatar(key, img, v)
}

// strp is v as a patch's field: nil for none.
func strp(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (s *Server) rolesChanged() {
	s.hub.Set("roles", s.rolesSeq.Add(1))
	s.cardsChanged(true)
}

func (s *Server) rolePatch(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.byAgent() {
		forbid(w, errors.New("roles are a person's to keep"))
		return
	}
	role, ok := s.roleBy(r.PathValue("id"))
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "no such role")
		return
	}
	var p api.RolePatch
	if !readJSON(w, r, &p) {
		return
	}
	key := store.RoleProfileKey(role.ID)
	if p.Session != "" {
		// 更新角色: the session's card, not what it runs (its model and preset change here alone;
		// otae, 2026-11-21). Not its nick either: that may carry its id's digits.
		id, err := s.sessionArg(r.Context(), c, p.Session)
		if err == nil {
			_, err = s.using(c, id)
		}
		if err == nil {
			err = s.keepCard(id, key)
		}
		if err == nil {
			err = s.st.SetRoleOf(id, role.ID)
		}
		if err != nil {
			s.error(w, err)
			return
		}
	}
	if p.Name != nil {
		if !deliver.ValidNick(strings.TrimSpace(*p.Name)) {
			refuse(w, http.StatusBadRequest, "bad_request", "name: letters, digits, _ and -, at most 32")
			return
		}
		role.Name = strings.TrimSpace(*p.Name)
	}
	for _, f := range []struct {
		to   *string
		from *string
	}{{&role.Model, p.Model}, {&role.Preset, p.Preset}} {
		if f.from != nil {
			*f.to = *f.from
		}
	}
	if p.Sort != nil {
		role.Sort = *p.Sort
	}
	prof := s.st.Profile(key)
	if p.Intro != nil {
		v, err := intro(*p.Intro)
		if err != nil {
			s.error(w, err)
			return
		}
		prof.Intro = v
	}
	if p.Persona != nil {
		v, err := persona(*p.Persona)
		if err != nil {
			s.error(w, err)
			return
		}
		prof.Persona = v
	}
	if _, err := s.setFaces(c, key, &prof, p.Avatar, p.Background, "", ""); err != nil {
		s.error(w, err)
		return
	}
	if err := s.st.SetProfile(key, prof); err != nil {
		s.fault(w, err)
		return
	}
	if err := s.st.PutRole(role); err != nil {
		s.roleFault(w, err)
		return
	}
	s.rolesChanged()
	reply(w, http.StatusOK, s.roleOut(role))
}

func (s *Server) roleDelete(w http.ResponseWriter, r *http.Request) {
	if callerOf(r).byAgent() {
		forbid(w, errors.New("roles are a person's to keep"))
		return
	}
	role, ok := s.roleBy(r.PathValue("id"))
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "no such role")
		return
	}
	if err := s.st.DeleteRole(role.ID); err != nil {
		s.fault(w, err)
		return
	}
	s.rolesChanged()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// roleLeave: session {id} is no role's from now on. Its card stays as it is; what it
// remembered stays the role's.
func (s *Server) roleLeave(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.byAgent() {
		forbid(w, errors.New("roles are a person's to keep"))
		return
	}
	id, err := s.sessionArg(r.Context(), c, r.PathValue("id"))
	if err == nil {
		_, err = s.using(c, id)
	}
	if err != nil {
		s.error(w, err)
		return
	}
	if err := s.st.SetRoleOf(id, ""); err != nil {
		s.fault(w, err)
		return
	}
	s.rolesChanged()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// asRole fills what req leaves unsaid from the role it names; the role, if it names one. Its
// effort and mode are req's or the agent's own: a role keeps none.
func (s *Server) asRole(req *api.NewSession) (*store.Role, error) {
	if req.Role == "" {
		return nil, nil
	}
	role, ok := s.roleBy(req.Role)
	if !ok {
		return nil, fmt.Errorf("%w: no role %q (ir api GET internal/roles)", session.ErrBadRequest, req.Role)
	}
	if req.Agent != "" && string(req.Agent) != role.Agent {
		return nil, fmt.Errorf("%w: role %s is %s, not %s", session.ErrBadRequest, role.Name, role.Agent, req.Agent)
	}
	req.Agent = api.Agent(role.Agent)
	req.Model, req.Preset = cmp.Or(req.Model, role.Model), cmp.Or(req.Preset, role.Preset)
	if req.Machine == "" && req.Cwd == "" {
		req.Machine = role.Machine
	}
	return &role, nil
}

// carded gives session id, just made and not yet started, the card req asks for: its role's
// (a copy, the role's name as its nick), then what req itself gives. Its agent's first system
// prompt has it.
func (s *Server) carded(c caller, id string, req api.NewSession) error {
	key := store.ChannelProfileKey(id)
	nick := req.Nick
	if req.Role != "" {
		if role, ok := s.roleBy(req.Role); ok {
			if err := s.st.CopyProfile(store.RoleProfileKey(role.ID), key); err != nil {
				return err
			}
			if err := s.st.SetRoleOf(id, role.ID); err != nil {
				return err
			}
			nick = cmp.Or(nick, role.Name)
		}
	}
	if nick != "" {
		// The role's name; while another session has it, hex digits of this one's id after it
		// (克劳德大小姐-e444; kumo, 2026-11-17: not 克劳德大小姐2).
		err := s.ch.SetNick(id, nick)
		if err != nil && deliver.ValidNick(nick) && s.ch.NickTaken(nick) {
			err = s.ch.SetNick(id, channel.NickWithID(nick, id, s.ch.NickTaken))
		}
		if err != nil {
			return err
		}
	}
	if req.Intro == "" && req.Persona == "" && req.Avatar == "" && req.Background == "" {
		return nil
	}
	prof := s.st.Profile(key)
	if req.Intro != "" {
		v, err := intro(req.Intro)
		if err != nil {
			return err
		}
		prof.Intro = v
	}
	if req.Persona != "" {
		v, err := persona(req.Persona)
		if err != nil {
			return err
		}
		prof.Persona = v
	}
	if _, err := s.setFaces(c, key, &prof, strp(req.Avatar), strp(req.Background), req.Cwd, ""); err != nil {
		return err
	}
	return s.st.SetProfile(key, prof)
}

// cardCaller is the context's key for who asked for a session being made (carded).
type cardCaller struct{}

// selfText is what session id's agent is told of itself in its system prompt.
func (s *Server) selfText(id string) *assemble.Self {
	p := s.st.Profile(store.ChannelProfileKey(id))
	out := &assemble.Self{Nick: s.ch.Nick(id), Intro: p.Intro, Persona: p.Persona, IR: true, Zone: time.Local}
	const memories = 12
	ms := s.memoriesOf(id, 0)
	for i, m := range ms {
		if i >= memories {
			out.More = len(ms) - memories
			break
		}
		from := ""
		if m.Channel != "" && m.Channel != id {
			from = fmt.Sprintf("%s #%d", cmp.Or(m.Name, m.Channel), m.Seq)
		}
		out.Memory = append(out.Memory, assemble.SelfMemory{At: m.At, From: from, Text: m.Text})
	}
	for _, c := range s.selfChannels(id, 8) {
		if c.Own {
			continue // its own session: it is in it
		}
		out.Channels = append(out.Channels, assemble.SelfChannel{ID: c.Channel, Name: cmp.Or(c.Name, c.Channel), At: c.At, With: c.With, Last: c.Last})
	}
	for _, b := range s.selfBefore(id, 3) {
		out.Before = append(out.Before, assemble.SelfBefore{Session: b.Session, At: b.At, Turns: b.Turns, First: b.First})
	}
	return out
}
