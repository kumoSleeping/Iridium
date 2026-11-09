package transcript

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// pngOf is a w×h picture filled with c, as PNG.
func pngOf(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestPicturesShapes(t *testing.T) {
	pic := pngOf(t, 3, 2, color.White)
	b64 := base64.StdEncoding.EncodeToString(pic)
	content := raw([]any{
		map[string]any{"type": "text", "text": "Read image file [image/png]"},
		map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": b64}},
		map[string]any{"type": "image", "data": b64, "mimeType": "image/png"},
		map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + b64, "detail": "high"},
		map[string]any{"type": "image", "attachment": map[string]any{"attachmentId": "sha256:" + string(bytes.Repeat([]byte("a"), 64)), "mediaType": "image/webp", "width": 640, "height": 420}},
		map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/a.png"}},
		map[string]any{"type": "input_image", "image_url": "https://example.com/a.png"},
	})
	got := pictures(content, false)
	if len(got) != 4 {
		t.Fatalf("pictures: %+v", got)
	}
	for i, img := range got[:3] {
		if img.Mime != "image/png" || img.W != 3 || img.H != 2 || img.Data != "" || img.Handle != "" {
			t.Errorf("picture %d on a page: %+v", i, img)
		}
	}
	if a := got[3]; a.Mime != "image/webp" || a.W != 640 || a.H != 420 || a.Handle == "" {
		t.Errorf("attachment: %+v", a)
	}
	for i, img := range pictures(content, true)[:3] {
		if img.Data != b64 {
			t.Errorf("picture %d read whole has no bytes", i)
		}
	}
	if pictures(raw("only text"), true) != nil || pictures(nil, true) != nil {
		t.Error("a string content gave pictures")
	}
	// No type in the record: its header tells.
	if got := pictures(raw([]any{map[string]any{"type": "image", "data": b64}}), false); len(got) != 1 || got[0].Mime != "image/png" {
		t.Errorf("untyped: %+v", got)
	}
}

func TestClaudeResultImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	pic := pngOf(t, 4, 3, color.Black)
	b64 := base64.StdEncoding.EncodeToString(pic)
	lines{
		user("u1", "look"),
		assistant(map[string]any{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{"file_path": "/a.png"}}),
		user("r1", []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": b64}},
		}}}),
		assistant(text("a black picture")),
	}.write(t, path, false)
	f := Open(path, nil)
	p, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	var r *api.Result
	for _, b := range p.Blocks {
		if b.Kind == api.BlockKindTool {
			r = b.Result
		}
	}
	if r == nil || len(r.Images) != 1 || r.Images[0].W != 4 || r.Images[0].H != 3 || r.Images[0].Data != "" {
		t.Fatalf("the call's result: %+v", r)
	}
	img, data, err := f.Image(r.At, 0)
	if err != nil || !bytes.Equal(data, pic) || img.Mime != "image/png" || img.Data != "" {
		t.Fatalf("Image: %+v %d bytes %v", img, len(data), err)
	}
	if _, _, err := f.Image(r.At, 1); !errors.Is(err, ErrNoImage) {
		t.Errorf("a second picture: %v", err)
	}
	if _, _, err := f.Image(p.Blocks[0].At, 0); !errors.Is(err, ErrNoImage) {
		t.Errorf("not a result: %v", err)
	}
}

func TestPiUnsentImage(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(pngOf(t, 2, 2, color.White))
	line := raw(map[string]any{"type": "message", "timestamp": "2026-11-09T00:00:00Z", "message": map[string]any{
		"role": "toolResult", "toolCallId": "c1", "content": []any{
			map[string]any{"type": "text", "text": "Read image file [image/png]\n[Current model does not support images. The image will be omitted from this request.]"},
			map[string]any{"type": "image", "data": b64, "mimeType": "image/png"},
		}}})
	b := piBlocks(0, line, Clip, nil)
	if len(b) != 1 || len(b[0].Images) != 1 || !b[0].Images[0].Unsent {
		t.Fatalf("pi's result: %+v", b)
	}
	sent := raw(map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolCallId": "c2", "content": []any{
		map[string]any{"type": "image", "data": b64, "mimeType": "image/png"}}}})
	if b := piBlocks(0, sent, Clip, nil); len(b[0].Images) != 1 || b[0].Images[0].Unsent {
		t.Fatalf("pi's result, sent: %+v", b)
	}
}

func TestDshResultHandle(t *testing.T) {
	handle := "sha256:" + string(bytes.Repeat([]byte("b"), 64))
	path := filepath.Join(t.TempDir(), "s.jsonl")
	content := []any{
		map[string]any{"type": "text", "text": "/w/a.png"},
		map[string]any{"type": "image", "attachment": map[string]any{"attachmentId": handle, "mediaType": "image/png", "bytes": 10, "width": 8, "height": 6}},
	}
	lines{
		{"type": "assistant/message", "time": 1, "data": map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "tool-call", "id": "c1", "name": "read_image", "arguments": map[string]any{"file_path": "a.png"}}}}}},
		{"type": "tool/result", "time": 2, "data": map[string]any{"message": map[string]any{"toolCallId": "c1", "content": content}}},
	}.write(t, path, false)
	f := OpenAs(path, Dsh, nil)
	p, _ := f.Latest()
	if len(p.Blocks) != 1 || p.Blocks[0].Result == nil || len(p.Blocks[0].Result.Images) != 1 {
		t.Fatalf("blocks: %+v", p.Blocks)
	}
	img, data, err := f.Image(p.Blocks[0].Result.At, 0)
	if err != nil || data != nil || img.Handle != handle || img.W != 8 {
		t.Fatalf("Image: %+v %v %v", img, data, err)
	}
}

// Codex rollout lines, for codexViews.
func cxCall(id string) map[string]any {
	return map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "call_id": id, "name": "exec", "input": "…"}}
}

func cxOut(t *testing.T, id string, shown ...[]byte) map[string]any {
	return cxSaid(id, "Script completed\nWall time 0.1 seconds\nOutput:\n", shown...)
}

// cxSaid is call id's output, its text said first.
func cxSaid(id, said string, shown ...[]byte) map[string]any {
	out := []any{map[string]any{"type": "input_text", "text": said}}
	for _, s := range shown {
		out = append(out, map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(s), "detail": "high"})
	}
	return map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": id, "output": out}}
}

// cxWait takes up a script that went on after its call's output.
func cxWait(id, cell string) map[string]any {
	return map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "call_id": id, "name": "wait", "arguments": `{"cell_id":"` + cell + `","yield_time_ms":1000}`}}
}

func cxEvent(payload map[string]any) map[string]any {
	return map[string]any{"timestamp": "2026-11-09T00:00:00Z", "type": "event_msg", "payload": payload}
}

func cxView(name string) map[string]any {
	return cxEvent(map[string]any{"type": "item_completed", "item": map[string]any{"type": "ImageView", "id": "exec-" + name, "path": "file:///w/" + name}})
}

// codexRollout writes ls after an earlier script's output (a big line) and gives the record and
// its view_image steps.
func codexRollout(t *testing.T, ls ...map[string]any) (*File, []api.Block) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// The earlier output as Codex writes it, its payload's type first: a long line, told by its head.
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 6<<20))
	lines{cxEvent(map[string]any{"type": "task_started"}), cxCall("c0")}.write(t, path, false)
	w, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	w.WriteString(`{"timestamp":"t","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c0","output":[{"type":"input_text","text":"Script completed"},{"type":"input_image","image_url":"data:image/png;base64,` + big + `"}]}}` + "\n")
	w.Close()
	lines(ls).write(t, path, true)
	f := OpenAs(path, Codex, nil)
	p, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	var views []api.Block
	for _, b := range p.Blocks {
		if b.Name == "view_image" && b.Result != nil {
			views = append(views, b)
		}
	}
	return f, views
}

func TestCodexImageFromScriptOutput(t *testing.T) {
	a, b := pngOf(t, 5, 5, color.Black), pngOf(t, 6, 4, color.White)
	run := cxEvent(map[string]any{"type": "item_completed", "item": map[string]any{"type": "CommandExecution", "id": "x", "command": []string{"true"}, "status": "completed"}})

	// One viewed, one shown: the page has its size, the picture its bytes.
	f, v := codexRollout(t, cxCall("c1"), run, cxView("a.png"), cxOut(t, "c1", a))
	if len(v) != 1 || len(v[0].Result.Images) != 1 || v[0].Result.Images[0].W != 5 {
		t.Fatalf("on the page: %+v", v)
	}
	if img, data, err := f.Image(v[0].Result.At, 0); err != nil || !bytes.Equal(data, a) || img.Mime != "image/png" {
		t.Errorf("one viewed, one shown: %+v %d bytes %v", img, len(data), err)
	}

	// Not told: neither on the page, nor by Image.
	none := map[string][]map[string]any{
		// Two viewed: the ImageViews come in the order the views ended, the pictures in the
		// order the script showed them (the user's rollouts, 2026-11-09).
		"two viewed":        {cxCall("c1"), cxView("a.png"), cxView("b.png"), cxOut(t, "c1", b, a)},
		"viewed, not shown": {cxCall("c1"), cxView("a.png"), cxView("b.png"), cxOut(t, "c1", a)},
		"shown, not viewed": {cxCall("c1"), cxView("a.png"), cxOut(t, "c1", a, b)},
		// Another call ran at once: whose the view was is unclear (the review, 2026-11-09).
		"two calls open":    {cxCall("c1"), cxCall("c2"), cxView("a.png"), cxOut(t, "c2", b), cxOut(t, "c1", a)},
		"a call in between": {cxCall("c1"), cxView("a.png"), cxCall("c2"), cxOut(t, "c1", a)},
		// The turn ended without the script's output: it will never come.
		"turn over": {cxCall("c1"), cxView("a.png"), cxEvent(map[string]any{"type": "turn_aborted"})},
		// A long line not told by its head (its keys in another order): the turn is not told.
		"a long line unread": {cxCall("c1"), cxOut(t, "c1", bytes.Repeat([]byte{1}, 128<<10)), cxCall("c2"), cxView("a.png"), cxOut(t, "c2", a)},
		// A script went on after its output (yielded) while another call viewed a picture.
		"a script running": {cxCall("c1"), cxSaid("c1", "Script running with cell ID 1\nWall time 1.0 seconds\nOutput:\n"), cxCall("c2"), cxView("a.png"), cxOut(t, "c2", b)},
	}
	for name, ls := range none {
		f, v := codexRollout(t, ls...)
		if len(v) == 0 {
			t.Fatalf("%s: no view_image", name)
		}
		for _, s := range v {
			if len(s.Result.Images) != 0 {
				t.Errorf("%s: on the page: %+v", name, s.Result.Images)
			}
			if _, _, err := f.Image(s.Result.At, 0); !errors.Is(err, ErrNoImage) {
				t.Errorf("%s: Image: %v", name, err)
			}
		}
	}

	// A wait takes up the script that went on: the view is its, the wait's output shows it.
	yielded := cxSaid("c1", "Script running with cell ID 7\nWall time 1.0 seconds\nOutput:\n")
	told := map[string][]map[string]any{
		"taken up by wait": {cxCall("c1"), yielded, cxWait("w1", "7"), cxView("a.png"), cxOut(t, "w1", a)},
		"one ended before": {cxCall("c1"), yielded, cxWait("w1", "7"), cxOut(t, "w1"), cxCall("c2"), cxView("a.png"), cxOut(t, "c2", a)},
	}
	for name, ls := range told {
		f, v := codexRollout(t, ls...)
		if _, data, err := f.Image(v[0].Result.At, 0); err != nil || !bytes.Equal(data, a) || len(v[0].Result.Images) != 1 {
			t.Errorf("%s: %d bytes %v, on the page %+v", name, len(data), err, v[0].Result.Images)
		}
	}

	// The script still runs: on the page as it is, to be asked again.
	f, v = codexRollout(t, cxCall("c1"), cxView("a.png"))
	if len(v[0].Result.Images) != 1 || v[0].Result.Images[0].W != 0 {
		t.Errorf("not yet, on the page: %+v", v[0].Result.Images)
	}
	if _, _, err := f.Image(v[0].Result.At, 0); !errors.Is(err, ErrNotYet) {
		t.Errorf("not yet: %v", err)
	}
	// Once written, the next page tells it.
	lines{cxOut(t, "c1", a)}.write(t, f.Path(), true)
	p, _ := f.Latest()
	for _, s := range p.Blocks {
		if s.Name == "view_image" && (len(s.Result.Images) != 1 || s.Result.Images[0].W != 5) {
			t.Errorf("written since: %+v", s.Result.Images)
		}
	}
}

func TestPicturesOtherValues(t *testing.T) {
	// A block with another kind of value under a picture's names: the others still read, the
	// same for a page and read whole.
	b64 := base64.StdEncoding.EncodeToString(pngOf(t, 2, 2, color.White))
	content := json.RawMessage(`[{"type":"resource","data":{"k":1}},{"type":"input_image","image_url":{"url":"x"}},{"type":"image","data":"` + b64 + `","mimeType":"image/png"}]`)
	if page, whole := pictures(content, false), pictures(content, true); len(page) != 1 || len(whole) != 1 || whole[0].Data != b64 {
		t.Fatalf("page %+v, whole %+v", page, whole)
	}
}

func TestPicturesHeadEscaped(t *testing.T) {
	// A writer may escape base64's slashes: the head is read all the same.
	b64 := base64.StdEncoding.EncodeToString(pngOf(t, 7, 3, color.White))
	esc := strings.ReplaceAll(b64, "/", `\/`)
	content := json.RawMessage(`[{"type":"image","data":"` + esc + `","mimeType":"image/png"}]`)
	got := pictures(content, false)
	if len(got) != 1 || got[0].W != 7 || got[0].H != 3 || got[0].Data != "" {
		t.Fatalf("escaped: %+v", got)
	}
	if got := pictures(content, true); len(got) != 1 || got[0].Data != b64 {
		t.Fatalf("escaped, whole: %+v", got)
	}
	if pictures(json.RawMessage(`[{"type":"text","text":"no pictures"}]`), false) != nil {
		t.Error("text only")
	}
}

func TestCodexStepAsCodexWrites(t *testing.T) {
	// As Codex writes them: its keys in its order, a script's output an array or a string.
	for line, want := range map[string]codexStepLine{
		`{"timestamp":"t","ordinal":3,"type":"response_item","payload":{"type":"custom_tool_call_output","id":"x","call_id":"c1","output":[{"type":"input_text","text":"Script running with cell ID 13\nWall time 1.0 seconds\nOutput:\n"}]}}`: {kind: codexOutput, call: "c1", yielded: "13"},
		`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"w1","output":"Script running with cell ID 11\nWall time 1.0 seconds\nOutput:\n"}}`:                                                        {kind: codexOutput, call: "w1", yielded: "11"},
		`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"w1","output":"Script completed\nOutput:\n"}}`:                                                                                             {kind: codexOutput, call: "w1"},
		`{"timestamp":"t","type":"response_item","payload":{"type":"function_call","name":"wait","arguments":"{\"cell_id\":\"13\",\"yield_time_ms\":1000}","call_id":"w2"}}`:                                                                   {kind: codexCall, call: "w2", cell: "13"},
		`{"timestamp":"t","type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"…"}}`:                                                                                                                         {},
		`{"timestamp":"t","type":"event_msg","payload":{"type":"item_completed","item":{"type":"ImageView","id":"exec-1","path":"/a.png"}}}`:                                                                                                   {kind: codexView},
		`{"timestamp":"t","type":"event_msg","payload":{"type":"task_complete","turn_id":"x"}}`:                                                                                                                                                {kind: codexEnd},
	} {
		if got := codexStep([]byte(line), true); got != want {
			t.Errorf("%s:\n got %+v\nwant %+v", line[:80], got, want)
		}
		// Told by its head alone, as a long line is, the same.
		if got := codexStep([]byte(line[:min(len(line), 400)]), false); got != want && want.kind != codexView && want.kind != codexEnd {
			t.Errorf("by its head %s:\n got %+v\nwant %+v", line[:80], got, want)
		}
	}
	// Long lines told by their head: a compacted history is nothing; a line whose kind is not
	// told may be a call, so the turn is lost.
	for head, want := range map[string]int{
		`{"timestamp":"t","ordinal":9,"type":"compacted","payload":{"message":"","replacement_history":[`:                                codexOther,
		`{"timestamp":"t","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","aggregated_output":"`: codexOther,
		`{"timestamp":"t","type":"response_item","payload":{"id":"x","type":"custom_tool_call_output","call_id":"c","output":[`:          codexLost,
		`{"payload":{"call_id":"c","output":[{"text":"`:                                                                                  codexLost,
	} {
		if got := codexStep([]byte(head), false); got.kind != want {
			t.Errorf("long %s: %+v, want kind %d", head[:60], got, want)
		}
	}
}
