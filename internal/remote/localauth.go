package remote

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	// LocalExecFile is written in Iridium's state directory while the server listens so a local
	// app/executor on this computer can connect to /v1/internal/exec over loopback WebSocket
	// without pairing or unix-socket ACL issues on Windows.
	LocalExecFile = "local-exec.json"

	// LocalExecTokenPrefix marks a local loopback executor token (separate from paired ir_ and
	// per-session irs_ tokens).
	LocalExecTokenPrefix = "irl_"

	// LocalExecID is the Hub machine ID used by a local-credential executor connection.
	LocalExecID = "local-exec"
)

// LocalExec is the JSON payload stored in <StatePath>/local-exec.json.
type LocalExec struct {
	WS    string `json:"ws"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// LocalExecAuth owns one running server's local-exec credential file and token.
type LocalExecAuth struct {
	path  string
	token string
	pid   int
}

// WriteLocalExec generates a fresh random token and atomically writes <stateDir>/local-exec.json
// readable only by the current user (0600 on Unix, protected single-user DACL on Windows).
func WriteLocalExec(stateDir, wsURL string) (*LocalExecAuth, error) {
	if stateDir == "" || wsURL == "" {
		return nil, errors.New("remote: stateDir and wsURL are required")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	tok := LocalExecTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	pid := os.Getpid()
	data, err := json.Marshal(LocalExec{WS: wsURL, Token: tok, PID: pid})
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	path := filepath.Join(stateDir, LocalExecFile)
	if err := writeLocalExecFile(path, data); err != nil {
		return nil, err
	}
	return &LocalExecAuth{path: path, token: tok, pid: pid}, nil
}

// ReadLocalExec reads <stateDir>/local-exec.json.
func ReadLocalExec(stateDir string) (LocalExec, error) {
	var out LocalExec
	b, err := os.ReadFile(filepath.Join(stateDir, LocalExecFile))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	if out.WS == "" || !strings.HasPrefix(out.Token, LocalExecTokenPrefix) {
		return LocalExec{}, errors.New("remote: invalid local-exec credential file")
	}
	return out, nil
}

// Match reports whether tok equals this instance's local executor token in constant time.
func (a *LocalExecAuth) Match(tok string) bool {
	if a == nil || a.token == "" || !strings.HasPrefix(tok, LocalExecTokenPrefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a.token), []byte(tok)) == 1
}

// Token returns the generated local token.
func (a *LocalExecAuth) Token() string {
	if a == nil {
		return ""
	}
	return a.token
}

// Path returns the credential file path.
func (a *LocalExecAuth) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Close removes the credential file if it still holds this instance's token.
func (a *LocalExecAuth) Close() error {
	if a == nil || a.path == "" {
		return nil
	}
	b, err := os.ReadFile(a.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var cur LocalExec
	if json.Unmarshal(b, &cur) == nil && cur.Token != "" && !a.Match(cur.Token) {
		return nil // another instance already replaced it
	}
	if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// LocalExecWSURL turns a TCP listener address (e.g. "127.0.0.1:7810", "[::]:7810", "0.0.0.0:7810")
// into the loopback WebSocket URL for /v1/internal/exec.
func LocalExecWSURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	default:
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			if ip.To4() != nil {
				host = "127.0.0.1"
			} else {
				host = "::1"
			}
		}
	}
	return fmt.Sprintf("ws://%s/v1/internal/exec", net.JoinHostPort(host, port))
}
