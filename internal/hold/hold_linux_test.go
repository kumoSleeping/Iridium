package hold_test

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/kumoSleeping/iridium/internal/platform"
)

// The holder under systemd.
func TestSystemd(t *testing.T) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("no systemd-run")
	}
	if err := exec.Command("systemctl", "--user", "is-system-running").Run(); err != nil {
		if out, _ := exec.Command("systemctl", "--user", "is-system-running").Output(); !bytes.HasPrefix(out, []byte("degraded")) {
			t.Skip("no user systemd")
		}
	}
	testUnits(t, platform.Systemd{})
}
