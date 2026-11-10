package gate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

// ServeProfile updates a profile (name and/or avatar). Mount it behind Wrap:
//
//	PUT /api/me/profile        ServeProfile(w, r, "")                 the caller itself: a browser by its session, an app by its TOKEN
//	PUT /api/apps/{id}/profile ServeProfile(w, r, r.PathValue("id"))  any app or login: ir_web's logins and the host only
//
// The body is multipart/form-data with optional fields "name", "avatar" (a file) and
// "removeAvatar" ("1"), or JSON {"name"?, "avatar"? (base64 or a data: URL), "removeAvatar"?}.
// The reply is the updated EndpointInfo, or {"code": ..., "nameError"?: ...} (422 bad_name).
func (g *Gate) ServeProfile(w http.ResponseWriter, r *http.Request, id string) {
	caller, ok := FromContext(r.Context())
	switch {
	case !ok:
		writeJSON(w, http.StatusUnauthorized, apiError{"unauthenticated"})
		return
	case r.Method != http.MethodPut && r.Method != http.MethodPost:
		w.Header().Set("Allow", "PUT, POST")
		writeJSON(w, http.StatusMethodNotAllowed, apiError{"method_not_allowed"})
		return
	case id == "" && caller.Host():
		writeJSON(w, http.StatusUnprocessableEntity, apiError{"built_in"})
		return
	case id == "":
		id = caller.ID()
	case id != caller.ID() && !caller.CanManage():
		writeJSON(w, http.StatusForbidden, apiError{"forbidden"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxAvatarBytes*4/3+maxBody)
	p, err := readProfile(r)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.Is(err, ErrAvatarTooLarge) || errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, apiError{"avatar_too_large"})
		} else if errors.Is(err, errMediaType) {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"unsupported_media_type"})
		} else {
			writeJSON(w, http.StatusBadRequest, apiError{"bad_request"})
		}
		return
	}
	info, err := g.UpdateProfile(id, p)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, info)
	case nameCode(err) != "":
		writeJSON(w, http.StatusUnprocessableEntity, struct {
			Code      string `json:"code"`
			NameError string `json:"nameError"`
		}{"bad_name", nameCode(err)})
	case errors.Is(err, ErrAvatarTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{"avatar_too_large"})
	case errors.Is(err, ErrAvatarFormat):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{"avatar_format"})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
	case errors.Is(err, ErrLocal):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{"built_in"})
	default:
		writeJSON(w, http.StatusInternalServerError, apiError{"internal"})
	}
}

var errMediaType = errors.New("unsupported media type")

func readProfile(r *http.Request) (Profile, error) {
	var p Profile
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "multipart/form-data":
		mr, err := r.MultipartReader()
		if err != nil {
			return p, err
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return p, nil
			}
			if err != nil {
				return p, err
			}
			limit := int64(1024)
			if part.FormName() == "avatar" {
				limit = MaxAvatarBytes
			}
			data, err := io.ReadAll(io.LimitReader(part, limit+1))
			part.Close()
			if err != nil {
				return p, err
			}
			if int64(len(data)) > limit {
				if part.FormName() == "avatar" {
					return p, ErrAvatarTooLarge
				}
				return p, errors.New("field too long")
			}
			switch part.FormName() {
			case "name":
				s := string(data)
				p.Name = &s
			case "avatar":
				if len(data) > 0 {
					p.Avatar = data
				}
			case "removeAvatar":
				v := strings.TrimSpace(string(data))
				p.RemoveAvatar = v == "1" || v == "true"
			}
		}
	case "application/json":
		var body struct {
			Name         *string `json:"name"`
			Avatar       *string `json:"avatar"`
			RemoveAvatar bool    `json:"removeAvatar"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return p, err
		}
		p.Name, p.RemoveAvatar = body.Name, body.RemoveAvatar
		if body.Avatar != nil && *body.Avatar != "" {
			s := *body.Avatar
			if strings.HasPrefix(s, "data:") {
				_, s, _ = strings.Cut(s, ",")
			}
			data, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return p, err
			}
			if len(data) > MaxAvatarBytes {
				return p, ErrAvatarTooLarge
			}
			p.Avatar = data
		}
		return p, nil
	default:
		return p, errMediaType
	}
}

// serveAvatar serves an app's or login's avatar to anyone identified (Wrap). With the current
// version in ?v= it may be cached for good.
func (g *Gate) serveAvatar(w http.ResponseWriter, r *http.Request, id string) {
	g.mu.Lock()
	e, ok := g.st.endpoints[id]
	g.mu.Unlock()
	if !ok || len(e.Avatar) == 0 {
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", http.DetectContentType(e.Avatar))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("ETag", `"`+e.AvatarVersion+`"`)
	if r.URL.Query().Get("v") == e.AvatarVersion {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(e.Avatar))
}
