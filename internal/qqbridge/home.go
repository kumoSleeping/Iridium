// Package qqbridge is the QQ plugin (cmd/iridium-qq): it holds a QQ bot's gateway connection and
// passes its owners' private chats to Iridium's plugin API and Iridium's replies back. Iridium
// itself knows nothing of QQ (plan "插件").
package qqbridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/qq"
)

// Home is the plugin's directory: a directory per bot under bots/, and a scan under way.
// IRIDIUM_QQ_HOME, else ~/.config/iridium-qq.
func Home() string {
	if h := os.Getenv("IRIDIUM_QQ_HOME"); h != "" {
		return h
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "iridium-qq")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "iridium-qq")
}

// BotsDir under Home holds a directory per bot, named by its AppID: its config, TOKEN and
// state. A bot's directory is its home in what follows (LoadConfig, Token, Read, Write).
const BotsDir = "bots"

// BotHome is the directory of the bot appID under root.
func BotHome(root, appID string) string { return filepath.Join(root, BotsDir, appID) }

// ValidAppID: an AppID that names a directory and a service (QQ's are digits).
func ValidAppID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// Bots lists the AppIDs of the bots under root that have a config, in order.
func Bots(root string) []string {
	ents, _ := os.ReadDir(filepath.Join(root, BotsDir))
	var out []string
	for _, e := range ents {
		if !e.IsDir() || !ValidAppID(e.Name()) {
			continue
		}
		if _, ok, _ := LoadConfig(BotHome(root, e.Name())); ok {
			out = append(out, e.Name())
		}
	}
	return out
}

// FreeName is the terminal name for the bot appID: base (its name on QQ) made a terminal's name,
// else QQ; with 2, 3… after it while another bot under root, or one of taken, has it (case aside).
func FreeName(root, appID, base string, taken ...string) string {
	used := map[string]bool{}
	for _, n := range taken {
		used[strings.ToLower(n)] = true
	}
	for _, id := range Bots(root) {
		if c, _, _ := LoadConfig(BotHome(root, id)); id != appID {
			used[strings.ToLower(c.Name)] = true
		}
	}
	base = nameOf(base)
	if base == "" {
		base = "QQ"
	}
	for i := 1; ; i++ {
		n := base
		if i > 1 {
			suffix := strconv.Itoa(i)
			r := []rune(base)
			if len(r)+len(suffix) > gate.MaxNameLen {
				r = r[:gate.MaxNameLen-len(suffix)]
			}
			n = strings.TrimSpace(string(r)) + suffix
		}
		if !used[strings.ToLower(n)] {
			return n
		}
	}
}

// nameOf makes a bot's name on QQ a terminal's name: what a name may not hold dropped, cut to its
// length; "" if nothing is left.
func nameOf(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || unicode.IsSpace(r) || strings.ContainsRune("-_.", r) {
			return r
		}
		return ' '
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > gate.MaxNameLen {
		s = strings.TrimSpace(string(r[:gate.MaxNameLen]))
	}
	n, err := gate.NormalizeName(s)
	if err != nil {
		return ""
	}
	return n
}

// MoveLegacy moves the one bot of before (its files right in root) into its own directory, and
// gives its AppID; "" if there was none.
func MoveLegacy(root string) string {
	var c Config
	if ok, err := Read(root, ConfigFile, &c); !ok || err != nil || !ValidAppID(c.AppID) {
		return ""
	}
	dir := BotHome(root, c.AppID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	// The config last: while it is in root, another process may still be moving.
	for _, f := range []string{TokenFile, StateFile, StatusFile, PairFile, ConfigFile} {
		_ = os.Rename(filepath.Join(root, f), filepath.Join(dir, f))
	}
	return c.AppID
}

// Files under a bot's home (BindFile and QRFile: under Home, the bot not known yet).
const (
	ConfigFile = "config.json" // Config, 0600: it holds the bot's secret
	TokenFile  = "token"       // Iridium's TOKEN for this plugin (ir token --into)
	StateFile  = "state.json"  // State
	StatusFile = "status.json" // Status
	BindFile   = "bind.json"   // a bind task waiting for its scan (qq.Bind)
	QRFile     = "qr.png"      // its QR code
	PairFile   = "pair.json"   // Pair
)

// Config is what setup decided.
type Config struct {
	AppID   string   `json:"appId"`
	Secret  string   `json:"secret"`
	Owners  []string `json:"owners,omitempty"` // openids under this bot whose chats go to Iridium
	Iridium string   `json:"iridium"`          // Iridium's HTTP address
	Name    string   `json:"name"`             // the terminal's name in Iridium
}

// IsOwner reports whether openid is one of the owners.
func (c Config) IsOwner(openid string) bool { return slices.Contains(c.Owners, openid) }

// State is what the running plugin keeps across restarts.
type State struct {
	AppID      string                `json:"appId,omitempty"` // whose session this is
	Gateway    qq.Session            `json:"gateway"`
	LastEvent  int64                 `json:"lastEvent"`          // Iridium's last event handled; 0: none yet
	Markdown   string                `json:"markdown,omitempty"` // yes | no: whether QQ takes this bot's Markdown
	MarkdownAt int64                 `json:"markdownAt,omitempty"`
	Avatar     string                `json:"avatar,omitempty"` // the avatar put on the terminal (a hash)
	Menus      string                `json:"menus,omitempty"`  // the menus pushed last (a hash)
	Chats      map[string]*ChatState `json:"chats,omitempty"`
}

// ChatState is one private chat's passive replies and what waits for the owner's next message.
type ChatState struct {
	MsgID   string     `json:"msgId,omitempty"` // the last message the owner sent
	MsgAt   int64      `json:"msgAt,omitempty"`
	Used    int        `json:"used,omitempty"` // passive replies to it so far
	Waiting []Outgoing `json:"waiting,omitempty"`
}

// Status is how the running plugin stands, for iridium-qq status.
type Status struct {
	PID          int    `json:"pid"`
	QQ           bool   `json:"qq"` // connected to QQ
	QQError      string `json:"qqError,omitempty"`
	Iridium      bool   `json:"iridium"` // its event stream is open
	IridiumError string `json:"iridiumError,omitempty"`
	Bot          string `json:"bot,omitempty"` // the bot's name on QQ
	At           int64  `json:"at"`
}

// Pair is a pairing code: the next private message with it makes its sender an owner.
type Pair struct {
	Code    string `json:"code"`
	Expires int64  `json:"expires"`
}

// Read reads Home's file name as JSON into v; false if it is not there.
func Read(home, name string, v any) (bool, error) {
	b, err := os.ReadFile(filepath.Join(home, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// Write writes v as JSON to Home's file name, 0600, in place at once.
func Write(home, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(home, name, append(b, '\n'))
}

// WriteFile writes b to Home's file name, 0600, in place at once.
func WriteFile(home, name string, b []byte) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(home, "."+name+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o600)
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(home, name))
	}
	return err
}

// Token reads the TOKEN file ("" if none).
func Token(home string) string {
	b, err := os.ReadFile(filepath.Join(home, TokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// LoadConfig reads the config; ok false if setup has not made one.
func LoadConfig(home string) (Config, bool, error) {
	var c Config
	ok, err := Read(home, ConfigFile, &c)
	if c.Name == "" {
		c.Name = "QQ"
	}
	return c, ok && c.AppID != "", err
}

// TakePair checks text against the pairing code waiting, using it up if it matches.
func TakePair(home, text string) bool {
	var p Pair
	if ok, err := Read(home, PairFile, &p); !ok || err != nil || p.Code == "" {
		return false
	}
	if time.Now().UnixMilli() > p.Expires {
		os.Remove(filepath.Join(home, PairFile))
		return false
	}
	if strings.TrimSpace(text) != p.Code {
		return false
	}
	os.Remove(filepath.Join(home, PairFile))
	return true
}
