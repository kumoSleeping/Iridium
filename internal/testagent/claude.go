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

// Claude is a fake claude -p. It answers initialize, get_usage, get_context_usage, interrupt,
// get_settings, apply_flag_settings, set_model and set_permission_mode. A message runs a turn
// that says "ok: <text>" (its source prefix taken off), costs $0.01 more, and goes into a
// transcript under $CLAUDE_CONFIG_DIR. By how the message ends:
//
//   - ASK asks to run `ls` first and goes on once answered; ASKASK then asks for `ls -la` too.
//     Both suggest the rule "ls *".
//   - WAIT runs for half a second (an interrupt ends it at once); messages that come meanwhile
//     join it, each answered "ok: <text>" in the same turn.
//   - crash begins the turn, then exits 3. Resumed with CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1, it
//     runs that turn again by itself, saying "ok: continued". crash! exits the same way but is
//     never run again by itself; crashloop crashes again each time it is run again.
//   - QUOTA is refused for the quota, which comes back in a second.
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
	recordPath := filepath.Join(dir, session+".jsonl")
	lastUser := lastUserText(recordPath)
	record, _ := os.OpenFile(recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
	unprefixed := func(text string) string {
		if _, rest, ok := strings.Cut(text, "] "); ok && strings.HasPrefix(text, "[") {
			return rest
		}
		return text
	}
	stamp := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }
	assistant := func(reply string) {
		for _, d := range []string{reply[:min(4, len(reply))], reply[min(4, len(reply)):]} {
			say(map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": d}}})
		}
		say(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": reply}}, "usage": map[string]any{"input_tokens": 10}}})
		note(map[string]any{"type": "assistant", "timestamp": stamp(), "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": reply}}}})
	}
	begin := func(f map[string]any) {
		say(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "running"})
		if f != nil {
			say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "started"})
		}
		say(map[string]any{"type": "system", "subtype": "init", "session_id": session, "model": "fake-model"})
		if f != nil {
			note(map[string]any{"type": "user", "uuid": f["uuid"], "timestamp": stamp(), "message": map[string]any{"role": "user", "content": textOf(f)}})
		}
	}
	end := func(f map[string]any, subtype string) {
		cost += 0.01
		os.WriteFile(filepath.Join(dir, session+".cost"), []byte(fmt.Sprint(cost)), 0o600)
		say(map[string]any{"type": "result", "subtype": subtype, "num_turns": 1, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1}, "total_cost_usd": cost, "modelUsage": usage()})
		if f != nil {
			say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "completed"})
		}
		say(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "idle"})
	}
	finish := func(f map[string]any, steered []map[string]any) {
		assistant("ok: " + unprefixed(textOf(f)))
		for _, s := range steered {
			assistant("ok: " + unprefixed(textOf(s)))
			say(map[string]any{"type": "command_lifecycle", "command_uuid": s["uuid"], "state": "completed"})
		}
		end(f, "success")
	}
	ask := func(id, command string) {
		say(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{"command": command}, "tool_use_id": "toolu_" + id,
			"permission_suggestions": []any{map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "Bash", "ruleContent": "ls *"}}, "behavior": "allow", "destination": "localSettings"}}}})
	}

	// Run the interrupted turn again by itself, before initialize's answer, as Claude does.
	if os.Getenv("CLAUDE_CODE_RESUME_INTERRUPTED_TURN") == "1" && (strings.HasSuffix(lastUser, "crash") || strings.HasSuffix(lastUser, "crashloop")) {
		begin(nil)
		if strings.HasSuffix(lastUser, "crashloop") {
			return 3
		}
		assistant("ok: continued")
		end(nil, "success")
	}

	lines := make(chan []byte)
	go func() {
		in := bufio.NewReader(os.Stdin)
		for {
			line, err := in.ReadBytes('\n')
			if err != nil {
				close(lines)
				return
			}
			lines <- line
		}
	}()
	var pending map[string]any // the message waiting on an ask
	asked := 0
	var waiting map[string]any // a WAIT turn
	var steered []map[string]any
	var timer <-chan time.Time
	for {
		var line []byte
		select {
		case l, ok := <-lines:
			if !ok {
				return 0
			}
			line = l
		case <-timer:
			timer = nil
			finish(waiting, steered)
			waiting, steered = nil, nil
			continue
		}
		var f map[string]any
		if json.Unmarshal(line, &f) != nil {
			return 1 // as the real CLI does
		}
		switch f["type"] {
		case "control_request":
			id := f["request_id"].(string)
			req := f["request"].(map[string]any)
			switch req["subtype"] {
			case "initialize":
				say(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "pending_permission_requests": []any{},
					"response": map[string]any{"claude_code_version": "fake", "current_permission_mode": modeOf(os.Args), "models": []any{map[string]any{"value": "default", "displayName": "Default"}, map[string]any{"value": "haiku", "displayName": "Haiku"}}, "commands": []any{map[string]any{"name": "compact", "description": "Compact the conversation", "argumentHint": "<instructions>"}},
						"account": map[string]any{"email": "user@example.com", "subscriptionType": "Claude Pro"}}}})
			case "get_usage":
				answer(id, map[string]any{"session": map[string]any{"total_cost_usd": cost, "model_usage": usage()}, "rate_limits": map[string]any{"limits": []any{map[string]any{"group": "session", "percent": 10, "resets_at": "2026-01-01T00:00:00Z"}}}})
			case "get_context_usage":
				answer(id, map[string]any{"totalTokens": 100, "maxTokens": 1000, "categories": []any{map[string]any{"id": "messages", "name": "Messages", "tokens": 100, "kind": "used"}}})
			case "get_settings":
				answer(id, map[string]any{"applied": map[string]any{"effort": "medium", "ultracodeRequested": false}})
			case "apply_flag_settings", "set_model", "set_permission_mode":
				answer(id, map[string]any{})
			case "interrupt":
				answer(id, map[string]any{"still_queued": []any{}})
				if waiting != nil {
					timer = nil
					note(map[string]any{"type": "user", "timestamp": stamp(), "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}}})
					end(waiting, "error_during_execution")
					for _, s := range steered {
						say(map[string]any{"type": "command_lifecycle", "command_uuid": s["uuid"], "state": "completed"})
					}
					waiting, steered = nil, nil
				}
			}
		case "control_response":
			if pending != nil {
				text := textOf(pending)
				if strings.HasSuffix(text, "ASKASK") && asked == 1 {
					asked++
					ask("ask-2", "ls -la")
					continue
				}
				finish(pending, nil)
				pending = nil
			}
		case "user":
			text := textOf(f)
			if waiting != nil {
				// Taken into the running turn, as the CLI does with a message mid-turn.
				say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "started"})
				note(map[string]any{"type": "user", "uuid": f["uuid"], "timestamp": stamp(), "message": map[string]any{"role": "user", "content": text}})
				steered = append(steered, f)
				continue
			}
			begin(f)
			switch {
			case strings.HasSuffix(text, "crash"), strings.HasSuffix(text, "crash!"), strings.HasSuffix(text, "crashloop"):
				record.Close()
				return 3
			case strings.HasSuffix(text, "ASK"):
				pending, asked = f, 1
				ask("ask-1", "ls")
				continue
			case strings.HasSuffix(text, "WAIT"):
				waiting = f
				timer = time.After(500 * time.Millisecond)
				continue
			case strings.HasSuffix(text, "QUOTA"):
				back := time.Now().Add(time.Second).Unix()
				say(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": back, "rateLimitType": "five_hour"}})
				assistant("You've hit your limit")
				end(f, "error_during_execution")
				continue
			}
			finish(f, nil)
		}
	}
}

func modeOf(args []string) string {
	for i, a := range args {
		if a == "--permission-mode" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "default"
}

// lastUserText is the text of the last user line in a transcript.
func lastUserText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	last := ""
	for _, l := range strings.Split(string(b), "\n") {
		var f struct {
			Type    string `json:"type"`
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(l), &f) == nil && f.Type == "user" {
			if s, ok := f.Message.Content.(string); ok {
				last = s
			}
		}
	}
	return last
}
