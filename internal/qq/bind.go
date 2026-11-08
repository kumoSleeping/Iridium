package qq

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The QR flow that makes a bot, as qq-cli's `qq init --qr` and Rhodium's rh bot do it: QQ makes
// the bot under a bind task the phone scans; the secret comes sealed with a key only we have
// (AES-256-GCM: IV, ciphertext, tag). The scanner's openid, when QQ gives it, is the owner.

// DefaultPortal is QQ's QR service.
const DefaultPortal = "https://q.qq.com"

// Bind is a bind task: the page to scan, and the key the secret comes sealed with.
type Bind struct {
	Task string `json:"task"`
	Key  string `json:"key"` // base64, 32 bytes
	URL  string `json:"url"` // the page the QR code holds
	At   int64  `json:"at"`  // when it was made (ms)
}

// Bound is a bot the scan made or picked.
type Bound struct {
	AppID  string `json:"appId"`
	Secret string `json:"secret"`
	Owner  string `json:"owner,omitempty"` // the scanner's openid under this bot
}

// BindStatus: waiting for the scan, done, or expired (a new task then).
type BindStatus int

const (
	BindWaiting BindStatus = iota
	BindDone
	BindExpired
)

// Portal talks to QQ's QR service.
type Portal struct {
	Base string // "" is DefaultPortal
	HTTP *http.Client
}

func (p Portal) call(ctx context.Context, path string, body any) (json.RawMessage, error) {
	base := p.Base
	if base == "" {
		base = DefaultPortal
	}
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	b, _ := json.Marshal(body)
	var last error
	for try := 1; try <= 5; try++ {
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/lite/"+path, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json") // else a page that checks for bots
		res, err := hc.Do(req)
		if err == nil {
			var out struct {
				Retcode *int            `json:"retcode"`
				Msg     string          `json:"msg"`
				Data    json.RawMessage `json:"data"`
			}
			derr := json.NewDecoder(res.Body).Decode(&out)
			res.Body.Close()
			switch {
			case derr == nil && out.Retcode != nil && *out.Retcode == 0:
				return out.Data, nil
			case derr == nil && out.Retcode != nil:
				return nil, fmt.Errorf("QQ 的扫码服务出错：%s", firstOf(out.Msg, fmt.Sprintf("HTTP %d", res.StatusCode)))
			}
			last = fmt.Errorf("HTTP %d", res.StatusCode)
		} else {
			last = err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("连不上 QQ 的扫码服务：%v", last)
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// NewBind starts a bind task.
func (p Portal) NewBind(ctx context.Context) (Bind, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Bind{}, err
	}
	k := base64.StdEncoding.EncodeToString(key)
	d, err := p.call(ctx, "create_bind_task", map[string]string{"key": k})
	if err != nil {
		return Bind{}, err
	}
	var out struct {
		Task json.RawMessage `json:"task_id"`
	}
	_ = json.Unmarshal(d, &out)
	task := strings.Trim(string(out.Task), `"`)
	if task == "" || task == "null" {
		return Bind{}, errors.New("QQ 的扫码服务没有给二维码")
	}
	u := "https://q.qq.com/qqbot/openclaw/connect.html?task_id=" + url.QueryEscape(task) + "&_wv=2"
	return Bind{Task: task, Key: k, URL: u, At: time.Now().UnixMilli()}, nil
}

// Poll asks once how b stands; Bound only when done.
func (p Portal) Poll(ctx context.Context, b Bind) (BindStatus, Bound, error) {
	d, err := p.call(ctx, "poll_bind_result", map[string]string{"task_id": b.Task})
	if err != nil {
		return BindWaiting, Bound{}, err
	}
	var out struct {
		Status int             `json:"status"` // 1 waiting, 2 done, 3 expired
		AppID  json.RawMessage `json:"bot_appid"`
		Sealed string          `json:"bot_encrypt_secret"`
		Owner  string          `json:"user_openid"`
	}
	if err := json.Unmarshal(d, &out); err != nil {
		return BindWaiting, Bound{}, err
	}
	switch out.Status {
	case 3:
		return BindExpired, Bound{}, nil
	case 2:
	default:
		return BindWaiting, Bound{}, nil
	}
	app := strings.Trim(string(out.AppID), `"`)
	secret, err := unseal(b.Key, out.Sealed)
	if err != nil || app == "" || secret == "" {
		return BindWaiting, Bound{}, errors.New("扫码没有拿到 Bot 的 AppID 和 AppSecret")
	}
	return BindDone, Bound{AppID: app, Secret: secret, Owner: out.Owner}, nil
}

func unseal(key64, sealed64 string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(key64)
	if err != nil {
		return "", err
	}
	sealed, err := base64.StdEncoding.DecodeString(sealed64)
	if err != nil || len(sealed) < 12+16 {
		return "", errors.New("bad sealed secret")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
	return string(plain), err
}

// Seal is unseal's opposite, for tests (a fake portal).
func Seal(key64, secret string) string {
	key, _ := base64.StdEncoding.DecodeString(key64)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	return base64.StdEncoding.EncodeToString(gcm.Seal(iv, iv, []byte(secret), nil))
}
