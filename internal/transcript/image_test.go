package transcript

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
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

// codexRollout writes a rollout where, after an earlier script's output (a big line), a script
// (call c1) views the pictures named in views and hands on shown, other lines after the first
// view; it gives the ImageView results' ats in order.
func codexRollout(t *testing.T, views []string, shown [][]byte, between ...map[string]any) (*File, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	ev := func(item map[string]any) map[string]any {
		return map[string]any{"timestamp": "2026-11-09T00:00:00Z", "type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": item}}
	}
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 6<<20))
	ls := lines{
		{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "call_id": "c0", "name": "exec", "input": "1"}},
		{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c0", "output": []any{
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + big}}}},
		{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "call_id": "c1", "name": "exec", "input": "image((await tools.view_image({path})).image_url)"}},
		ev(map[string]any{"type": "CommandExecution", "id": "x", "command": []string{"true"}, "status": "completed"}),
	}
	for i, v := range views {
		ls = append(ls, ev(map[string]any{"type": "ImageView", "id": "exec-" + v, "path": "file:///w/" + v}))
		if i == 0 {
			ls = append(ls, between...)
		}
	}
	var out []any
	for _, s := range shown {
		out = append(out, map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(s), "detail": "high"})
	}
	if shown != nil {
		ls = append(ls, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": out}})
	}
	ls.write(t, path, false)
	f := OpenAs(path, Codex, nil)
	p, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	var ats []string
	for _, b := range p.Blocks {
		if b.Name == "view_image" && b.Result != nil && len(b.Result.Images) == 1 {
			ats = append(ats, b.Result.At)
		}
	}
	if len(ats) != len(views) {
		t.Fatalf("view_image results: %+v", p.Blocks)
	}
	return f, ats
}

func TestCodexImageFromScriptOutput(t *testing.T) {
	a, b := pngOf(t, 5, 5, color.Black), pngOf(t, 6, 4, color.White)
	f, ats := codexRollout(t, []string{"a.png"}, [][]byte{a})
	if img, data, err := f.Image(ats[0], 0); err != nil || !bytes.Equal(data, a) || img.Mime != "image/png" {
		t.Errorf("one viewed, one shown: %+v %d bytes %v", img, len(data), err)
	}
	// Two viewed: the ImageViews come in the order the views ended, the pictures in the order
	// the script showed them (the user's rollouts, 2026-11-09). Neither is told, either way.
	for _, shown := range [][][]byte{{a, b}, {b, a}, {a}} {
		f, ats = codexRollout(t, []string{"a.png", "b.png"}, shown)
		for i, at := range ats {
			if _, _, err := f.Image(at, 0); !errors.Is(err, ErrNoImage) {
				t.Errorf("%d shown, view %d: %v", len(shown), i, err)
			}
		}
	}
	// Viewed one, showed two (one it did not view).
	f, ats = codexRollout(t, []string{"a.png"}, [][]byte{a, b})
	if _, _, err := f.Image(ats[0], 0); !errors.Is(err, ErrNoImage) {
		t.Errorf("shown more: %v", err)
	}
	// Another call began before the script's output.
	other := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "call_id": "c2", "name": "shell"}}
	f, ats = codexRollout(t, []string{"a.png"}, [][]byte{a}, other)
	if _, _, err := f.Image(ats[0], 0); !errors.Is(err, ErrNoImage) {
		t.Errorf("interleaved: %v", err)
	}
	// The script still runs: its output is not written yet.
	f, ats = codexRollout(t, []string{"a.png"}, nil)
	if _, _, err := f.Image(ats[0], 0); !errors.Is(err, ErrNotYet) {
		t.Errorf("no output yet: %v", err)
	}
}

func TestLineBefore(t *testing.T) {
	long := bytes.Repeat([]byte("x"), 300<<10)
	text := append(append(append([]byte("first\n"), long...), '\n'), "last\n"...)
	r := bytes.NewReader(text)
	start, line, err := lineBefore(r, int64(len(text))-5)
	if err != nil || start != 6 || !bytes.Equal(line, long) {
		t.Fatalf("long line: start %d, %d bytes, %v", start, len(line), err)
	}
	if start, line, err := lineBefore(r, 6); err != nil || start != 0 || string(line) != "first" {
		t.Fatalf("first line: %d %q %v", start, line, err)
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
