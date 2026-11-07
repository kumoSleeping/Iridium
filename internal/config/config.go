// Package config is Iridium's one config file, JSON, which the settings page edits (plan "数据").
// A missing file, or a field left out, means the default.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	// Listen is the HTTP address; loopback only unless the Passkey gate is on.
	Listen string `json:"listen"`
	// Socket is the unix socket (0600) the local ir talks to; "" is $XDG_RUNTIME_DIR/iridium/ir.sock.
	Socket string `json:"socket,omitempty"`
	// StateDir holds the database and the holders' directories; "" is $XDG_STATE_HOME/iridium.
	StateDir string `json:"stateDir,omitempty"`
	// Hosts are the Host names served besides localhost and the loopback addresses.
	Hosts []string `json:"hosts,omitempty"`
	// IdleReap stops a hosted agent idle this long (its session stays); 0 never does.
	IdleReap Duration `json:"idleReap"`
	// StopGrace is how long a stop waits after closing the agent's stdin, before signals.
	StopGrace Duration `json:"stopGrace"`
	// CrashResume and QuotaResume run a turn again after the agent crashed or its quota came back.
	CrashResume bool `json:"crashResume"`
	QuotaResume bool `json:"quotaResume"`
	// Modes are the approval mode a new session of each agent starts in; none: the agent's own.
	Modes map[string]string `json:"modes,omitempty"`
	// Push is Web Push's total switch, off by default.
	Push bool `json:"push"`
	// QuotaHidden are the quota bars the page does not show, each "<agent>:<account>:<group>:<model>".
	QuotaHidden []string `json:"quotaHidden,omitempty"`
	// EnterInTurn is what the page's Enter does while a turn runs: "steer" ("" too) or "queue".
	EnterInTurn string `json:"enterInTurn,omitempty"`
	// Lang is the user's language, as the web settings last set it: "zh", "en"; "" not yet known.
	// Hosted agents are told it (session.SystemPrompt).
	Lang string `json:"lang,omitempty"`
	// ChatDir is where a plugin's chat opens a session before it set one with /new; "" is home.
	ChatDir string `json:"chatDir,omitempty"`
	// Path is the PATH agents run with; "" is the daemon's own.
	Path string `json:"path,omitempty"`
	// Programs are the agents' programs, absolute or looked up on Path.
	Programs map[string]string `json:"programs"`
	// ClaudeHome is Claude's own directory; "" is $CLAUDE_CONFIG_DIR, else ~/.claude.
	ClaudeHome string `json:"claudeHome,omitempty"`
	// UnitPrefix names the holders' systemd units; "" is iridium-hold-. Tests use iridium-test-.
	UnitPrefix string `json:"unitPrefix,omitempty"`
	// FolderRoots are where the folder picker may go; none is the user's home.
	FolderRoots []string `json:"folderRoots,omitempty"`
	// Uploads: how long uploaded files are kept ("0": always) and the largest one, in bytes.
	Uploads Uploads `json:"uploads"`
	// Public is the way in from outside (docs/gate.md): set, every web request needs a passkey.
	Public Public `json:"public,omitzero"`
	// Update is where new versions are looked for.
	Update Update `json:"update,omitzero"`
}

type Uploads struct {
	Keep    Duration `json:"keep"`
	MaxFile int64    `json:"maxFile"`
}

type Public struct {
	// Origin is public.origin, e.g. https://ir.example.com; "" serves this machine only.
	Origin string `json:"origin,omitempty"`
}

type Update struct {
	// URL is where new versions are looked for: a GitHub repository ("github:owner/repo") or a
	// manifest's URL (package update); "" is Iridium's own releases, "off" looks nowhere.
	URL string `json:"url,omitempty"`
	// Every is how often to look; "" or "0" is once a day.
	Every Duration `json:"every,omitzero"`
	// Manual: a newer version is only downloaded when 更新 is pressed (settings 自动下载更新 off).
	Manual bool `json:"manual,omitempty"`
}

// Source is where to look for new versions, "" for nowhere.
func (u Update) Source(fallback string) string {
	switch u.URL {
	case "":
		return fallback
	case "off", "none":
		return ""
	}
	return u.URL
}

func Default() Config {
	return Config{
		Listen:      "127.0.0.1:7810",
		IdleReap:    Duration(time.Hour),
		StopGrace:   Duration(5 * time.Second),
		CrashResume: true,
		QuotaResume: true,
		Programs:    map[string]string{"claude": "claude", "codex": "codex", "pi": "pi", "dsh": "dsh"},
		// Every agent approves by itself unless a session picks a mode that asks (2026-11-05).
		Modes:   map[string]string{"claude": "auto", "codex": "auto", "pi": "auto"},
		Uploads: Uploads{Keep: Duration(30 * 24 * time.Hour), MaxFile: 2 << 30},
	}
}

// DefaultPath is $XDG_CONFIG_HOME/iridium/config.json.
func DefaultPath() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "iridium", "config.json")
}

// Load reads path over the defaults; none there is the defaults. Unknown fields are an error, so
// a misspelt one is not silently ignored.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	for k, v := range Default().Programs {
		if c.Programs[k] == "" {
			c.Programs[k] = v
		}
	}
	return c, nil
}

// Save writes c to path whole, or not at all.
func (c Config) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c Config) StatePath() string {
	if c.StateDir != "" {
		return c.StateDir
	}
	return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "iridium")
}

func (c Config) SocketPath() string {
	if c.Socket != "" {
		return c.Socket
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "iridium", "ir.sock")
	}
	return filepath.Join(c.StatePath(), "ir.sock")
}

func (c Config) ClaudeDir() string {
	if c.ClaudeHome != "" {
		return c.ClaudeHome
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func xdg(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

// Duration is a time.Duration written as "1h30m"; "0" or "" is 0.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	if d == 0 {
		return []byte(`"0"`), nil
	}
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"1h\": %s", b)
	}
	if s == "" || s == "0" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }
