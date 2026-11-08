package version

import "testing"

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.4.1": true, "1.2.3": true, "v10.0.12": true,
		"dev": false, "": false, "v0.4.2-dev.3+g1db5968": false, "v0.4.2-dev.3+g1db5968.dirty": false,
		"v1.2": false, "v1.2.3.4": false, "v1..3": false, "v1.2.3-rc1": false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}
