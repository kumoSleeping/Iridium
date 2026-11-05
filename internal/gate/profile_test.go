package gate

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// halves is a w×h image, left half red and right half blue, with alpha a.
func halves(w, h int, a uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{0, 0, 255, a}
			if x < w/2 {
				c = color.NRGBA{255, 0, 0, a}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 95})
	return b.Bytes()
}

// withOrientation inserts an EXIF APP1 segment with the given orientation after SOI.
func withOrientation(j []byte, o uint16) []byte {
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08") // big endian, IFD0 at 8
	tiff = binary.BigEndian.AppendUint16(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.BigEndian.AppendUint16(tiff, 3) // SHORT
	tiff = binary.BigEndian.AppendUint32(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, o)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0) // padding, next IFD = 0
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(seg)+2))
	app1 = append(app1, seg...)
	return append(append(append([]byte{}, j[:2]...), app1...), j[2:]...)
}

func decodeAvatar(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != AvatarSize || b.Dy() != AvatarSize {
		t.Fatalf("avatar is %v", b)
	}
	return img
}

func reddish(c color.Color) bool { r, _, b, _ := c.RGBA(); return r > 0xc000 && b < 0x4000 }
func bluish(c color.Color) bool  { r, _, b, _ := c.RGBA(); return b > 0xc000 && r < 0x4000 }

func TestMakeAvatar(t *testing.T) {
	// Opaque, wide: centre-cropped, scaled, stored as JPEG; red left, blue right.
	data, ver, err := makeAvatar(encodePNG(halves(800, 400, 255)))
	if err != nil {
		t.Fatal(err)
	}
	if http.DetectContentType(data) != "image/jpeg" || len(ver) != 12 {
		t.Fatalf("type %s version %q", http.DetectContentType(data), ver)
	}
	img := decodeAvatar(t, data)
	if !reddish(img.At(10, 64)) || !bluish(img.At(118, 64)) {
		t.Fatal("crop lost the halves")
	}
	// Transparent: stays PNG with alpha.
	data, _, err = makeAvatar(encodePNG(halves(64, 300, 128)))
	if err != nil || http.DetectContentType(data) != "image/png" {
		t.Fatalf("transparent: %v %s", err, http.DetectContentType(data))
	}
	if _, _, _, a := decodeAvatar(t, data).At(64, 64).RGBA(); a > 0x9000 || a < 0x7000 {
		t.Fatalf("alpha %x", a)
	}
	// A phone JPEG that says "rotate 90° clockwise": red ends up on top.
	data, _, err = makeAvatar(withOrientation(encodeJPEG(halves(300, 300, 255)), 6))
	if err != nil {
		t.Fatal(err)
	}
	img = decodeAvatar(t, data)
	if !reddish(img.At(64, 10)) || !bluish(img.At(64, 118)) {
		t.Fatal("EXIF orientation 6 not applied")
	}
	if jpegOrientation(encodeJPEG(halves(8, 8, 255))) != 1 {
		t.Fatal("orientation without EXIF")
	}
	// Refused: GIF, junk, too big, too many pixels.
	var g bytes.Buffer
	gif.Encode(&g, halves(10, 10, 255), nil)
	for name, in := range map[string][]byte{
		"gif":  g.Bytes(),
		"junk": []byte("not an image"),
		"huge": pngHeader(100000, 100000),
	} {
		if _, _, err := makeAvatar(in); err != ErrAvatarFormat {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := makeAvatar(make([]byte, MaxAvatarBytes+1)); err != ErrAvatarTooLarge {
		t.Errorf("too large: %v", err)
	}
}

// pngHeader is the start of a PNG that claims the given size (a decompression bomb).
func pngHeader(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := b.Bytes()
	binary.BigEndian.PutUint32(data[16:], uint32(w))
	binary.BigEndian.PutUint32(data[20:], uint32(h))
	return data
}

func TestChannelTokensAndProfiles(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	panelCookie := h.cookie

	// `ir token qq`: a channel endpoint with a bearer token.
	info, tok, err := h.g.CreateToken("QQ")
	if err != nil {
		t.Fatal(err)
	}
	h.secrets = append(h.secrets, tok)
	if info.Kind != KindChannel || info.Name != "QQ" || !strings.HasPrefix(tok, channelTokenPrefix) {
		t.Fatalf("token: %+v", info)
	}
	if _, _, err := h.g.CreateToken("mac"); err != ErrNameTaken {
		t.Fatalf("token for a panel's name: %v", err)
	}
	bot := func(q req) *httptest.ResponseRecorder {
		if q.header == nil {
			q.header = map[string]string{}
		}
		q.header["Authorization"] = "Bearer " + tok
		q.header["Origin"] = "" // plugins are not browsers
		q.header["Sec-Fetch-Site"] = ""
		q.noCookie = true
		return h.do(q)
	}
	w := bot(req{method: "POST", path: "/api/chat/qq:private:1/input"})
	wantStatus(t, w, http.StatusOK)
	if id := decode[appReply](t, w); id.EndpointID != info.ID || id.Kind != KindChannel || id.Name != "QQ" {
		t.Fatalf("bot identity: %+v", id)
	}
	wantStatus(t, h.do(req{path: "/api/x", noCookie: true, header: map[string]string{"Authorization": "Bearer ir_" + strings.Repeat("x", tokenLen)}}), http.StatusUnauthorized)
	wantStatus(t, h.do(req{path: "/api/x", noCookie: true, header: map[string]string{"Authorization": "Bearer junk"}}), http.StatusUnauthorized)

	// The bot sets its own avatar and name (multipart).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", "QQ Bot")
	fw, _ := mw.CreateFormFile("avatar", "bot.png")
	fw.Write(encodePNG(halves(256, 256, 255)))
	mw.Close()
	w = bot(req{method: "PUT", path: "/api/me/profile", body: body.Bytes(), header: map[string]string{"Content-Type": mw.FormDataContentType()}})
	wantStatus(t, w, http.StatusOK)
	info = decode[EndpointInfo](t, w)
	if info.Name != "QQ Bot" || !strings.HasPrefix(info.Avatar, "/_ir/avatar/"+info.ID+"?v=") {
		t.Fatalf("bot profile: %+v", info)
	}
	// A bot cannot change another endpoint.
	var mac EndpointInfo
	for _, e := range h.g.Access().Endpoints {
		if e.Kind == KindPanel {
			mac = e
		}
	}
	w = bot(req{method: "PUT", path: "/api/endpoints/" + mac.ID + "/profile", body: map[string]any{"name": "pwned"}})
	wantStatus(t, w, http.StatusForbidden)

	// The panel sees the avatar; outsiders do not; the version URL is cacheable.
	w = h.do(req{path: info.Avatar})
	wantStatus(t, w, http.StatusOK)
	if w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("avatar headers: %v", w.Header())
	}
	decodeAvatar(t, w.Body.Bytes())
	w = h.do(req{path: info.Avatar, header: map[string]string{"If-None-Match": w.Header().Get("ETag")}})
	wantStatus(t, w, http.StatusNotModified)
	if cc := h.do(req{path: "/_ir/avatar/" + info.ID}).Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Fatalf("unversioned avatar cache-control %q", cc)
	}
	wantStatus(t, bot(req{path: info.Avatar}), http.StatusOK)
	wantStatus(t, h.do(req{path: info.Avatar, noCookie: true}), http.StatusUnauthorized)

	// The panel renames the bot (JSON), names stay unique, and removes its avatar.
	w = h.do(req{method: "PUT", path: "/api/endpoints/" + info.ID + "/profile", body: map[string]any{"name": "MAC"}})
	wantStatus(t, w, http.StatusConflict)
	if r := decode[joinReply](t, w); r.NameError != "taken" {
		t.Fatalf("rename to a taken name: %s", w.Body.String())
	}
	w = h.do(req{method: "PUT", path: "/api/endpoints/" + info.ID + "/profile", body: map[string]any{"name": "qq", "removeAvatar": true}})
	wantStatus(t, w, http.StatusOK)
	if got := decode[EndpointInfo](t, w); got.Name != "qq" || got.Avatar != "" {
		t.Fatalf("after panel update: %+v", got)
	}
	wantStatus(t, h.do(req{path: info.Avatar}), http.StatusNotFound)

	// The panel updates itself: case change of its own name, avatar as a data: URL.
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encodePNG(halves(40, 40, 0)))
	w = h.do(req{method: "PUT", path: "/api/me/profile", body: map[string]any{"name": "Mac", "avatar": data}})
	wantStatus(t, w, http.StatusOK)
	if got := decode[EndpointInfo](t, w); got.Name != "Mac" || got.Avatar == "" {
		t.Fatalf("self update: %+v", got)
	}
	w = h.do(req{method: "PUT", path: "/api/me/profile", body: map[string]any{"avatar": base64.StdEncoding.EncodeToString([]byte("GIF89a"))}})
	wantStatus(t, w, http.StatusUnprocessableEntity)
	w = h.do(req{method: "PUT", path: "/api/me/profile", body: []byte("x"), header: map[string]string{"Content-Type": "text/plain"}})
	wantStatus(t, w, http.StatusUnsupportedMediaType)
	// Cookie-authenticated profile writes need our origin.
	w = h.do(req{method: "PUT", path: "/api/me/profile", body: map[string]any{"name": "evil"}, header: map[string]string{"Origin": "https://evil.example"}})
	wantStatus(t, w, http.StatusForbidden)

	// Admin function, by name.
	if _, err := h.g.UpdateProfile("qq", Profile{Name: ptr("本机")}); err != ErrNameReserved {
		t.Fatalf("reserved: %v", err)
	}
	if _, err := h.g.UpdateProfile(LocalName, Profile{Name: ptr("x")}); err != ErrLocal {
		t.Fatalf("local: %v", err)
	}
	if _, err := h.g.UpdateProfile("nobody", Profile{}); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}

	// Re-issuing the token cuts off the old one and its connections.
	var closed bool
	r := httptest.NewRequest("GET", "/api/events", nil)
	r.Host = "ir.example.com"
	r.Header.Set("Authorization", "Bearer "+tok)
	h.g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.g.Track(r, func() { closed = true })
	})).ServeHTTP(httptest.NewRecorder(), r)
	_, tok2, err := h.g.CreateToken("QQ")
	if err != nil {
		t.Fatal(err)
	}
	h.secrets = append(h.secrets, tok2)
	if !closed {
		t.Fatal("old token's connection still open")
	}
	wantStatus(t, bot(req{path: "/api/x"}), http.StatusUnauthorized)
	tok = tok2
	wantStatus(t, bot(req{path: "/api/x"}), http.StatusOK)

	// Revoking the channel is always allowed.
	if err := h.g.Revoke("qq"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, bot(req{path: "/api/x"}), http.StatusUnauthorized)
	h.cookie = panelCookie
	if _, code := h.whoami(); code != 200 {
		t.Fatal("panel lost its session")
	}
	h.noSecretsLogged()
}

func TestChannelInLocalMode(t *testing.T) {
	h := newHarness(t, "")
	_, tok, err := h.g.CreateToken("bot")
	if err != nil {
		t.Fatal(err)
	}
	do := func(auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/x", nil)
		r.Host = "127.0.0.1:7810"
		r.RemoteAddr = "127.0.0.1:1"
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.h.ServeHTTP(w, r)
		return w
	}
	if id := decode[appReply](t, do("Bearer "+tok)); id.Name != "bot" || id.Local {
		t.Fatalf("channel in local mode: %+v", id)
	}
	if id := decode[appReply](t, do("")); !id.Local {
		t.Fatalf("no token in local mode: %+v", id)
	}
	wantStatus(t, do("Bearer ir_"+strings.Repeat("A", tokenLen)), http.StatusUnauthorized)
	// 本机 has no stored profile.
	r := httptest.NewRequest("PUT", "/api/me/profile", strings.NewReader(`{"name":"x"}`))
	r.Host = "127.0.0.1:7810"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusConflict)
}
