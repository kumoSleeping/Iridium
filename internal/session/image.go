package session

import (
	"context"
	"net/http"

	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// ErrNoImage: the record has no such picture, or not one that may be shown.
var ErrNoImage = transcript.ErrNoImage

// shown are the picture types served: each shows as a picture and nothing else.
var shown = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// Image is picture n of the result at at in session id's record (api.Image) and its type: as the
// record keeps it, or read by its agent's channel where the record names it by a handle. Only
// pictures are given, by their bytes, whatever the record says they are.
func (m *Manager) Image(ctx context.Context, id, at string, n int) (string, []byte, error) {
	f, err := m.readable(id)
	if err != nil {
		return "", nil, err
	}
	if f == nil {
		return "", nil, ErrNotFound
	}
	img, data, err := f.Image(at, n)
	if err != nil {
		return "", nil, err
	}
	if img.Handle != "" {
		r, ok := m.ref(id)
		im, has := channelOf[harness.Imager](m, r.Agent)
		if !ok || !has {
			return "", nil, ErrNoImage
		}
		var done bool
		if data, done, err = im.Image(ctx, r, img.Handle); err != nil {
			return "", nil, err
		}
		if !done {
			return "", nil, ErrNoImage
		}
	}
	mime := http.DetectContentType(data)
	if !shown[mime] {
		return "", nil, ErrNoImage
	}
	return mime, data, nil
}
