package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// Logins of apps (draft 二「接入」, decision 12): an app names the login it speaks as by
// HeaderLogin, its ID within the app. One it speaks as the first time waits for a person: the
// request is refused (403 login_pending, nothing kept) and a login_join need asks. Allowed, its
// row is approved and its requests go through as it; denied or removed, it asks again as it next
// speaks. An app has at most loginsPending waiting.

// HeaderLogin is the header naming the login an app speaks as (Satori's).
const HeaderLogin = "Satori-User-ID"

const (
	loginsPending = 20
	loginMaxLen   = 64
)

// withLogin puts the login an app speaks as into the caller, once approved; else refuses.
func (s *Server) withLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login := r.Header.Get(HeaderLogin)
		id, ok := gate.FromContext(r.Context())
		if login == "" || !ok || id.App == "" || id.Host() || id.App == gate.WebApp || id.Session != "" {
			next.ServeHTTP(w, r)
			return
		}
		if !validLogin(login) {
			refuse(w, http.StatusUnprocessableEntity, "bad_login", "a login's ID: 1 to 64 characters, no / and no control characters")
			return
		}
		now := time.Now()
		l, there, err := s.st.Login(id.App, login)
		if err != nil {
			s.error(w, err)
			return
		}
		if there && !l.ApprovedAt.IsZero() {
			if now.Sub(l.LastActive) > time.Minute {
				_ = s.st.TouchLogin(id.App, login, now)
			}
			id.Login = login
			next.ServeHTTP(w, r.WithContext(gate.NewContext(r.Context(), id)))
			return
		}
		if !there {
			added, err := s.st.AddLogin(id.App, login, login, loginsPending, now)
			if err != nil {
				s.error(w, err)
				return
			}
			if !added {
				refuse(w, http.StatusForbidden, "too_many_pending", "too many logins of this app wait to be approved")
				return
			}
		}
		info, _ := s.gate().Endpoint(id.App)
		app := s.appOf(info)
		nd, added := s.asks.addUnless(func(n api.Need) bool {
			return n.Kind == api.NeedLoginJoin && n.App != nil && n.App.ID == id.App && n.Login == login
		}, api.Need{Kind: api.NeedLoginJoin, Name: login, Login: login, Asker: info.Name, App: &app})
		if added {
			s.logf("server: %s speaks as a new login %s, need %s", info.Name, login, nd.ID)
			s.terminalsChanged()
		}
		refuse(w, http.StatusForbidden, "login_pending", "this login waits to be approved")
	})
}

// validLogin: id may be a login's ID.
func validLogin(id string) bool {
	if id == "" || len(id) > loginMaxLen || !utf8.ValidString(id) || strings.Contains(id, "/") {
		return false
	}
	return !strings.ContainsFunc(id, unicode.IsControl)
}

// loginPerms is what login id of ir_web was given; nil for all.
func (s *Server) loginPerms(id string) *api.Perms {
	b, err := s.st.LoginPerms(id)
	if err != nil || len(b) == 0 {
		return nil
	}
	var p api.Perms
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}
