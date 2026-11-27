//go:build !windows && !darwin

package dsh

import (
	"os"
	"path/filepath"
	"strings"
)

func discoverDesktopOS() (DesktopInstall, bool) {
	var dirs []string
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "applications"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "applications"))
	}
	dirs = append(dirs, "/usr/local/share/applications", "/usr/share/applications")
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if exe := parseDesktopEntry(string(b)); exe != "" {
				if inst, ok := ResolveDesktopRoot(exe); ok {
					return inst, true
				}
			}
		}
	}
	return DesktopInstall{}, false
}

// parseDesktopEntry extracts the executable path from a freedesktop .desktop file if its Name
// identifies DeepSeek Harness.
func parseDesktopEntry(content string) string {
	var name, execLine string
	inEntry := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		if v, ok := strings.CutPrefix(line, "Name="); ok && name == "" {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "Exec="); ok && execLine == "" {
			execLine = strings.TrimSpace(v)
		}
	}
	lower := strings.ToLower(name)
	if !strings.Contains(lower, "deepseek harness") && !strings.Contains(lower, "dsh desktop") {
		return ""
	}
	return firstExecToken(execLine)
}

func firstExecToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		if end := strings.IndexByte(s[1:], '"'); end >= 0 {
			return s[1 : 1+end]
		}
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func defaultDesktopRoots() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".local", "share", "deepseek-harness"),
			filepath.Join(home, ".local", "opt", "deepseek-harness"),
		)
	}
	out = append(out,
		"/opt/DeepSeek Harness",
		"/opt/deepseek-harness",
		"/usr/lib/deepseek-harness",
	)
	return out
}
