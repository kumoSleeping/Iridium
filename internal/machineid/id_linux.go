package machineid

import "os"

// read is systemd's (or D-Bus's) machine ID.
func read() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return ""
}
