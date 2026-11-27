package ghenv

import (
	"slices"
	"strings"
	"testing"
)

// Iridium's entries go after git's own config in the environment, an empty helper first; Without
// gives the environment as it was.
func TestEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=x"}
	env := Env(slices.Clone(base), "/opt/ir/iridium", "/state/bin")
	want := []string{"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_1=" + helperKey, "GIT_CONFIG_VALUE_1=", "GIT_CONFIG_KEY_2=" + helperKey,
		"GIT_CONFIG_VALUE_2=!'/opt/ir/iridium' ir github credential", "PATH=/state/bin:/usr/bin", ShimVar + "=/state/bin", "GIT_CONFIG_KEY_0=user.name"}
	for _, w := range want {
		if !slices.Contains(env, w) {
			t.Errorf("no %q in %v", w, env)
		}
	}
	back := Without(env)
	if get(back, "GIT_CONFIG_COUNT") != "1" || get(back, countVar) != "" {
		t.Errorf("without: %v", back)
	}
	// None before: no count left.
	none := Without(Env([]string{"PATH=/usr/bin"}, "/x/iridium", ""))
	if strings.Contains(strings.Join(none, "\n"), "GIT_CONFIG_COUNT") {
		t.Errorf("without, none before: %v", none)
	}
	if got := Env([]string{"A=1"}, "", ""); len(got) != 1 {
		t.Errorf("no binary: %v", got)
	}
}
