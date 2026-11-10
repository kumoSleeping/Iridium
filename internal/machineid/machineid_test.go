package machineid

import "testing"

func TestHash(t *testing.T) {
	t.Setenv(Env, "abc")
	if Hash() != Of("abc") || len(Hash()) != 32 {
		t.Errorf("Hash() = %q", Hash())
	}
	t.Setenv(Env, "none")
	if Hash() != "" {
		t.Errorf("none hashed: %q", Hash())
	}
	if Of("a") == Of("b") {
		t.Error("two IDs, one hash")
	}
}
