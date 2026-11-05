package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers the GIF decoder
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder

	"github.com/kumoSleeping/iridium/internal/agent"
)

const (
	ThumbSide = 1024 // a thumbnail's longest side, by default
	// maxPixels bounds what a decode may take: about 400 MB of RGBA.
	maxPixels = 100_000_000
	// Small images already within the side are served as they are.
	thumbAsIs = 512 << 10
	// What Claude takes (the Messages API): at most 5 MB an image, base64, and 8000 px a side;
	// past 1568 px it scales the image down itself. Copies are made at that.
	claudeMaxBytes = 5 << 20 * 3 / 4
	claudeMaxSide  = 8000
	claudeSide     = 1568
)

// Thumb serves the thumbnail of the image at path (checked by the caller), its longest side at
// most side: made once, kept in the state directory by path, size, modification time and side.
// An SVG is served as it is, under the sandbox CSP.
func (s *Service) Thumb(w http.ResponseWriter, r *http.Request, path string, side int) {
	typ := sniff(path)
	if baseType(typ) == "image/svg+xml" {
		s.serve(w, r, path, typ, false)
		return
	}
	out, otyp, err := s.thumbFile(path, side)
	if err != nil {
		code := http.StatusInternalServerError
		if err == ErrNotImage {
			code = http.StatusUnsupportedMediaType
		}
		http.Error(w, err.Error(), code)
		return
	}
	s.serve(w, r, out, otyp, false)
}

func (s *Service) thumbFile(path string, side int) (string, string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || !(format == "png" || format == "jpeg" || format == "gif" || format == "webp") {
		return "", "", ErrNotImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return "", "", ErrNotImage
	}
	if max(cfg.Width, cfg.Height) <= side && st.Size() <= thumbAsIs {
		return path, "image/" + format, nil
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", path, st.Size(), st.ModTime().UnixNano(), side)))
	dir := filepath.Join(s.o.State, "thumbs")
	base := filepath.Join(dir, hex.EncodeToString(sum[:16]))
	for _, ext := range []string{".jpg", ".png"} {
		if _, err := os.Stat(base + ext); err == nil {
			return base + ext, map[string]string{".jpg": "image/jpeg", ".png": "image/png"}[ext], nil
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", "", err
	}
	img, err := scaled(data, format, side)
	if err != nil {
		return "", "", err
	}
	var buf bytes.Buffer
	ext, typ := ".png", "image/png"
	if img.Opaque() {
		ext, typ = ".jpg", "image/jpeg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, ".thumb-*")
	if err != nil {
		return "", "", err
	}
	_, err = tmp.Write(buf.Bytes())
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), base+ext)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	return base + ext, typ, nil
}

// scaled decodes data (the first frame of a GIF), turns it upright by its EXIF orientation, and
// scales it to fit side (never up).
func scaled(data []byte, format string, side int) (*image.RGBA, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrNotImage
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if l := max(w, h); l > side {
		w, h = max(1, w*side/l), max(1, h*side/l)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	if format == "jpeg" {
		dst = orient(dst, jpegOrientation(data))
	}
	return dst, nil
}

// ForClaude is the image at path as Claude takes it: its own bytes where within Claude's limits,
// else a copy scaled down. ok false: not a PNG, JPEG, GIF or WebP image, or past decoding.
func ForClaude(path string) (agent.Image, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.Image{}, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !(format == "png" || format == "jpeg" || format == "gif" || format == "webp") {
		return agent.Image{}, false
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return agent.Image{}, false
	}
	if len(data) <= claudeMaxBytes && max(cfg.Width, cfg.Height) <= claudeMaxSide {
		return agent.Image{MediaType: "image/" + format, Data: data}, true
	}
	img, err := scaled(data, format, claudeSide)
	if err != nil {
		return agent.Image{}, false
	}
	var buf bytes.Buffer
	if !img.Opaque() && png.Encode(&buf, img) == nil && buf.Len() <= claudeMaxBytes {
		return agent.Image{MediaType: "image/png", Data: buf.Bytes()}, true
	}
	// JPEG has no alpha: on white.
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, image.Point{}, draw.Over)
	buf.Reset()
	if jpeg.Encode(&buf, flat, &jpeg.Options{Quality: 85}) != nil || buf.Len() > claudeMaxBytes {
		return agent.Image{}, false
	}
	return agent.Image{MediaType: "image/jpeg", Data: buf.Bytes()}, true
}

// orient applies an EXIF orientation (1–8).
func orient(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6: // rotate 90° clockwise
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8: // rotate 90° counter-clockwise
				sx, sy = w-1-y, x
			}
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

// jpegOrientation is a JPEG's EXIF orientation tag; 1 when there is none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF || data[i+1] == 0xDA || data[i+1] == 0xD9 {
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		if seg := data[i+4 : i+2+size]; data[i+1] == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			t := seg[6:]
			var bo binary.ByteOrder = binary.BigEndian
			if string(t[:2]) == "II" {
				bo = binary.LittleEndian
			}
			ifd := int(bo.Uint32(t[4:]))
			if ifd < 8 || ifd+2 > len(t) {
				return 1
			}
			for k, n := 0, int(bo.Uint16(t[ifd:])); k < n; k++ {
				e := ifd + 2 + 12*k
				if e+12 > len(t) {
					return 1
				}
				if bo.Uint16(t[e:]) == 0x0112 {
					if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
						return v
					}
					return 1
				}
			}
			return 1
		}
		i += 2 + size
	}
	return 1
}

func hexToBase64(h string) (string, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
