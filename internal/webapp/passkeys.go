package webapp

// A login of ir_web on several devices: each device a passkey of it (docs/web.md "Passkey"). A
// passkey is added through a one-time link made for the login (AddPasskeyLink); the badge on a
// person's messages is the device of the passkey the browser logged in with: set by hand, or
// what that browser says it is.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// Devices a passkey may be set to (api.Device's); "" is by the browser.
const (
	DeviceComputer = "computer"
	DeviceLaptop   = "laptop"
	DevicePhone    = "phone"
	DeviceNone     = "none"
)

// ErrNoPasskey: the login has no such passkey.
var ErrNoPasskey = errors.New("webapp: no such passkey")

// ErrLastOfLogin: a login keeps one passkey at least; remove the login instead.
var ErrLastOfLogin = errors.New("webapp: the last passkey of a login is removed with the login")

type sessionKey struct{}

// SessionOf is the web session (its hash) a request ir_web forwarded came in, "" for none.
func SessionOf(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey{}).(string)
	return s
}

// PasskeyID is how a passkey is named outside: its credential ID, base64url.
func PasskeyID(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }

// PasskeyInfo is a passkey of a login as listed: no key material.
type PasskeyInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"` // its provider (iCloud 钥匙串), else the browser it was made in
	// Device: what it shows as; Auto, not set by hand but by the browser it was last used in.
	Device     string    `json:"device"`
	Auto       bool      `json:"auto,omitempty"`
	Current    bool      `json:"current,omitempty"` // the browser asking is logged in with it
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}

// Passkeys are login's passkeys, oldest first; current marks the one session (a hash) logged in
// with.
func (w *Web) Passkeys(login, session string) []PasskeyInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.st.creds[login]
	cur := w.st.sessions[session]
	out := make([]PasskeyInfo, 0, len(c.Passkeys))
	for _, pk := range c.Passkeys {
		out = append(out, PasskeyInfo{
			ID: PasskeyID(pk.Credential.ID), Name: passkeyName(pk), Device: w.passkeyDeviceLocked(login, pk), Auto: pk.Device == "",
			Current: cur.Login == login && bytes.Equal(cur.Passkey, pk.Credential.ID), CreatedAt: pk.CreatedAt, LastUsedAt: pk.LastUsedAt,
		})
	}
	return out
}

// passkeyDeviceLocked is what pk shows as: as set, else as the browser it was last used in said,
// else as the one it was made in.
func (w *Web) passkeyDeviceLocked(login string, pk Passkey) string {
	if pk.Device != "" {
		return pk.Device
	}
	var last Session
	for _, s := range w.st.sessions {
		if s.Login == login && bytes.Equal(s.Passkey, pk.Credential.ID) && s.Device != "" && s.LastSeenAt.After(last.LastSeenAt) {
			last = s
		}
	}
	if last.Device != "" {
		return last.Device
	}
	return deviceOfPlatform(pk.Platform)
}

// AddPasskeyLink makes a one-time link (valid InviteTTL) that adds a passkey to login: opened on
// another device, it makes one there, and that device is login.
func (w *Web) AddPasskeyLink(login string) (InviteLink, error) {
	if !w.public {
		return InviteLink{}, ErrNotPublic
	}
	if info, ok := w.core.Endpoint(login); !ok || info.Kind != gate.KindPanel {
		return InviteLink{}, ErrNotFound
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	token := gate.NewSecret()
	id := gate.NewID(5)
	for w.st.invites[id].ID != "" {
		id = gate.NewID(5)
	}
	inv := Invite{ID: id, Hash: gate.HashSecret(token), Login: login, CreatedAt: now, ExpiresAt: now.Add(InviteTTL)}
	if err := w.commitLocked(Change{Invites: []Invite{inv}}); err != nil {
		return InviteLink{}, err
	}
	w.log.Info("webapp: passkey link created", "invite", inv.ID, "endpoint", login)
	return InviteLink{ID: inv.ID, URL: w.joinURL(token), ExpiresAt: inv.ExpiresAt}, nil
}

// PasskeyLinks are login's links to add a passkey not used yet.
func (w *Web) PasskeyLinks(login string) []InviteInfo {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []InviteInfo{}
	for _, inv := range w.st.state().Invites {
		if inv.Login == login && now.Before(inv.ExpiresAt) {
			out = append(out, InviteInfo{ID: inv.ID, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt})
		}
	}
	return out
}

// RemovePasskey takes passkey id (PasskeyID) from login; the browsers logged in with it are
// logged out. Not the last one: ErrLastOfLogin.
func (w *Web) RemovePasskey(login, id string) error {
	w.mu.Lock()
	c, ok := w.st.creds[login]
	i := slices.IndexFunc(c.Passkeys, func(pk Passkey) bool { return PasskeyID(pk.Credential.ID) == id })
	if !ok || i < 0 {
		w.mu.Unlock()
		return ErrNoPasskey
	}
	if len(c.Passkeys) == 1 {
		w.mu.Unlock()
		return ErrLastOfLogin
	}
	cred := c.Passkeys[i].Credential.ID
	c.Passkeys = slices.Delete(slices.Clone(c.Passkeys), i, i+1)
	ch := Change{Creds: []Cred{c}}
	for h, s := range w.st.sessions {
		if s.Login == login && bytes.Equal(s.Passkey, cred) {
			ch.DeleteSessions = append(ch.DeleteSessions, h)
		}
	}
	err := w.commitLocked(ch)
	w.mu.Unlock()
	if err != nil {
		return err
	}
	gone := map[string]bool{}
	for _, h := range ch.DeleteSessions {
		gone[h] = true
	}
	w.cancel(func(f *inFlight) bool { return gone[f.session] })
	w.log.Info("webapp: passkey removed", "endpoint", login, "sessions", len(ch.DeleteSessions))
	return nil
}

// SetPasskeyDevice sets what passkey id of login is on: a device, or "" by the browser.
func (w *Web) SetPasskeyDevice(login, id, device string) error {
	if !validDevice(device) && device != "" {
		return ErrBadDevice
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.st.creds[login]
	i := slices.IndexFunc(c.Passkeys, func(pk Passkey) bool { return PasskeyID(pk.Credential.ID) == id })
	if !ok || i < 0 {
		return ErrNoPasskey
	}
	c.Passkeys = slices.Clone(c.Passkeys)
	c.Passkeys[i].Device = device
	return w.commitLocked(Change{Creds: []Cred{c}})
}

// ErrBadDevice: not one of the devices.
var ErrBadDevice = errors.New("webapp: no such device")

func validDevice(d string) bool {
	switch d {
	case DeviceComputer, DeviceLaptop, DevicePhone, DeviceNone:
		return true
	}
	return false
}

// SessionPasskey is the passkey (PasskeyID) session logged in with, "" for none known (one from
// before passkeys were told apart).
func (w *Web) SessionPasskey(session string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s, ok := w.st.sessions[session]; ok && len(s.Passkey) > 0 {
		return PasskeyID(s.Passkey)
	}
	return ""
}

// SessionDevice is the device session (a hash) is on: its passkey's; "" if not known (no such
// session, or one from before).
func (w *Web) SessionDevice(session string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sessionDeviceLocked(w.st.sessions[session])
}

func (w *Web) sessionDeviceLocked(s Session) string {
	if len(s.Passkey) == 0 {
		return ""
	}
	for _, pk := range w.st.creds[s.Login].Passkeys {
		if bytes.Equal(pk.Credential.ID, s.Passkey) {
			if pk.Device != "" {
				return pk.Device
			}
			return cmpOr(s.Device, deviceOfPlatform(pk.Platform))
		}
	}
	return ""
}

// LoginDevice is the device login was last on: that of its browser seen last; "" if not known.
func (w *Web) LoginDevice(login string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var last Session
	for _, s := range w.st.sessions {
		if s.Login == login && (last.Hash == "" || s.LastSeenAt.After(last.LastSeenAt)) {
			last = s
		}
	}
	return w.sessionDeviceLocked(last)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- what a browser is ----

// deviceOfBrowser is what the browser r came from says it is: a phone (a tablet too), else a
// computer.
func deviceOfBrowser(r *http.Request) string {
	if r.Header.Get("Sec-CH-UA-Mobile") == "?1" {
		return DevicePhone
	}
	return deviceOfPlatform(platformOf(r.UserAgent()))
}

// deviceOfPlatform is the device a platform (platformOf) is on.
func deviceOfPlatform(p string) string {
	os, _, _ := strings.Cut(p, " · ")
	switch os {
	case "iPhone", "iPad", "Android":
		return DevicePhone
	}
	return DeviceComputer
}

// platformOf names the browser of a User-Agent: "Mac · Chrome", "iPhone · Safari"; "" if it
// cannot tell.
func platformOf(ua string) string {
	var os string
	switch {
	case strings.Contains(ua, "iPhone"):
		os = "iPhone"
	case strings.Contains(ua, "iPad"):
		os = "iPad"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "CrOS"):
		os = "ChromeOS"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		os = "Mac"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	var b string
	switch {
	case strings.Contains(ua, "Edg"):
		b = "Edge"
	case strings.Contains(ua, "OPR/"):
		b = "Opera"
	case strings.Contains(ua, "SamsungBrowser"):
		b = "Samsung Internet"
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS"):
		b = "Firefox"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS"):
		b = "Chrome"
	case strings.Contains(ua, "Safari/"):
		b = "Safari"
	}
	switch {
	case os != "" && b != "":
		return os + " · " + b
	case os != "":
		return os
	}
	return b
}

// passkeyName is how a passkey is called: its provider, by the AAGUID it was made with; else the
// browser it was made in; else "Passkey".
func passkeyName(pk Passkey) string {
	if a := pk.Credential.Authenticator.AAGUID; len(a) == 16 {
		h := hex.EncodeToString(a)
		if n, ok := providers[h[:8]+"-"+h[8:12]+"-"+h[12:16]+"-"+h[16:20]+"-"+h[20:]]; ok {
			return n
		}
	}
	return cmpOr(pk.Platform, "Passkey")
}

// providers names the passkey providers most passkeys are in, by AAGUID (the community's list,
// github.com/passkeydeveloper/passkey-authenticator-aaguids).
var providers = map[string]string{
	"fbfc3007-154e-4ecc-8c0b-6e020557d7bd": "iCloud 钥匙串",
	"dd4ec289-e01d-41c9-bb89-70fa845d4bf2": "iCloud 钥匙串",
	"ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4": "Google 密码管理器",
	"adce0002-35bc-c60a-648b-0b25f1f05503": "Chrome on Mac",
	"771b48fd-d3d4-4f74-9232-fc157ab0507a": "Edge on Mac",
	"08987058-cadc-4b81-b6e1-30de50dcbe96": "Windows Hello",
	"9ddd1817-af5a-4672-a2b9-3e3dd95000a9": "Windows Hello",
	"6028b017-b1d4-4c02-b4b3-afcdafc96bb2": "Windows Hello",
	"bada5566-a7aa-401f-bd96-45619a55120d": "1Password",
	"d548826e-79b4-db40-a3d8-11116f7e8349": "Bitwarden",
	"531126d6-e717-415c-9320-3d9aa6981239": "Dashlane",
	"53414d53-554e-4700-0000-000000000000": "Samsung Pass",
	"50726f74-6f6e-5061-7373-50726f746f6e": "Proton Pass",
	"fdb141b2-5d84-443e-8a35-4698c205a502": "KeePassXC",
}
