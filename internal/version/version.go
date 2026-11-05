// Package version is the binary's version, set at build time:
//
//	go build -ldflags "-X github.com/kumoSleeping/iridium/internal/version.Version=1.2.3" ./cmd/iridium
package version

// Version is "dev" for a build that did not set it.
var Version = "dev"
