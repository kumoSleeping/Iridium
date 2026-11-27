//go:build windows

package dsh

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func discoverDesktopOS() (DesktopInstall, bool) {
	roots := []struct {
		key  registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	for _, r := range roots {
		k, err := registry.OpenKey(r.key, r.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, err := k.ReadSubKeyNames(-1)
		_ = k.Close()
		if err != nil {
			continue
		}
		for _, name := range names {
			sk, err := registry.OpenKey(r.key, r.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			disp, _, _ := sk.GetStringValue("DisplayName")
			loc, _, _ := sk.GetStringValue("InstallLocation")
			icon, _, _ := sk.GetStringValue("DisplayIcon")
			_ = sk.Close()
			if !isDesktopDisplayName(disp) {
				continue
			}
			for _, cand := range []string{loc, stripIconIndex(icon)} {
				if inst, ok := ResolveDesktopRoot(cand); ok {
					return inst, true
				}
			}
		}
	}
	return DesktopInstall{}, false
}

func isDesktopDisplayName(name string) bool {
	s := strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(s, "deepseek harness") || strings.Contains(s, "dsh desktop")
}

func stripIconIndex(icon string) string {
	icon = strings.TrimSpace(strings.Trim(icon, `"`))
	if i := strings.LastIndex(icon, ","); i > 0 {
		icon = strings.TrimSpace(icon[:i])
	}
	return strings.Trim(icon, `"`)
}

func defaultDesktopRoots() []string {
	var out []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		out = append(out,
			filepath.Join(local, "Programs", "DeepSeek Harness"),
			filepath.Join(local, "DeepSeek Harness"),
		)
	}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		out = append(out, filepath.Join(pf, "DeepSeek Harness"))
	}
	return out
}
