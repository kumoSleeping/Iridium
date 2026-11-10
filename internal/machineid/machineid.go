// Package machineid is a hash of this computer's own ID, which an executor's hello says
// (wire.Hello.MachineID): an app on the Iridium's own computer is told by it, so the host's agents
// are not offered twice (docs/next/decisions.md 22). It guards against a mistake, not an attacker:
// any executor may say any hash.
package machineid

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
)

// Env overrides the computer's ID (tests, and a test instance that joins the main one on the same
// computer): its value is hashed in its place; "none" says no ID.
const Env = "IRIDIUM_MACHINE_ID"

var (
	once sync.Once
	raw  string
)

// Hash is this computer's ID hashed, "" if it has none to be read.
func Hash() string {
	if v, ok := os.LookupEnv(Env); ok {
		return Of(v)
	}
	once.Do(func() { raw = strings.TrimSpace(read()) })
	return Of(raw)
}

// Of is id hashed as Hash gives it; "" for none.
func Of(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == "none" {
		return ""
	}
	sum := sha256.Sum256([]byte("iridium machine " + id))
	return hex.EncodeToString(sum[:16])
}
