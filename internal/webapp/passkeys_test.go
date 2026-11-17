package webapp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/kumoSleeping/iridium/internal/gate"
)

const (
	uaIPhone  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	uaWindows = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0"
)

// joinAs is harness.join from a browser that says ua.
func (h *harness) joinAs(a *authenticator, token, ua string, name *string) *http.Response {
	h.t.Helper()
	hd := map[string]string{"User-Agent": ua}
	w := h.do(req{method: "POST", path: "/_ir/api/join/options", body: joinRequest{Token: token, Name: name}, header: hd})
	wantStatus(h.t, w, http.StatusOK)
	c := decode[ceremonyReply](h.t, w)
	return h.do(req{method: "POST", path: "/_ir/api/join/verify", body: verifyRequest{Flow: c.Flow, Response: a.create(c.Options)}, header: hd}).Result()
}

func TestAddPasskeyToLogin(t *testing.T) {
	h := newHarness(t, testOrigin)
	phone := &authenticator{t: t, origin: testOrigin}
	pc := &authenticator{t: t, origin: testOrigin}

	if r := h.joinAs(phone, h.setupToken(), uaIPhone, ptr("kumo")); r.StatusCode != http.StatusOK {
		t.Fatalf("join: %d", r.StatusCode)
	}
	me, _ := h.whoami()
	login, onPhone := me.ID, h.cookie

	link, err := h.w.AddPasskeyLink(login)
	if err != nil {
		t.Fatal(err)
	}
	token := tokenOf(t, link.URL)
	h.secrets = append(h.secrets, token)
	if inv := h.w.Invites(); len(inv) != 0 {
		t.Fatalf("a passkey link listed as an invite: %+v", inv)
	}
	if l := h.w.PasskeyLinks(login); len(l) != 1 || l[0].ID != link.ID {
		t.Fatalf("passkey links: %+v", l)
	}

	// Another browser opens it: no name to choose, the login's.
	h.cookie = ""
	reply := decode[joinReply](t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: token}}))
	if !reply.Add || reply.Name != "kumo" {
		t.Fatalf("check of a passkey link: %+v", reply)
	}
	if r := h.joinAs(pc, token, uaWindows, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("add: %d", r.StatusCode)
	}
	onPC := h.cookie
	if me, _ := h.whoami(); me.ID != login {
		t.Fatalf("the new device is %q, want %q", me.ID, login)
	}
	if n := len(h.g.Access().Endpoints); n != 1 { // kumo alone
		t.Fatalf("endpoints: %d", n)
	}
	if p, _ := h.w.Counts(login); p != 2 {
		t.Fatalf("passkeys: %d", p)
	}
	if l := h.w.PasskeyLinks(login); len(l) != 0 {
		t.Fatalf("link not used up: %+v", l)
	}
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: token}}), http.StatusForbidden)

	// Each browser is on its own device; the login on the one seen last.
	hashOf := gate.HashSecret
	if d := h.w.SessionDevice(hashOf(onPhone)); d != DevicePhone {
		t.Fatalf("phone's device %q", d)
	}
	if d := h.w.SessionDevice(hashOf(onPC)); d != DeviceComputer {
		t.Fatalf("pc's device %q", d)
	}
	h.clock.Advance(2 * touchEvery)
	h.cookie = onPhone
	h.whoami()
	if d := h.w.LoginDevice(login); d != DevicePhone {
		t.Fatalf("login's device %q, want the phone's", d)
	}

	list := h.w.Passkeys(login, hashOf(onPC))
	if len(list) != 2 || list[0].Current || !list[1].Current || !list[1].Auto || list[1].Name != "Windows · Edge" {
		t.Fatalf("passkeys: %+v", list)
	}
	pcKey := list[1].ID

	// Set by hand: a laptop, wherever it is used.
	if err := h.w.SetPasskeyDevice(login, pcKey, DeviceLaptop); err != nil {
		t.Fatal(err)
	}
	if d := h.w.SessionDevice(hashOf(onPC)); d != DeviceLaptop {
		t.Fatalf("pc's device %q after set", d)
	}
	if err := h.w.SetPasskeyDevice(login, pcKey, "tablet"); !errors.Is(err, ErrBadDevice) {
		t.Fatalf("bad device: %v", err)
	}

	// Its login on the phone's passkey goes on; removing the pc's logs that browser out.
	if err := h.w.RemovePasskey(login, pcKey); err != nil {
		t.Fatal(err)
	}
	h.cookie = onPC
	if _, code := h.whoami(); code != http.StatusUnauthorized {
		t.Fatalf("pc after its passkey went: %d", code)
	}
	h.cookie = ""
	wantStatus(t, h.login(pc, pc.creds[0]), http.StatusForbidden)
	h.cookie = onPhone
	if me, code := h.whoami(); code != http.StatusOK || me.ID != login {
		t.Fatalf("phone after the pc's passkey went: %d", code)
	}
	if err := h.w.RemovePasskey(login, list[0].ID); !errors.Is(err, ErrLastOfLogin) {
		t.Fatalf("last passkey: %v", err)
	}

	// The phone logs in again with its own passkey (its own user handle).
	h.cookie = ""
	wantStatus(t, h.login(phone, phone.creds[0]), http.StatusOK)
	if me, _ := h.whoami(); me.ID != login {
		t.Fatalf("logged in as %q", me.ID)
	}

	// The login gone, its links go too.
	if _, err := h.w.AddPasskeyLink(login); err != nil {
		t.Fatal(err)
	}
	h.w.Forget(login)
	if l := h.w.PasskeyLinks(login); len(l) != 0 {
		t.Fatalf("links of a login gone: %+v", l)
	}
	h.noSecretsLogged()
}

func TestPlatformOf(t *testing.T) {
	for ua, want := range map[string]string{
		uaIPhone:  "iPhone · Safari",
		uaWindows: "Windows · Edge",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36": "Mac · Chrome",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36":           "Android · Chrome",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                                 "Linux · Firefox",
		"curl/8.5.0": "",
	} {
		if got := platformOf(ua); got != want {
			t.Errorf("platformOf(%q) = %q, want %q", ua, got, want)
		}
	}
	if deviceOfPlatform("Android · Chrome") != DevicePhone || deviceOfPlatform("Mac · Safari") != DeviceComputer {
		t.Fatal("deviceOfPlatform")
	}
}
