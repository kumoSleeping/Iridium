// Package testagent holds the fake agents tests run instead of real ones. Only test files import
// it, so it is never part of the binary. A test binary dispatches to them from its TestMain.
package testagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/hold"
)

// Main runs what the test binary was started as, if not the tests: the holder (`hold ...`), or
// the fake Claude through a link named claude. ok false: run the tests.
func Main() (code int, ok bool) {
	if filepath.Base(os.Args[0]) == "claude" {
		return Claude(), true
	}
	if len(os.Args) > 1 && os.Args[1] == "hold" {
		return hold.Main(os.Args[2:]), true
	}
	return 0, false
}

// Claude is a fake claude -p. It answers initialize, get_usage, get_context_usage and interrupt.
// A message runs a turn that says "ok: <text>" (its source prefix taken off), costs $0.01 more,
// and goes into a transcript under $CLAUDE_CONFIG_DIR. One ending in ASK asks first and goes on
// once answered; one ending in crash exits 3.
func Claude() int {
	session := ""
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--session-id="); ok {
			session = v
		}
		if v, ok := strings.CutPrefix(a, "--resume="); ok {
			session = v
		}
	}
	cwd, _ := os.Getwd()
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", strings.ReplaceAll(cwd, "/", "-"))
	os.MkdirAll(dir, 0o700)
	record, _ := os.OpenFile(filepath.Join(dir, session+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	out := bufio.NewWriter(os.Stdout)
	say := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	note := func(v any) {
		b, _ := json.Marshal(v)
		record.Write(append(b, '\n'))
	}
	answer := func(id string, body any) {
		say(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": body}})
	}
	cost := 0.0
	// A resumed session goes on from its running total, as Claude's does.
	if b, err := os.ReadFile(filepath.Join(dir, session+".cost")); err == nil {
		fmt.Sscan(string(b), &cost)
	}
	usage := func() map[string]any {
		return map[string]any{"fake-model": map[string]any{"inputTokens": int(cost * 1000), "outputTokens": int(cost * 100), "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0, "costUSD": cost}}
	}
	textOf := func(f map[string]any) string {
		return f["message"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	var pending map[string]any // the message waiting on an ask
	finish := func(f map[string]any) {
		text := textOf(f)
		if _, rest, ok := strings.Cut(text, "] "); ok && strings.HasPrefix(text, "[") {
			text = rest
		}
		reply := "ok: " + text
		for _, d := range []string{"ok: ", text} {
			say(map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": d}}})
		}
		say(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": reply}}, "usage": map[string]any{"input_tokens": 10}}})
		note(map[string]any{"type": "assistant", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": reply}}}})
		cost += 0.01
		os.WriteFile(filepath.Join(dir, session+".cost"), []byte(fmt.Sprint(cost)), 0o600)
		say(map[string]any{"type": "result", "subtype": "success", "num_turns": 1, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1}, "total_cost_usd": cost, "modelUsage": usage()})
		say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "completed"})
		say(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "idle"})
	}
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return 0
		}
		var f map[string]any
		if json.Unmarshal(line, &f) != nil {
			return 1 // as the real CLI does
		}
		switch f["type"] {
		case "control_request":
			id := f["request_id"].(string)
			switch f["request"].(map[string]any)["subtype"] {
			case "initialize":
				say(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "pending_permission_requests": []any{},
					"response": map[string]any{"claude_code_version": "fake", "current_permission_mode": "default", "models": []any{map[string]any{"value": "default", "displayName": "Default"}}, "commands": []any{}}}})
			case "get_usage":
				answer(id, map[string]any{"session": map[string]any{"total_cost_usd": cost, "model_usage": usage()}, "rate_limits": map[string]any{"limits": []any{map[string]any{"group": "session", "percent": 10, "resets_at": "2026-01-01T00:00:00Z"}}}})
			case "get_context_usage":
				answer(id, map[string]any{"totalTokens": 100, "maxTokens": 1000, "categories": []any{map[string]any{"id": "messages", "name": "Messages", "tokens": 100, "kind": "used"}}})
			case "interrupt":
				answer(id, map[string]any{"still_queued": []any{}})
			}
		case "control_response":
			if pending != nil {
				finish(pending)
				pending = nil
			}
		case "user":
			text := textOf(f)
			if strings.HasSuffix(text, "crash") {
				return 3
			}
			say(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "running"})
			say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "started"})
			say(map[string]any{"type": "system", "subtype": "init", "session_id": session, "model": "fake-model"})
			note(map[string]any{"type": "user", "uuid": f["uuid"], "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": text}})
			if strings.HasSuffix(text, "ASK") {
				pending = f
				say(map[string]any{"type": "control_request", "request_id": "ask-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{"command": "ls"}, "tool_use_id": "toolu_1"}})
				continue
			}
			finish(f)
		}
	}
}
