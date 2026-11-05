package gate

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// ServeProfile updates a profile (name and/or avatar). Mount it behind Wrap:
//
//	PUT /api/me/profile             ServeProfile(w, r, "")                 the caller itself: a panel by its session, a channel by its token
//	PUT /api/endpoints/{id}/profile ServeProfile(w, r, r.PathValue("id"))  any endpoint: web panels and 本机 only
//
// The body is multipart/form-data with optional fields "name", "avatar" (a file) and
// "removeAvatar" ("1"), or JSON {"name"?, "avatar"? (base64 or a data: URL), "removeAvatar"?}.
// The reply is the updated EndpointInfo, or {"error": ..., "nameError"?: ...}.
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
	case id == "" && caller.Local:
		writeJSON(w, http.StatusConflict, apiError{"local"})
		return
	case id == "":
		id = caller.EndpointID
	case id != caller.EndpointID && !caller.CanManage():
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
		writeJSON(w, http.StatusConflict, struct {
			Error     string `json:"error"`
			NameError string `json:"nameError"`
		}{"name", nameCode(err)})
	case errors.Is(err, ErrAvatarTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{"avatar_too_large"})
	case errors.Is(err, ErrAvatarFormat):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{"avatar_format"})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{"not_found"})
	case errors.Is(err, ErrLocal):
		writeJSON(w, http.StatusConflict, apiError{"local"})
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
