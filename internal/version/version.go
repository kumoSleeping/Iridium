// Package version is the binary's version, set at build time:
//
//	go build -ldflags "-X github.com/kumoSleeping/iridium/internal/version.Version=1.2.3" ./cmd/iridium
package version

import "strings"

// Version is "dev" for a build that did not set it.
var Version = "dev"

// Release reports whether this is a tagged release (v1.2.3, as release.yml stamps it), not a build
// from source ("dev", or scripts/version.sh's v1.2.4-dev.3+g1db5968).
func Release() bool { return IsRelease(Version) }

// IsRelease reports whether v is a bare v1.2.3: no pre-release, no build.
func IsRelease(v string) bool {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}
