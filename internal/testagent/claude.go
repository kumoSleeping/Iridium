// Package testagent holds the fake agents tests run instead of real ones. Only test files import
// it, so it is never part of the binary. A test binary dispatches to them from its TestMain.
package testagent

import (
	"bufio"
	"cmp"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	if os.Getenv("IRIDIUM_FAKE_DSH") != "" {
		return Dsh(), true
	}
	if len(os.Args) > 1 && os.Args[1] == "hold" {
		return hold.Main(os.Args[2:]), true
	}
	return 0, false
}

// Claude is a fake claude -p. It answers initialize, get_usage, get_context_usage, interrupt,
// get_settings, apply_flag_settings, set_model and set_permission_mode; the model, the effort level
// and ultracode are kept as --model, --effort, --settings and those set them (default runs
// fake-model, haiku fake-haiku, which takes no effort level and no ultracode). A message runs a turn
// that says "ok: <text>" (its source prefix taken off), costs $0.01 more, and goes into a
// transcript under $CLAUDE_CONFIG_DIR. By how the message ends:
//
//   - ASK asks to run `ls` first and goes on once answered; ASKASK then asks for `ls -la` too.
//     Both suggest the rule "ls *".
//   - QUESTION asks two questions (AskUserQuestion: a color, one of Red or Blue; sizes, any of S,
//     M, L) and answers "ok: answers <question>=<answer>; ...", by question.
//   - WAIT runs for half a second (an interrupt ends it at once); messages that come meanwhile
//     join it, each answered "ok: <text>" in the same turn.
//   - crash begins the turn, then exits 3. Resumed with CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1, it
//     runs that turn again by itself, saying "ok: continued". crash! exits the same way but is
//     never run again by itself; crashloop crashes again each time it is run again.
//   - QUOTA is refused for the quota, which comes back in half a second to one and a half.
//
// /compact compacts for three seconds (a compacting status, then the boundary with its figures).
// /clear (as the whole message, its source prefix aside) clears it as Claude's own does: a
// conversation_reset, then it goes on in the same process as a conversation of a new id, the
// command its first lines.
// compactFor is how long a /compact takes.
var compactFor = 3 * time.Second

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
	model, effort, ultra := cmp.Or(argOf(os.Args, "--model"), "default"), cmp.Or(argOf(os.Args, "--effort"), "medium"), false
	if v := argOf(os.Args, "--settings"); v != "" {
		var st struct {
			Ultracode bool `json:"ultracode"`
		}
		json.Unmarshal([]byte(v), &st)
		ultra = st.Ultracode
	}
	resolved := func() string {
		if model == "haiku" {
			return "fake-haiku"
		}
		return "fake-model"
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
	note := func(v map[string]any) {
		// Each line says where and which session, as Claude's own do.
		v["cwd"], v["sessionId"] = cwd, session
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
		say(map[string]any{"type": "system", "subtype": "init", "session_id": session, "model": resolved()})
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
	// open: the asks waiting for an answer, as their requests went; initialize names them again
	// (pending_permission_requests), as the CLI does for a daemon that comes back.
	open := []any{}
	ask := func(id, command string) {
		req := map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{"command": command}, "tool_use_id": "toolu_" + id,
			"permission_suggestions": []any{map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "Bash", "ruleContent": "ls *"}}, "behavior": "allow", "destination": "localSettings"}}}}
		open = []any{req}
		// As the CLI does: the call in an assistant message (its usage the context so far), then the ask.
		say(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_" + id, "name": "Bash", "input": map[string]any{"command": command}}}, "usage": map[string]any{"input_tokens": 10}}})
		say(req)
	}
	question := func(id string) {
		qs := []any{
			map[string]any{"question": "Which color?", "header": "Color", "multiSelect": false, "options": []any{map[string]any{"label": "Red"}, map[string]any{"label": "Blue", "description": "the sea"}}},
			map[string]any{"question": "Which sizes?", "header": "Sizes", "multiSelect": true, "options": []any{map[string]any{"label": "S"}, map[string]any{"label": "M"}, map[string]any{"label": "L"}}},
		}
		req := map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "input": map[string]any{"questions": qs}, "tool_use_id": "toolu_" + id, "requires_user_interaction": true}}
		open = []any{req}
		say(req)
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
	var compacting map[string]any // a /compact running
	var compacted <-chan time.Time
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
		case <-compacted:
			compacted = nil
			pre, post := 120000, 9000
			note(map[string]any{"type": "system", "subtype": "compact_boundary", "timestamp": stamp(), "content": "Conversation compacted", "compactMetadata": map[string]any{"trigger": "manual", "preTokens": pre, "postTokens": post, "durationMs": compactFor.Milliseconds()}})
			note(map[string]any{"type": "user", "isCompactSummary": true, "timestamp": stamp(), "message": map[string]any{"role": "user", "content": "This session is being continued from a previous conversation."}})
			note(map[string]any{"type": "user", "uuid": compacting["uuid"], "timestamp": stamp(), "message": map[string]any{"role": "user", "content": "<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args></command-args>"}})
			say(map[string]any{"type": "system", "subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "manual", "pre_tokens": pre, "post_tokens": post}})
			say(map[string]any{"type": "system", "subtype": "status", "status": nil, "compact_result": "success"})
			end(compacting, "success")
			compacting = nil
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
				say(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "pending_permission_requests": open,
					"response": map[string]any{"claude_code_version": "fake", "current_permission_mode": modeOf(os.Args), "models": []any{
						map[string]any{"value": "default", "resolvedModel": "fake-model", "displayName": "Default (recommended)", "description": "Fake 1 · For tests", "supportsEffort": true, "supportedEffortLevels": []string{"low", "medium", "high", "xhigh", "max"}},
						map[string]any{"value": "haiku", "resolvedModel": "fake-haiku", "displayName": "Haiku"}}, "commands": []any{map[string]any{"name": "compact", "description": "Compact the conversation", "argumentHint": "<instructions>"}},
						"account": map[string]any{"email": "user@example.com", "subscriptionType": "Claude Pro"}}}})
			case "get_usage":
				answer(id, map[string]any{"session": map[string]any{"total_cost_usd": cost, "model_usage": usage()}, "rate_limits": map[string]any{"limits": []any{map[string]any{"group": "session", "percent": 10, "resets_at": "2026-01-01T00:00:00Z"}}}})
			case "get_context_usage":
				answer(id, map[string]any{"totalTokens": 100, "maxTokens": 1000, "categories": []any{map[string]any{"id": "messages", "name": "Messages", "tokens": 100, "kind": "used"}}})
			case "get_settings":
				avail := model != "haiku"
				answer(id, map[string]any{"applied": map[string]any{"model": resolved(), "effort": effort, "ultracode": ultra && avail, "ultracodeRequested": ultra, "ultracodeAvailable": avail}})
			case "apply_flag_settings":
				st, _ := req["settings"].(map[string]any)
				if v, ok := st["effortLevel"].(string); ok {
					effort = v
				}
				if v, ok := st["ultracode"].(bool); ok {
					ultra = v
				}
				answer(id, map[string]any{})
			case "set_model":
				model, _ = req["model"].(string)
				answer(id, map[string]any{})
			case "set_permission_mode":
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
				open = []any{}
				text := textOf(pending)
				if strings.HasSuffix(text, "QUESTION") {
					var r struct {
						Response struct {
							Response struct {
								UpdatedInput struct {
									Answers map[string]string `json:"answers"`
								} `json:"updatedInput"`
							} `json:"response"`
						} `json:"response"`
					}
					json.Unmarshal(line, &r)
					var said []string
					for q, a := range r.Response.Response.UpdatedInput.Answers {
						said = append(said, q+"="+a)
					}
					sort.Strings(said)
					assistant("ok: answers " + strings.Join(said, "; "))
					end(pending, "success")
					pending = nil
					continue
				}
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
			if unprefixed(text) == "/compact" {
				// Compacts for compactFor, as Claude's does: a status, then the boundary and the
				// command's line in the record.
				say(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "running"})
				say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "started"})
				say(map[string]any{"type": "system", "subtype": "status", "status": "compacting"})
				compacting, compacted = f, time.After(compactFor)
				continue
			}
			if unprefixed(text) == "/clear" {
				say(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "running"})
				say(map[string]any{"type": "command_lifecycle", "command_uuid": f["uuid"], "state": "started"})
				say(map[string]any{"type": "conversation_reset", "new_conversation_id": f["uuid"], "trigger": "clear", "session_id": session})
				b := make([]byte, 16)
				rand.Read(b)
				b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
				session = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
				record.Close()
				recordPath = filepath.Join(dir, session+".jsonl")
				record, _ = os.OpenFile(recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				say(map[string]any{"type": "system", "subtype": "init", "session_id": session, "model": resolved()})
				note(map[string]any{"type": "user", "uuid": f["uuid"], "timestamp": stamp(), "message": map[string]any{"role": "user", "content": "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>"}})
				end(f, "success")
				continue
			}
			begin(f)
			switch {
			case strings.HasSuffix(text, "crash"), strings.HasSuffix(text, "crash!"), strings.HasSuffix(text, "crashloop"):
				record.Close()
				return 3
			case strings.HasSuffix(text, "QUESTION"):
				pending, asked = f, 1
				question("ask-q")
				continue
			case strings.HasSuffix(text, "ASK"):
				pending, asked = f, 1
				ask("ask-1", "ls")
				continue
			case strings.HasSuffix(text, "WAIT"):
				waiting = f
				timer = time.After(500 * time.Millisecond)
				continue
			case strings.HasSuffix(text, "DENIED"):
				// Auto mode's classifier turns the call down: the CLI answers the model itself.
				call := map[string]any{"type": "tool_use", "id": "toolu_denied", "name": "Bash", "input": map[string]any{"command": "npx -y some-tool", "description": "fetch a tool"}}
				say(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"role": "assistant", "content": []any{call}, "usage": map[string]any{"input_tokens": 10}}})
				note(map[string]any{"type": "assistant", "timestamp": stamp(), "message": map[string]any{"role": "assistant", "content": []any{call}}})
				result := map[string]any{"type": "tool_result", "tool_use_id": "toolu_denied", "is_error": true, "content": "Permission for this action was denied by the Claude Code auto mode classifier. Reason: [Code from External]. If you have other tasks that don't depend on this action, continue working on those."}
				say(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{result}}})
				note(map[string]any{"type": "user", "timestamp": stamp(), "message": map[string]any{"role": "user", "content": []any{result}}})
				finish(f, nil)
				continue
			case strings.HasSuffix(text, "AGAIN"):
				// Switched to manual for the denied call: the same call again, asked now.
				pending, asked = f, 1
				ask("again", "npx -y some-tool")
				continue
			case strings.HasSuffix(text, "QUOTA"):
				back := time.Now().Add(1500 * time.Millisecond).Unix() // whole seconds: back in 0.5 to 1.5
				say(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": back, "rateLimitType": "five_hour"}})
				assistant("You've hit your limit")
				end(f, "error_during_execution")
				continue
			}
			finish(f, nil)
		}
	}
}

// argOf is the value after flag name in args, "" if none.
func argOf(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
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
