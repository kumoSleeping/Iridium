// Package push is Web Push (plan "推送和插件接口"): VAPID keys kept in the state directory, the
// browsers' subscriptions in the store, the default events and the rules of workspaces, folders
// and sessions over them (Resolve). Off until the config's total switch is on. Messages are
// encrypted as RFC 8291 says (aes128gcm) and signed with VAPID (RFC 8292).
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// FileKeys holds the VAPID key pair, in the state directory.
const FileKeys = "vapid.json"

var b64 = base64.RawURLEncoding

type Options struct {
	StateDir string
	Store    *store.Store
	Manager  *session.Manager // nil: no session notices
	// Enabled is the total switch at start; SetEnabled changes it, and Save, if set, writes it.
	Enabled bool
	Save    func(enabled bool) error
	Client  *http.Client
	Subject string // the VAPID contact; "" is mailto:iridium@localhost
	// AllowHTTP takes plain http endpoints (tests); push services are https.
	AllowHTTP bool
	Logf      func(string, ...any)
	// Mask, if set, takes the credentials' values out of what is pushed (plan "凭据").
	Mask func(string) string
}

type Service struct {
	o       Options
	key     *ecdsa.PrivateKey
	pub     []byte // uncompressed point
	enabled atomic.Bool
	mu      sync.Mutex // one SetEnabled at a time

	amu       sync.Mutex
	compacted map[string]bool // sessions whose running turn compacted as asked, pushed
}

// asked marks (set) a session's running turn as one whose compaction you asked for was pushed, or
// takes the mark (not set) as the turn ends, saying if it was there.
func (s *Service) asked(session string, set bool) bool {
	s.amu.Lock()
	defer s.amu.Unlock()
	if set {
		if s.compacted == nil {
			s.compacted = map[string]bool{}
		}
		s.compacted[session] = true
		return true
	}
	was := s.compacted[session]
	delete(s.compacted, session)
	return was
}

// Open loads the VAPID keys, or makes them (0600) the first time.
func Open(o Options) (*Service, error) {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Subject == "" {
		o.Subject = "mailto:iridium@localhost"
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	key, err := loadKey(filepath.Join(o.StateDir, FileKeys))
	if err != nil {
		return nil, err
	}
	pub, err := key.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	s := &Service{o: o, key: key, pub: pub}
	s.enabled.Store(o.Enabled)
	if o.Manager != nil {
		o.Manager.Watch(s.onNotice)
	}
	return s, nil
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	var saved struct {
		Private string `json:"private"`
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &saved); err != nil {
			return nil, fmt.Errorf("push: %s: %w", path, err)
		}
		d, err := b64.DecodeString(saved.Private)
		if err != nil {
			return nil, fmt.Errorf("push: %s: %w", path, err)
		}
		return ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	d, err := key.Bytes()
	if err != nil {
		return nil, err
	}
	saved.Private = b64.EncodeToString(d)
	b, _ := json.Marshal(saved)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// PublicKey is the VAPID public key a page subscribes with (applicationServerKey).
func (s *Service) PublicKey() string { return b64.EncodeToString(s.pub) }

func (s *Service) Enabled() bool { return s.enabled.Load() }

// SetEnabled turns the total switch, and saves it.
func (s *Service) SetEnabled(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.o.Save != nil {
		if err := s.o.Save(on); err != nil {
			return err
		}
	}
	s.enabled.Store(on)
	return nil
}

// State is the settings as the page shows them.
func (s *Service) State() (api.PushState, error) {
	st := api.PushState{Enabled: s.Enabled(), PublicKey: s.PublicKey(), Defaults: apiEvents(store.DefaultPushEvents), Rules: []api.PushRule{}}
	rules, err := s.o.Store.PushRules()
	if err != nil {
		return st, err
	}
	for _, r := range rules {
		if r.Scope == store.PushDefault {
			if r.Events != nil {
				st.Defaults = apiEvents(*r.Events)
			}
			continue
		}
		ar := api.PushRule{Scope: api.PushScope(r.Scope), Target: r.Target, Mode: api.PushModeOn}
		if r.Off {
			ar.Mode = api.PushModeOff
		}
		if r.Events != nil {
			e := apiEvents(*r.Events)
			ar.Events = &e
		}
		st.Rules = append(st.Rules, ar)
	}
	return st, nil
}

func apiEvents(e store.PushEvents) api.PushEvents {
	return api.PushEvents{NeedsYou: e.NeedsYou, Error: e.Error, TurnEnd: e.TurnEnd}
}

func storeEvents(e api.PushEvents) store.PushEvents {
	return store.PushEvents{NeedsYou: e.NeedsYou, Error: e.Error, TurnEnd: e.TurnEnd}
}

// SetDefaults sets the events pushed where no rule says otherwise.
func (s *Service) SetDefaults(e api.PushEvents) error {
	ev := storeEvents(e)
	return s.o.Store.SetPushRule(store.PushRule{Scope: store.PushDefault, Events: &ev})
}

var ErrBadRule = errors.New("bad push rule")

// SetRule keeps r, or drops its scope's rule when its mode is inherit (as above). Off keeps no
// events.
func (s *Service) SetRule(r api.PushRule) error {
	switch r.Scope {
	case api.PushScopeWorkspace:
		if id, err := strconv.ParseInt(r.Target, 10, 64); err != nil || id < 0 || strconv.FormatInt(id, 10) != r.Target {
			return fmt.Errorf("%w: a workspace's target is its id", ErrBadRule)
		}
	case api.PushScopeFolder:
		if !filepath.IsAbs(r.Target) {
			return fmt.Errorf("%w: a folder's target is its absolute path", ErrBadRule)
		}
	case api.PushScopeSession:
		if r.Target == "" {
			return fmt.Errorf("%w: a session's target is its id", ErrBadRule)
		}
	default:
		return fmt.Errorf("%w: scope %q", ErrBadRule, r.Scope)
	}
	switch r.Mode {
	case api.PushModeInherit, "":
		return s.o.Store.DeletePushRule(string(r.Scope), r.Target)
	case api.PushModeOff:
		return s.o.Store.SetPushRule(store.PushRule{Scope: string(r.Scope), Target: r.Target, Off: true})
	case api.PushModeOn:
		sr := store.PushRule{Scope: string(r.Scope), Target: r.Target}
		if r.Events != nil {
			e := storeEvents(*r.Events)
			sr.Events = &e
		}
		return s.o.Store.SetPushRule(sr)
	}
	return fmt.Errorf("%w: mode %q", ErrBadRule, r.Mode)
}

// Resolve is what a session's events push (web/src/push.ts pushOf is the same): the nearest rule
// of the session, its folder (cwd) and its workspace says on or off (none: on); the events are the
// nearest of them on with events of their own, else the defaults.
func Resolve(st api.PushState, workspace int64, cwd, session string) (api.PushEvents, bool) {
	at := func(scope api.PushScope, target string) *api.PushRule {
		for i := range st.Rules {
			if r := &st.Rules[i]; r.Scope == scope && r.Target == target {
				return r
			}
		}
		return nil
	}
	levels := []*api.PushRule{at(api.PushScopeSession, session), at(api.PushScopeFolder, cwd), at(api.PushScopeWorkspace, strconv.FormatInt(workspace, 10))}
	for _, r := range levels {
		if r != nil {
			if r.Mode == api.PushModeOff {
				return api.PushEvents{}, false
			}
			break
		}
	}
	for _, r := range levels {
		if r != nil && r.Mode == api.PushModeOn && r.Events != nil {
			return *r.Events, true
		}
	}
	return st.Defaults, true
}

// Subscribe keeps a browser's subscription for terminal.
func (s *Service) Subscribe(terminal string, sub api.PushSubscription) error {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Host == "" || u.Scheme != "https" && !(s.o.AllowHTTP && u.Scheme == "http") {
		return fmt.Errorf("%w: a push endpoint is an https URL", ErrBadSubscription)
	}
	if p, err := b64.DecodeString(strings.TrimRight(sub.Keys.P256dh, "=")); err != nil || len(p) != 65 {
		return fmt.Errorf("%w: bad p256dh", ErrBadSubscription)
	}
	if a, err := b64.DecodeString(strings.TrimRight(sub.Keys.Auth, "=")); err != nil || len(a) != 16 {
		return fmt.Errorf("%w: bad auth", ErrBadSubscription)
	}
	keys, _ := json.Marshal(sub.Keys)
	return s.o.Store.AddPushSubscription(store.PushSubscription{Terminal: terminal, Endpoint: sub.Endpoint, Keys: string(keys)})
}

func (s *Service) Unsubscribe(endpoint string) error {
	return s.o.Store.DeletePushSubscription(endpoint)
}

var ErrBadSubscription = errors.New("bad subscription")

// onNotice pushes what the session's rules want (Resolve), while the switch is on.
func (s *Service) onNotice(n session.Notice) {
	if !s.Enabled() {
		return
	}
	if kind, n, ok := s.kindOf(n); ok {
		go s.notice(kind, n)
	}
}

// kindOf is the push a notice is, if any, and the notice as pushed.
func (s *Service) kindOf(n session.Notice) (string, session.Notice, bool) {
	switch n.Kind {
	case session.NoticeAsk:
		return "needs_you", n, true
	case session.NoticeError:
		return "error", n, true
	case session.NoticeTurnEnd:
		// After a compaction you asked for, its turn's end with nothing said: that was pushed.
		if s.asked(n.Session, false) && n.Text == "" {
			return "", n, false
		}
		return "turn_end", n, !n.Background || n.Text != ""
	case session.NoticeCompacted:
		// The agent's own, its context full, mid-work: nothing to tell. One you asked for is done
		// work, a turn's end (the user, 2026-11-08).
		if !n.Asked {
			return "", n, false
		}
		s.asked(n.Session, true)
		n.Text = "压缩完成"
		return "turn_end", n, true
	}
	return "", n, false
}

func (s *Service) notice(kind string, n session.Notice) {
	st, err := s.o.Manager.State(n.Session)
	if err != nil {
		return
	}
	// A session in a group at another machine not listed (docs/exec.md "远端工作区").
	if !s.o.Manager.Listed(n.Session) {
		return
	}
	ps, err := s.State()
	if err != nil {
		s.o.Logf("push: %v", err)
		return
	}
	e, on := Resolve(ps, st.Workspace, st.Cwd, n.Session)
	if !on || !map[string]bool{"needs_you": e.NeedsYou, "error": e.Error, "turn_end": e.TurnEnd}[kind] {
		return
	}
	msg := api.PushMessage{Kind: kind, Session: n.Session, Title: titleOf(st), Tag: n.Session}
	switch kind {
	case "needs_you":
		msg.Body = "需要你"
		if n.Ask != nil {
			c := session.CardOf(n.Session, *n.Ask)
			msg.Body = c.Title
			if len(c.Summary) > 0 {
				msg.Body += "：" + c.Summary[0]
			}
		}
	case "error":
		msg.Body = "出错：" + n.Text
	case "turn_end":
		msg.Body = n.Text
		if n.Denied > 0 {
			msg.Body = fmt.Sprintf("（auto 拦了 %d 步）%s", n.Denied, msg.Body)
		}
	}
	if s.o.Mask != nil {
		msg.Title, msg.Body = s.o.Mask(msg.Title), s.o.Mask(msg.Body)
	}
	msg.Body = cut(msg.Body, 200)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s.Send(ctx, "", msg)
}

func titleOf(st *api.SessionState) string {
	for _, t := range []string{st.Name, st.Title} {
		if t != "" {
			return cut(t, 60)
		}
	}
	return filepath.Base(st.Cwd)
}

func cut(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// Send pushes msg to every subscription (terminal "": of every terminal). One the push service
// says is gone (404, 410) is dropped. It gives how many took it.
func (s *Service) Send(ctx context.Context, terminal string, msg api.PushMessage) int {
	subs, err := s.o.Store.PushSubscriptions()
	if err != nil {
		s.o.Logf("push: %v", err)
		return 0
	}
	body, _ := json.Marshal(msg)
	n := 0
	for _, sub := range subs {
		if terminal != "" && sub.Terminal != terminal {
			continue
		}
		code, err := s.post(ctx, sub, body)
		switch {
		case err != nil:
			s.o.Logf("push to %s: %v", host(sub.Endpoint), err)
		case code == http.StatusNotFound || code == http.StatusGone:
			s.o.Logf("push to %s: gone, dropping it", host(sub.Endpoint))
			_ = s.o.Store.DeletePushSubscription(sub.Endpoint)
		case code >= 300:
			s.o.Logf("push to %s: status %d", host(sub.Endpoint), code)
		default:
			n++
		}
	}
	return n
}

func host(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "?"
	}
	return u.Host
}

func (s *Service) post(ctx context.Context, sub store.PushSubscription, payload []byte) (int, error) {
	var keys api.PushSubscription
	keys.Endpoint = sub.Endpoint
	if err := json.Unmarshal([]byte(sub.Keys), &keys.Keys); err != nil {
		return 0, err
	}
	p256dh, err := b64.DecodeString(strings.TrimRight(keys.Keys.P256dh, "="))
	if err != nil {
		return 0, err
	}
	auth, err := b64.DecodeString(strings.TrimRight(keys.Keys.Auth, "="))
	if err != nil {
		return 0, err
	}
	body, err := Encrypt(p256dh, auth, payload)
	if err != nil {
		return 0, err
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil {
		return 0, err
	}
	jwt, err := s.vapid(u.Scheme + "://" + u.Host)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+s.PublicKey())
	resp, err := s.o.Client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode, nil
}

// vapid is the signed JWT for the push service at aud (RFC 8292), good for 12 hours.
func (s *Service) vapid(aud string) (string, error) {
	head := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": aud, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": s.o.Subject})
	signed := head + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	r, sig, err := ecdsa.Sign(rand.Reader, s.key, sum[:])
	if err != nil {
		return "", err
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])
	return signed + "." + b64.EncodeToString(raw), nil
}

// Encrypt is payload for the subscription whose keys are p256dh and auth, as RFC 8291 says:
// aes128gcm, one record, the sender's key in the header.
func Encrypt(p256dh, auth, payload []byte) ([]byte, error) {
	ua, err := ecdh.P256().NewPublicKey(p256dh)
	if err != nil {
		return nil, err
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	cek, nonce, err := keys(secret, auth, salt, p256dh, asPub)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 0, 86)
	head = append(head, salt...)
	head = binary.BigEndian.AppendUint32(head, 4096)
	head = append(head, byte(len(asPub)))
	head = append(head, asPub...)
	return gcm.Seal(head, nonce, append(payload, 2), nil), nil
}

// keys derives the content key and nonce (RFC 8291 §3.4).
func keys(secret, auth, salt, uaPub, asPub []byte) (cek, nonce []byte, err error) {
	prk, err := hkdf.Extract(sha256.New, secret, auth)
	if err != nil {
		return nil, nil, err
	}
	info := "WebPush: info\x00" + string(uaPub) + string(asPub)
	ikm, err := hkdf.Expand(sha256.New, prk, info, 32)
	if err != nil {
		return nil, nil, err
	}
	prk, err = hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, nil, err
	}
	if cek, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, err
	}
	nonce, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	return cek, nonce, err
}

// Decrypt is Encrypt's inverse for the subscriber holding ua (tests; a push service never does).
func Decrypt(ua *ecdh.PrivateKey, auth, body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("push: short body")
	}
	salt, idlen := body[:16], int(body[20])
	if len(body) < 21+idlen {
		return nil, errors.New("push: short body")
	}
	asPub := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		return nil, err
	}
	secret, err := ua.ECDH(as)
	if err != nil {
		return nil, err
	}
	cek, nonce, err := keys(secret, auth, salt, ua.PublicKey().Bytes(), asPub)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, errors.New("push: no record delimiter")
	}
	return plain[:len(plain)-1], nil
}

// VerifyVAPID checks a vapid Authorization header against the service's key (tests).
func VerifyVAPID(header string, pub []byte) error {
	t, k, ok := strings.Cut(strings.TrimPrefix(header, "vapid t="), ", k=")
	if !ok || k != b64.EncodeToString(pub) {
		return errors.New("push: bad vapid header")
	}
	parts := strings.Split(t, ".")
	if len(parts) != 3 {
		return errors.New("push: bad jwt")
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return errors.New("push: bad signature")
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return errors.New("push: signature does not verify")
	}
	return nil
}
