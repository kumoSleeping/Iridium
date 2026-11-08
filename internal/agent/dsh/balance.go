package dsh

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// ErrNoKey: no DeepSeek API key where dsh looks for one.
var ErrNoKey = errors.New("dsh: no DeepSeek API key")

// Balance is the DeepSeek account's balance (GET <base>/user/balance; costs nothing), with the key
// dsh itself would use: dsh has no quota windows, but the account has money (the user,
// 2026-11-08). Only for the official API: a DEEPSEEK_BASE_URL elsewhere has no such call.
func (d *Driver) Balance(ctx context.Context) ([]api.Balance, error) {
	d.mu.Lock()
	env := d.env
	d.mu.Unlock()
	if env == nil {
		env = os.Environ()
	}
	base := strings.TrimRight(cmpOr(lookEnv(env, "DEEPSEEK_BASE_URL"), "https://api.deepseek.com"), "/")
	if !strings.HasPrefix(base, "https://api.deepseek.com") {
		return nil, fmt.Errorf("dsh: no balance for %s", base)
	}
	key := APIKey(env)
	if key == "" {
		return nil, ErrNoKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/v1")+"/user/balance", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dsh: balance: %s", resp.Status)
	}
	var body struct {
		Infos []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
			Granted  string `json:"granted_balance"`
			ToppedUp string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	num := func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
	out := make([]api.Balance, 0, len(body.Infos))
	for _, b := range body.Infos {
		out = append(out, api.Balance{Currency: b.Currency, Total: num(b.Total), Granted: num(b.Granted), ToppedUp: num(b.ToppedUp)})
	}
	return out, nil
}

var _ agent.Balancer = (*Driver)(nil)

// APIKey is DEEPSEEK_API_KEY where dsh-credentials-local finds it, the first that has it: the
// stored file ($DSH_HOME/.credentials.yaml, its refs), the environment, $DSH_HOME/.env. Never
// logged.
func APIKey(env []string) string {
	home := lookEnv(env, "DSH_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".dsh")
	}
	if b, err := os.ReadFile(filepath.Join(home, ".credentials.yaml")); err == nil {
		if k := storedRef(b, "DEEPSEEK_API_KEY"); k != "" {
			return k
		}
	}
	if k := lookEnv(env, "DEEPSEEK_API_KEY"); k != "" {
		return k
	}
	if b, err := os.ReadFile(filepath.Join(home, ".env")); err == nil {
		for line := range strings.Lines(string(b)) {
			k, v, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export ")), "=")
			if ok && strings.TrimSpace(k) == "DEEPSEEK_API_KEY" {
				return unquote(strings.TrimSpace(v))
			}
		}
	}
	return ""
}

// storedRef reads name under the file's top-level refs: (a mapping of plain scalars; no YAML
// library for one key).
func storedRef(b []byte, name string) string {
	in := false
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := s.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			in = strings.TrimSpace(line) == "refs:"
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && unquote(strings.TrimSpace(k)) == name {
			return unquote(strings.TrimSpace(v))
		}
	}
	return ""
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		if s[0] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
		}
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func lookEnv(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
