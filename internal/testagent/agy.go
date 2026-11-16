package testagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// AgyVersion is the version the fake agy says it is in initialize's agentInfo.
const AgyVersion = "1.5.9"

// Agy is a fake `agy acp`: it opens a session and answers each prompt with its words echoed and
// "ok", with usage, as agy 1.5.9 does. Enough for a session to be made and talked to, which is
// what a test that starts agy needs; the real agy would talk to Google on the user's account.
func Agy() int {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(AgyVersion)
		return 0
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<24)
	var mu sync.Mutex
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, err := fmt.Fprintf(os.Stdout, "%s\n", b)
		return err == nil
	}
	update := func(sid string, u map[string]any) bool {
		return send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": u}})
	}
	model, level, mode := "fake-flash", "low", "default"
	config := func() []map[string]any {
		return []map[string]any{
			{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": model, "options": []map[string]any{{"value": "fake-flash", "name": "Fake Flash"}, {"value": "fake-pro", "name": "Fake Pro"}}},
			{"id": "thought_level", "name": "Thinking Level", "category": "thought_level", "type": "select", "currentValue": level, "options": []map[string]any{{"value": "low", "name": "Low"}, {"value": "high", "name": "High"}}},
			{"id": "mode", "name": "Mode", "category": "mode", "type": "select", "currentValue": mode, "options": []map[string]any{{"value": "default", "name": "Default"}, {"value": "accept-edits", "name": "Accept edits"}, {"value": "plan", "name": "Plan"}}},
		}
	}
	opened := func(sid string) map[string]any {
		return map[string]any{
			"sessionId": sid,
			"models": map[string]any{"currentModelId": "fake-flash-low", "availableModels": []map[string]any{
				{"modelId": "fake-flash-low", "name": "Fake Flash (Low)"}, {"modelId": "fake-flash-high", "name": "Fake Flash (High)"},
				{"modelId": "fake-pro-low", "name": "Fake Pro (Low)"}, {"modelId": "fake-pro-high", "name": "Fake Pro (High)"},
			}},
			"modes":         map[string]any{"currentModeId": mode},
			"configOptions": config(),
			"_meta":         map[string]any{"antigravity": map[string]any{"tier": map[string]any{"name": "Fake Tier"}}},
		}
	}
	n := 0
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
				ConfigID  string `json:"configId"`
				Value     string `json:"value"`
				Prompt    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil || m.Method == "" || len(m.ID) == 0 {
			continue // an answer, or session/cancel
		}
		sid := m.Params.SessionID
		var result any = map[string]any{}
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "Antigravity ACP Server", "version": AgyVersion}}
		case "session/new":
			n++
			sid = fmt.Sprintf("a9a00000-0000-4000-8000-%012d", n)
			result = opened(sid)
		case "session/resume", "session/load":
			result = opened(sid)
		case "session/set_config_option":
			switch m.Params.ConfigID {
			case "model":
				model = m.Params.Value
			case "thought_level":
				level = m.Params.Value
			case "mode":
				mode = m.Params.Value
			}
			result = map[string]any{"configOptions": config()}
		case "session/prompt":
			for _, p := range m.Params.Prompt {
				if p.Type == "text" && !update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": p.Text}}) {
					return 1
				}
			}
			update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "ok"}})
			update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 100, "size": 1000})
			result = map[string]any{"stopReason": "end_turn", "usage": map[string]any{"inputTokens": 90, "outputTokens": 1, "cachedReadTokens": 9, "cachedWriteTokens": 0, "thoughtTokens": 0, "totalTokens": 100}}
		}
		if !send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result}) {
			return 1
		}
	}
	return 0
}
