package agy

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// LoginFile is agy's login in its directory, where the keyring is not there to hold it (agy
// 1.5.9: {"token": {"access_token", "expiry"...}, "id_token_email"}). Read, never written: agy
// renews the token itself while it runs.
const LoginFile = "antigravity-oauth-token"

// Endpoint is the Cloud Code service agy reads its quota from (the daily one: its numbers are those
// Antigravity's usage page shows, the other's are not); "" asks nothing
// (tests: testagent.Programs).
var Endpoint = "https://daily-cloudcode-pa.googleapis.com"

// ErrNoLogin: agy has no login file, or its token has run out (agy renews it only while it runs).
var ErrNoLogin = errors.New("agy: no valid login")

type login struct {
	Token struct {
		Access string    `json:"access_token"`
		Expiry time.Time `json:"expiry"`
	} `json:"token"`
	Email string `json:"id_token_email"`
}

func readLogin(home string) (*login, error) {
	b, err := os.ReadFile(filepath.Join(home, LoginFile))
	if err != nil {
		return nil, err
	}
	var l login
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("agy: %s: %w", LoginFile, err)
	}
	return &l, nil
}

// quotaSummary is retrieveUserQuotaSummary's answer: groups of models sharing their quota, each a
// bucket per window ("5h", "weekly").
type quotaSummary struct {
	Groups []struct {
		Name    string `json:"displayName"`
		Buckets []struct {
			ID        string   `json:"bucketId"`
			Window    string   `json:"window"`
			Remaining *float64 `json:"remainingFraction"`
			Reset     string   `json:"resetTime"`
		} `json:"buckets"`
	} `json:"groups"`
}

// Windows is the account's quota as Antigravity shows it in its own usage page, read with agy's
// own login: retrieveUserQuotaSummary of the project loadCodeAssist names (windowsOf). Asked as
// Antigravity's app asks (its User-Agent, as CodexBar does): asked as agy's CLI, the service leaves
// the Gemini group out (2026-11-16).
func Windows(ctx context.Context, home, version string) ([]api.QuotaWindow, error) {
	if Endpoint == "" {
		return nil, nil
	}
	l, err := readLogin(home)
	if err != nil || l.Token.Access == "" || time.Now().After(l.Token.Expiry) {
		return nil, ErrNoLogin
	}
	ua := fmt.Sprintf("antigravity/hub/2.9.1 %s/%s", runtime.GOOS, runtime.GOARCH)
	post := func(method string, body, out any) error {
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint+"/v1internal:"+method, bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+l.Token.Access)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", ua)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("agy: %s: %s", method, resp.Status)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
	platform := strings.ToUpper(runtime.GOOS + "_" + runtime.GOARCH)
	var assist struct {
		Project string `json:"cloudaicompanionProject"`
	}
	if err := post("loadCodeAssist", map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY", "platform": platform, "pluginType": "GEMINI"}}, &assist); err != nil {
		return nil, err
	}
	var sum quotaSummary
	if err := post("retrieveUserQuotaSummary", map[string]string{"project": assist.Project}, &sum); err != nil {
		return nil, err
	}
	return windowsOf(sum), nil
}

// windowsOf is a window per group and window of the summary, named by the group ("Gemini Models":
// Gemini; "Claude and GPT models": Claude · GPT), the five hours' a session's, the week's weekly.
func windowsOf(sum quotaSummary) []api.QuotaWindow {
	var out []api.QuotaWindow
	for _, g := range sum.Groups {
		name := strings.TrimSpace(g.Name)
		for _, suffix := range []string{" Models", " models"} {
			name = strings.TrimSuffix(name, suffix)
		}
		name = strings.ReplaceAll(name, " and ", " · ")
		for _, b := range g.Buckets {
			w := api.QuotaWindow{Model: name}
			switch b.Window {
			case "5h":
				w.Group = "session"
			case "weekly":
				w.Group = "weekly"
			default:
				continue
			}
			left := 1.0
			if b.Remaining != nil {
				left = *b.Remaining
			}
			w.Percent = (1 - left) * 100
			if t, err := time.Parse(time.RFC3339, b.Reset); err == nil {
				w.ResetsAt = t.UnixMilli()
			}
			out = append(out, w)
		}
	}
	// By group, the five hours ("session") before the week, as Claude's.
	slices.SortStableFunc(out, func(a, b api.QuotaWindow) int {
		return cmp.Or(cmp.Compare(a.Model, b.Model), cmp.Compare(a.Group, b.Group))
	})
	return out
}
