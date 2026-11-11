package testagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// CodexVersion is the version the fake Codex says it is in initialize's userAgent.
const CodexVersion = "0.165.0"

// Codex is a fake `codex app-server` (stdio://): it opens a thread and takes what is said to it,
// answering nothing of its own. Enough for a session to be made, which is what a test that starts
// Codex needs; the real Codex would open a thread on the user's own account and write its rollout
// into their ~/.codex (2026-11-11: three tests left 95 of them there).
func Codex() int {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<22)
	n := 0
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil || m.Method == "" || len(m.ID) == 0 {
			continue // a notification, as initialized is
		}
		var result any = map[string]any{}
		switch m.Method {
		case "initialize":
			result = map[string]any{"userAgent": "codex_cli_rs/" + CodexVersion + " (fake)"}
		case "model/list":
			result = map[string]any{"data": []map[string]any{{
				"id": "fake-model", "displayName": "fake", "description": "the fake", "isDefault": true,
				"defaultReasoningEffort": "low",
				"supportedReasoningEfforts": []map[string]any{
					{"reasoningEffort": "low", "description": "low"},
					{"reasoningEffort": "high", "description": "high"},
				},
			}}}
		case "thread/start", "thread/resume":
			var p struct {
				ThreadID string `json:"threadId"`
				Model    string `json:"model"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.ThreadID == "" {
				p.ThreadID = codexThread(n)
				n++
			}
			result = map[string]any{"thread": map[string]any{"id": p.ThreadID}, "model": "fake-model", "approvalPolicy": "never"}
		case "turn/start":
			n++
			result = map[string]any{"turn": map[string]any{"id": codexThread(n)}}
		}
		b, _ := json.Marshal(map[string]any{"id": m.ID, "result": result})
		if _, err := fmt.Fprintf(os.Stdout, "%s\n", b); err != nil {
			return 1
		}
	}
	return 0
}

// codexThread is a thread or turn id the fake opens: one by n, of the shape Codex's own.
func codexThread(n int) string {
	return "01a1c000-0000-7000-8000-" + fmt.Sprintf("%012d", n)
}
