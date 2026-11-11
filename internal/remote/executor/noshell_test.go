package executor

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

func TestNoShell(t *testing.T) {
	e := &Executor{o: Options{State: t.TempDir(), Agents: []wire.AgentInfo{{Agent: "dsh", Argv: []string{"/n/node", "/d/bin.js"}}}}, holds: map[string]*holder{}}
	if !e.agentArgv([]string{"/n/node", "/d/bin.js", "--profile", "sdk"}) {
		t.Error("the agent's own argv was refused")
	}
	for _, argv := range [][]string{{"/bin/sh", "-c", "id"}, {"/n/node"}, {"/n/node", "/x.js"}} {
		if e.agentArgv(argv) {
			t.Errorf("%q was taken", argv)
		}
	}
	if _, err := e.start("sh-1", spec{Argv: []string{"/bin/sh", "-c", "id"}}); !wire.IsCode(err, wire.CodeDenied) {
		t.Errorf("a command line started: %v", err)
	}
	if hi := e.hello(); hi.ShellOn() || !hi.NoShell || hi.AgentShell {
		t.Error("the hello does not say so")
	}
}
