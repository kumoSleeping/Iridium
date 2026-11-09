package transcript

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp" // registers the WebP decoder

	"github.com/kumoSleeping/iridium/internal/api"
)

// The pictures a tool's result gave the model (api.Image): each format finds them in its
// results' content with pictures, which knows every shape agents write one in; a page carries
// only what it shows of them, and Image reads one again, bytes and all.

// headLen is the base64 a picture's size is read from: its header, where formats keep it.
const headLen = 64 << 10

// imageItem is a content block that may be a picture, in any of its shapes: Anthropic's
// (Claude: source.data, source.media_type), MCP's and pi's (data, mimeType), OpenAI's (Codex:
// input_image, image_url a data URL), dsh's (attachment, a handle into its store). S is how its
// base64 is kept: whole, or only its head, for a page.
type imageItem[S ~string] struct {
	Type   string `json:"type"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      S      `json:"data"`
	} `json:"source"`
	Data       S      `json:"data"`
	MimeType   string `json:"mimeType"`
	ImageURL   S      `json:"image_url"`
	Attachment *struct {
		AttachmentID string `json:"attachmentId"`
		MediaType    string `json:"mediaType"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
	} `json:"attachment"`
}

// head is a JSON string's first headLen characters: a picture's base64 as a page needs it, its
// header, without the whole copied. base64 has nothing to escape but a slash a writer may (\/):
// such a string is read whole.
type head string

func (h *head) UnmarshalJSON(b []byte) error {
	if len(b) < 2 || b[0] != '"' {
		*h = ""
		return nil
	}
	body := b[1 : len(b)-1]
	if bytes.IndexByte(body[:min(len(body), headLen)], '\\') < 0 {
		*h = head(body[:min(len(body), headLen)])
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) != nil {
		s = ""
	}
	*h = head(s[:min(len(s), headLen)])
	return nil
}

// whole is a JSON string as it is, unescaped; anything else in its place, "". Where a block has
// another kind of value under the same name (a resource's data), the content's other blocks
// still read, as with head.
type whole string

func (w *whole) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil {
		s = ""
	}
	*w = whole(s)
	return nil
}

// pictures is the pictures among content's blocks, in order. keep: with their bytes (Data), for
// a record read again whole; else only what a page shows of them. A content that is a string, or
// a picture by URL, gives none.
func pictures(content json.RawMessage, keep bool) []api.Image {
	if len(content) == 0 || content[0] != '[' || !bytes.Contains(content, []byte(`image"`)) {
		return nil
	}
	if keep {
		return picturesAs[whole](content, true)
	}
	return picturesAs[head](content, false)
}

func picturesAs[S ~string](content json.RawMessage, keep bool) []api.Image {
	var items []imageItem[S]
	if json.Unmarshal(content, &items) != nil {
		return nil
	}
	var out []api.Image
	for _, it := range items {
		var img api.Image
		var b64 string
		switch {
		case it.Type == "image" && it.Attachment != nil && it.Attachment.AttachmentID != "":
			a := it.Attachment
			out = append(out, api.Image{Mime: a.MediaType, W: a.Width, H: a.Height, Handle: a.AttachmentID})
			continue
		case it.Type == "image" && it.Source != nil && it.Source.Type == "base64":
			img.Mime, b64 = it.Source.MediaType, string(it.Source.Data)
		case it.Type == "image":
			img.Mime, b64 = it.MimeType, string(it.Data)
		case it.Type == "input_image":
			mime, data, ok := dataURL(string(it.ImageURL))
			if !ok {
				continue
			}
			img.Mime, b64 = mime, data
		}
		if b64 == "" {
			continue
		}
		if keep {
			img.Data = b64
		}
		sized(&img, b64)
		out = append(out, img)
	}
	return out
}

// dataURL is a base64 data URL's type and data.
func dataURL(u string) (mime, b64 string, ok bool) {
	rest, ok := strings.CutPrefix(u, "data:")
	if !ok {
		return "", "", false
	}
	head, b64, ok := strings.Cut(rest, ",")
	mime, enc, _ := strings.Cut(head, ";")
	return mime, b64, ok && enc == "base64" && b64 != ""
}

// sized fills in img's size, and its type where the record gives none, from its header.
func sized(img *api.Image, b64 string) {
	if img.W > 0 && img.Mime != "" {
		return
	}
	head := b64[:min(len(b64), headLen)]
	head = head[:len(head)/4*4]
	buf, err := base64.StdEncoding.DecodeString(head)
	if err != nil {
		return
	}
	if img.Mime == "" {
		img.Mime = http.DetectContentType(buf)
	}
	if c, _, err := image.DecodeConfig(bytes.NewReader(buf)); err == nil && img.W == 0 {
		img.W, img.H = c.Width, c.Height
	}
}

// ErrNoImage: the result has no such picture, or its bytes are not where the record says.
var ErrNoImage = errors.New("transcript: no such picture")

// ErrNotYet: the record names where the picture will be, and that is not written yet (a script
// still running).
var ErrNotYet = errors.New("transcript: the picture is not in the record yet")

// Image is picture n of the result at at, read again whole: with its bytes where the record has
// them (on that line, or on a later one), else with only img.Handle, for the agent's channel.
func (f *File) Image(at string, n int) (img api.Image, data []byte, err error) {
	blocks, idx, e, err := f.whole(at)
	if err != nil {
		return api.Image{}, nil, err
	}
	images := blocks[idx].Images
	if blocks[idx].Kind != api.BlockKindResult || n < 0 || n >= len(images) {
		return api.Image{}, nil, ErrNoImage
	}
	img = images[n]
	if img.Later {
		if img, err = f.later(e); err != nil {
			return api.Image{}, nil, err
		}
	}
	if img.Handle != "" {
		return img, nil, nil
	}
	if data, err = base64.StdEncoding.DecodeString(img.Data); err != nil || len(data) == 0 {
		return api.Image{}, nil, ErrNoImage
	}
	img.Data = ""
	return img, data, nil
}

// settle tells, for a page, the pictures blocks have on a later line of the record (Later:
// Codex's view_image, codexViews): with their size where the picture is told, gone where it never
// will be, as they are while it is not written yet. What was found is kept, by the result's at.
// f.mu is held; r reads the record.
func (f *File) settle(r io.ReaderAt, blocks []api.Block) {
	for i := range blocks {
		b := &blocks[i]
		at, images := b.At, &b.Images
		if b.Kind == api.BlockKindTool && b.Result != nil {
			at, images = b.Result.At, &b.Result.Images
		}
		if len(*images) != 1 || !(*images)[0].Later {
			continue
		}
		told, ok := f.told[at]
		if !ok {
			e, found := f.entry(at)
			if !found {
				continue
			}
			img, err := f.codexShot(r, e, false)
			switch {
			case err == nil:
				told = &api.Image{Mime: img.Mime, W: img.W, H: img.H, Later: true}
			case errors.Is(err, ErrNoImage):
			default:
				continue // not written yet, or not read now: asked again next time
			}
			if f.told == nil {
				f.told = map[string]*api.Image{}
			}
			f.told[at] = told
		}
		if told == nil {
			*images = nil
		} else {
			*images = []api.Image{*told}
		}
	}
}

// entry is the line of the block at at ("offset.index"); f.mu is held.
func (f *File) entry(at string) (entry, bool) {
	o, _, _ := strings.Cut(at, ".")
	off, err := strconv.ParseInt(o, 10, 64)
	if err != nil {
		return entry{}, false
	}
	k := sort.Search(len(f.lines), func(j int) bool { return f.lines[j].off >= off })
	if k == len(f.lines) || f.lines[k].off != off {
		return entry{}, false
	}
	return f.lines[k], true
}

// later is the picture an ImageView on line e showed the model, from the output after it
// (Codex's: codexViews).
func (f *File) later(e entry) (api.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, done, err := f.text()
	if err != nil {
		return api.Image{}, err
	}
	defer done()
	return f.codexShot(r, e, true)
}
