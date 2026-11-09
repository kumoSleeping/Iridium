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
	"net/http"
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
// input_image, image_url a data URL), dsh's (attachment, a handle into its store). Raw: its
// bytes are only copied, not unescaped, till they are kept.
type imageItem struct {
	Type   string `json:"type"`
	Source *struct {
		Type      string          `json:"type"`
		MediaType string          `json:"media_type"`
		Data      json.RawMessage `json:"data"`
	} `json:"source"`
	Data       json.RawMessage `json:"data"`
	MimeType   string          `json:"mimeType"`
	ImageURL   json.RawMessage `json:"image_url"`
	Attachment *struct {
		AttachmentID string `json:"attachmentId"`
		MediaType    string `json:"mediaType"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
	} `json:"attachment"`
}

// pictures is the pictures among content's blocks, in order. keep: with their bytes (Data), for
// a record read again whole; else only what a page shows of them. A content that is a string, or
// a picture by URL, gives none.
func pictures(content json.RawMessage, keep bool) []api.Image {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var items []imageItem
	if json.Unmarshal(content, &items) != nil {
		return nil
	}
	var out []api.Image
	for _, it := range items {
		var img api.Image
		var data json.RawMessage
		switch {
		case it.Type == "image" && it.Attachment != nil && it.Attachment.AttachmentID != "":
			a := it.Attachment
			img = api.Image{Mime: a.MediaType, W: a.Width, H: a.Height, Handle: a.AttachmentID}
		case it.Type == "image" && it.Source != nil && it.Source.Type == "base64":
			img.Mime, data = it.Source.MediaType, it.Source.Data
		case it.Type == "image" && len(it.Data) > 0:
			img.Mime, data = it.MimeType, it.Data
		case it.Type == "input_image":
			var url string
			if json.Unmarshal(it.ImageURL, &url) != nil {
				continue
			}
			mime, b64, ok := dataURL(url)
			if !ok {
				continue
			}
			img.Mime = mime
			if keep {
				img.Data = b64
			}
			sized(&img, b64)
			out = append(out, img)
			continue
		default:
			continue
		}
		if data != nil {
			var b64 string
			if json.Unmarshal(data, &b64) != nil || b64 == "" {
				continue
			}
			if keep {
				img.Data = b64
			}
			sized(&img, b64)
		}
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

// later is the picture an ImageView on line e showed the model, from the output after it
// (Codex's: codexShown).
func (f *File) later(e entry) (api.Image, error) {
	f.mu.Lock()
	r, done, err := f.text()
	size := f.size
	f.mu.Unlock()
	if err != nil {
		return api.Image{}, err
	}
	defer done()
	return codexShown(r, size, e.off, int64(e.n))
}
