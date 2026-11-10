package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/kumoSleeping/iridium/internal/gate"
)

// v25 is the last schema of 0.1 (v0.1.41): what the 0.2 migrations start from.
const v25 = 25

// openAt makes a new database at path at schema version n: migrations 1 to n and their steps, as a
// binary of that version left it. A test fills it, closes it, and Open migrates it from there.
func openAt(t *testing.T, path string, n int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v < n; v++ {
		if _, err := tx.Exec(schema[v]); err != nil {
			t.Fatalf("migration %d: %v", v+1, err)
		}
		if step := steps[v+1]; step != nil {
			if err := step(tx); err != nil {
				t.Fatalf("migration %d: %v", v+1, err)
			}
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, n)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

// fixture is a synthetic 0.1 database (schema 25), with a row or more in every table a 0.2 step
// migrates or drops, and the secrets that let a test in as before: what a migration must keep.
type fixture struct {
	Path string
	T0   time.Time // the rows' times are around it
	// Two ir_web logins to be (panels): Mac has Claude (the first), a passkey, a browser logged in
	// (Cookie) and a phone; Pad has none of these.
	Mac, Pad string
	Cookie   string
	// Two apps to be (channels): QQ has Claude and a TOKEN; Box is an executor (machines), with a
	// TOKEN and a laptop.
	QQ, Box         string
	QQToken, BoxTok string
	// Unused invites: Owner (made with --owner), Plain.
	Owner, Plain string
}

func token(seed string) string {
	s := strings.Repeat(seed, 43)
	return s[:43]
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// makeFixture writes the fixture at version 25 in dir; with upTo above 25 it is opened (and so
// migrated) to the latest and closed again.
func makeFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{
		Path: filepath.Join(t.TempDir(), "iridium.db"),
		T0:   time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		Mac:  "pmacmacmacmacmac", Pad: "ppadpadpadpadpad",
		QQ: "cqqqqqqqqqqqqqqq", Box: "cboxboxboxboxbox",
		Cookie: token("c"), QQToken: "ir_" + token("q"), BoxTok: "ir_" + token("b"),
		Owner: "invowner", Plain: "invplain",
	}
	db := openAt(t, f.Path, v25)
	ns := func(d time.Duration) int64 { return f.T0.Add(d).UnixNano() }
	ms := func(d time.Duration) int64 { return f.T0.Add(d).UnixMilli() }
	passkeys, _ := json.Marshal([]gate.Passkey{{Credential: webauthn.Credential{ID: []byte("cred-mac"), PublicKey: []byte("pk")}, CreatedAt: f.T0, LastUsedAt: f.T0}})
	claude := func(by string) string { return fmt.Sprintf(`{"claudeOwner":{"by":%q,"at":%d}}`, by, ms(0)) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Terminals: two panels, two channels; their permissions, devices, follows.
	exec(`INSERT INTO terminals (id, name, kind, user_handle, passkeys, token_hash, avatar, avatar_version, created_at, last_seen_at) VALUES
		(?, 'kumo mac', 'panel', x'0102', ?, NULL, x'89504e47', 'av1', ?, ?),
		(?, 'pad', 'panel', x'0304', '[]', NULL, NULL, '', ?, 0),
		(?, 'QQ', 'channel', NULL, '[]', ?, NULL, '', ?, ?),
		(?, 'box', 'channel', NULL, '[]', ?, x'89504e47', 'av2', ?, ?)`,
		f.Mac, string(passkeys), ns(0), ns(time.Hour),
		f.Pad, ns(time.Minute),
		f.QQ, sha(f.QQToken), ns(2*time.Minute), ns(time.Hour),
		f.Box, sha(f.BoxTok), ns(3*time.Minute), ns(2*time.Hour))
	exec(`INSERT INTO permissions (terminal, rules) VALUES (?, ?), (?, ?)`, f.Mac, claude("first"), f.QQ, claude("local"))
	exec(`INSERT INTO devices (terminal, device) VALUES (?, 'phone'), (?, 'laptop'), ('local', 'none')`, f.Mac, f.Box)
	exec(`INSERT INTO bot_follows (bot, panel) VALUES (?, ?)`, f.QQ, f.Mac)
	exec(`INSERT INTO machines (terminal, info, at) VALUES (?, '{"id":"x","os":"linux","home":"/home/box","sep":"/","agents":[]}', ?)`, f.Box, ms(0))
	// Invites: the setup link, one --owner, one plain.
	exec(`INSERT INTO invites (id, hash, name, setup, owner, created_at, expires_at) VALUES
		('setup', ?, '', 1, 0, ?, 0), (?, ?, 'phone', 0, 1, ?, ?), (?, ?, '', 0, 0, ?, ?)`,
		sha("s"), ns(0), f.Owner, sha("o"), ns(0), ns(10*time.Minute), f.Plain, sha("p"), ns(0), ns(10*time.Minute))
	// Mac's browser, logged in for a week from now.
	exec(`INSERT INTO logins (hash, terminal, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		sha(f.Cookie), f.Mac, ns(0), ns(0), ns(7*24*time.Hour))
	// Sessions they opened, wrote to, spent on.
	exec(`INSERT INTO openers (session, terminal, at) VALUES ('s-mac', ?, ?), ('s-qq', ?, ?)`, f.Mac, ms(0), f.QQ, ms(0))
	exec(`INSERT INTO inputs (id, session, terminal, text, state, created_at, sent_at, tag) VALUES
		('i1', 's-mac', ?, 'hi', 'sent', ?, ?, ''), ('i2', 's-qq', ?, 'yo', 'sent', ?, ?, 'QQ·张三')`, f.Mac, ms(0), ms(0), f.QQ, ms(0), ms(0))
	exec(`INSERT INTO usage (at, terminal, session, agent, model, input, output, cache_read, cache_write, cost_usd) VALUES
		(?, ?, 's-mac', 'claude', 'haiku', 10, 20, 30, 40, 0.01), (?, ?, 's-qq', 'codex', 'luna', 1, 2, 3, 4, NULL), (?, 'local', 's-l', 'pi', 'flash', 5, 5, 0, 0, NULL)`,
		ms(0), f.Mac, ms(0), f.QQ, ms(0))
	exec(`INSERT INTO secret_uses (secret, session, terminal, command, at) VALUES ('GH', 's-mac', ?, 'gh', ?)`, f.Mac, ms(0))
	// Push: the host's, a panel's, and (a 0.1 leftover) a channel's and a gone terminal's.
	exec(`INSERT INTO push_subscriptions (terminal, endpoint, keys, created_at) VALUES
		('local', 'https://push/local', '{}', ?), (?, 'https://push/mac', '{}', ?), (?, 'https://push/qq', '{}', ?), ('gone', 'https://push/gone', '{}', ?)`,
		ms(0), f.Mac, ms(0), f.QQ, ms(0), ms(0))
	// The chat layer (S4 drops it).
	exec(`INSERT INTO chat_pointers (plugin, chat, session, agent, cwd, updated_at) VALUES (?, 'qq:private:1', 's-qq', 'codex', '/home', ?)`, f.QQ, ms(0))
	exec(`INSERT INTO chat_seen (plugin, chat, session, at) VALUES (?, 'qq:private:1', 's-qq', ?)`, f.QQ, ms(0))
	exec(`INSERT INTO outbox (plugin, chat, type, data, at) VALUES (?, 'qq:private:1', 'reply', '{}', ?)`, f.QQ, ms(0))
	// Layout keyed by machine ID and path (S2/S3).
	exec(`INSERT INTO workspaces (name, path, created_at, creator) VALUES ('ir', '/home/u/ir', ?, ?)`, ms(0), f.Mac)
	exec(`INSERT INTO folder_colors (path, color) VALUES (?, 'red')`, f.Box+":/home/box/x")
	exec(`INSERT INTO folder_order (path, at) VALUES (?, 1)`, f.Box+":/home/box/x")
	exec(`INSERT INTO folder_pins (path, at) VALUES ('/home/u/ir', ?)`, ms(0))
	exec(`INSERT INTO group_order (key, sort) VALUES ('w:1', 0)`)
	exec(`INSERT INTO session_at (session, path) VALUES ('s-mac', ?)`, f.Box+":/home/box/x")
	exec(`INSERT INTO notes (id, body, pos, created_at, updated_at, root) VALUES ('n1', 'note', 1, ?, ?, ''), ('n2', 'boxed', 2, ?, ?, ?)`, ms(0), ms(0), ms(0), ms(0), f.Box+":/home/box")
	exec(`INSERT INTO secrets (name, scope, value, by, created_at, updated_at) VALUES ('GH', 'owner', x'00', ?, ?, ?)`, f.Mac, ms(0), ms(0))
	db.Close()
	return f
}
