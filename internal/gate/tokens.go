package gate

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Bearer tokens are 32 random bytes in base64url (43 characters). Channel tokens carry a
// prefix so they are recognisable in config files and by secret scanners.
const (
	tokenBytes         = 32
	tokenLen           = 43
	channelTokenPrefix = "ir_"
)

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b) // never fails; see crypto/rand
	return b
}

// newToken returns a fresh secret for an invite, a session cookie or a channel.
func newToken() string { return base64.RawURLEncoding.EncodeToString(randomBytes(tokenBytes)) }

func newChannelToken() string { return channelTokenPrefix + newToken() }

// newID returns a random lowercase ID of 8*n/5 characters.
func newID(n int) string { return idEncoding.EncodeToString(randomBytes(n)) }

// hashToken is how tokens are stored and looked up.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// wellFormed reports whether s looks like a token from newToken, so junk is refused
// before any lookup.
func wellFormed(s string) bool {
	if len(s) != tokenLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func wellFormedChannel(s string) bool {
	rest, ok := strings.CutPrefix(s, channelTokenPrefix)
	return ok && wellFormed(rest)
}

func hashEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
