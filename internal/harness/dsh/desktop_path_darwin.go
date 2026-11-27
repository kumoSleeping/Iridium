//go:build darwin

package dsh

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func discoverDesktopOS() (DesktopInstall, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "mdfind", `kMDItemDisplayName == "DeepSeek Harness*" || kMDItemCFBundleIdentifier == "*deepseek*harness*"`).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasSuffix(line, ".app") {
				if inst, ok := ResolveDesktopRoot(line); ok {
					return inst, true
				}
			}
		}
	}
	return DesktopInstall{}, false
}

func defaultDesktopRoots() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, "Applications", "DeepSeek Harness.app"))
	}
	out = append(out, "/Applications/DeepSeek Harness.app")
	return out
}
