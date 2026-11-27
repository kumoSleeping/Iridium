package desktop

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/kumoSleeping/iridium/internal/join"
)

// Server is an Iridium the app knows: its page in the window when it is the one shown, this
// computer joined to it while it is on.
type Server struct {
	URL string `json:"url"`
	// Local: this computer's own Iridium (local.go), never joined: it is this computer.
	Local bool `json:"local,omitempty"`
	Off   bool `json:"off,omitempty"` // not joined (still shown when chosen)
	// What it may do here (docs/exec.md): no command lines; only these folders ("": all).
	NoShell   bool     `json:"noShell,omitempty"`
	Roots     []string `json:"roots,omitempty"`
	AgentsOff []string `json:"agentsOff,omitempty"` // the agents it may not start here, by name
	// LoggedIn: the window was logged in to it (its cookie there); till its page says otherwise,
	// it is opened at once, not the app's login page.
	LoggedIn bool `json:"loggedIn,omitempty"`
	// Who the window is there (its page tells, irapp.ts), for the app's own page: name, face (a
	// data: URL).
	Name   string `json:"name,omitempty"`
	Avatar string `json:"avatar,omitempty"`
	// Title: the name it goes by in this app (kumo, 2026-11-22: one person, several Iridiums, a
	// window each), given at the top left of its page; its windows' titles, the picker's row, the
	// tray's. The app's alone: the Iridium knows nothing of it. "": its address.
	Title string `json:"title,omitempty"`
	// Face: the Iridium's own (GET /v1/internal/face, a data: URL), what stands for it in the list.
	Face string `json:"face,omitempty"`
	// Proxy: this computer's iridium is fetched through this Iridium when it follows its release
	// (ir join --proxy), not from GitHub.
	Proxy bool `json:"proxy,omitempty"`
	// AlwaysAllow: what this Iridium asks for here is allowed, not asked (the panel's 以后都允许).
	AlwaysAllow bool `json:"alwaysAllow,omitempty"`
}

// Config is the app's own, kept on this computer: what it joins and how. What an Iridium may do
// here is said here, never by the Iridium (docs/app.md "安全").
type Config struct {
	Servers []Server `json:"servers"`
	Current string   `json:"current,omitempty"` // the one in the window
	Name    string   `json:"name,omitempty"`    // this computer's name; "": the host name
	Theme   string   `json:"theme,omitempty"`   // "dark" or "light", as the Iridium's page last was
	// HideLocal: 此电脑 not in the picker's list (the tray's 此电脑 › 隐藏; kumo, 2026-11-23).
	HideLocal bool `json:"hideLocal,omitempty"`
	// LoginItemGone: the login item a former 登录时打开 left was taken away (dropLoginItem).
	LoginItemGone bool    `json:"loginItemGone,omitempty"`
	Zoom          float64 `json:"zoom,omitempty"` // the window's, as last set (⌘= ⌘- ⌘0); 0: 1
	Frame         Frame   `json:"frame,omitzero"` // the main window's place and size, as last left
	// Away: the Iridiums in windows of their own (mainwin.go), opened again at the next start.
	Away []Away `json:"away,omitempty"`
	// NoServer: no Iridium of this computer's own (ir server off, ir start --no-server).
	NoServer bool `json:"noServer,omitempty"`
	// Me: who the person at the window says they are on the app's first page (kumo, 2026-11-22):
	// this computer's Iridium's host is given it, and an invite registers under the name.
	Me Me `json:"me,omitzero"`
}

// Away is an Iridium in a window of its own, where and as large as that window was last.
type Away struct {
	URL   string `json:"url"`
	Frame Frame  `json:"frame,omitzero"`
}

// setAway puts address's window of its own in the list (on), with its frame if one is given,
// or takes it out.
func (c *Config) setAway(address string, on bool, f Frame) {
	i := slices.IndexFunc(c.Away, func(a Away) bool { return a.URL == address })
	switch {
	case !on && i >= 0:
		c.Away = slices.Delete(c.Away, i, i+1)
	case on && i < 0:
		c.Away = append(c.Away, Away{URL: address, Frame: f})
	case on && f.W > 0:
		c.Away[i].Frame = f
	}
}

// away is address's window of its own; nil: none.
func (c Config) away(address string) *Away {
	for i := range c.Away {
		if c.Away[i].URL == address {
			return &c.Away[i]
		}
	}
	return nil
}

// Me is the app's first page's: a name, a face (a data: URL of the cropped image).
type Me struct {
	Name   string `json:"name,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

var cfgMu sync.Mutex

// configPath is the app's file: in the user's config directory, or IR_APP_CONFIG (tests).
func configPath() (string, error) {
	if p := os.Getenv("IR_APP_CONFIG"); p != "" {
		return p, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "Iridium", "app.json"), nil
}

func loadConfig() (Config, error) {
	var c Config
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func saveConfig(c Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// update changes the config on disk under its lock.
func updateConfig(f func(*Config)) (Config, error) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c, err := loadConfig()
	if err != nil {
		return c, err
	}
	f(&c)
	return c, saveConfig(c)
}

// exists: has the app's file been written yet? Not: the app's first start.
func configExists() bool {
	p, err := configPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// setLocal has this computer's Iridium at address in the list, first; shown says it is the one in
// the window. An address it had before (its public address turned on or off) is replaced.
func (c *Config) setLocal(address string, shown bool) {
	if l := c.local(); l != nil {
		if c.Current == l.URL {
			c.Current = address
		}
		l.URL = address
	} else {
		c.Servers = append([]Server{{URL: address, Local: true}}, slices.DeleteFunc(c.Servers, func(s Server) bool { return s.URL == address })...)
	}
	if shown {
		c.Current = address
	}
}

// local is this computer's own Iridium in the list; nil: not there.
func (c *Config) local() *Server {
	for i := range c.Servers {
		if c.Servers[i].Local {
			return &c.Servers[i]
		}
	}
	return nil
}

// add puts address in the list (once), not joined, and makes it the one shown.
func (c *Config) add(address string) {
	if !slices.ContainsFunc(c.Servers, func(s Server) bool { return s.URL == address }) {
		// Shown, not joined: joining is its own switch (kumo, 2026-11-22: only looking at others
		// runs nothing here).
		c.Servers = append(c.Servers, Server{URL: address, Off: true})
	}
	c.Current = address
}

// current is the address shown in the window: the chosen one, else the first.
func (c *Config) current() string {
	if c.Current != "" && slices.ContainsFunc(c.Servers, func(s Server) bool { return s.URL == c.Current }) {
		return c.Current
	}
	if len(c.Servers) > 0 {
		return c.Servers[0].URL
	}
	return ""
}

// normalize is an address as join takes it.
func normalize(s string) (string, error) { return join.Address(s) }

// label is s as the app's lists say it: its name here, else its address.
func (s Server) label() string {
	if s.Title != "" {
		return s.Title
	}
	return hostOf(s.URL)
}

func (c *Config) setOff(address string, off bool) {
	for i := range c.Servers {
		if c.Servers[i].URL == address {
			c.Servers[i].Off = off
		}
	}
}

func (c *Config) remove(address string) {
	c.Servers = slices.DeleteFunc(c.Servers, func(s Server) bool { return s.URL == address })
	c.setAway(address, false, Frame{})
	if c.Current == address {
		c.Current = ""
	}
}

// server is address's entry, for changing in place; nil: not in the list.
func (c *Config) server(address string) *Server {
	for i := range c.Servers {
		if c.Servers[i].URL == address {
			return &c.Servers[i]
		}
	}
	return nil
}

// Frame is a window's place and size.
type Frame struct {
	X, Y, W, H int
}
