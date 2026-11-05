package gate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

// Avatar limits: uploads up to MaxAvatarBytes of PNG, JPEG or WebP; stored as a
// AvatarSize×AvatarSize square (JPEG when opaque, PNG otherwise).
const (
	MaxAvatarBytes = 2 << 20
	AvatarSize     = 128
	maxAvatarSide  = 12000
	maxAvatarArea  = 40_000_000 // pixels; bounds the memory a decode can take
)

var (
	ErrAvatarTooLarge = errors.New("gate: avatar larger than 2 MB")
	ErrAvatarFormat   = errors.New("gate: avatar must be a PNG, JPEG or WebP image")
)

// makeAvatar turns an uploaded image into the stored avatar and its version.
func makeAvatar(data []byte) (avatar []byte, version string, err error) {
	if len(data) > MaxAvatarBytes {
		return nil, "", ErrAvatarTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") {
		return nil, "", ErrAvatarFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxAvatarSide || cfg.Height > maxAvatarSide ||
		cfg.Width*cfg.Height > maxAvatarArea {
		return nil, "", ErrAvatarFormat
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrAvatarFormat
	}

	// Centre square crop, scaled down (or up) to AvatarSize.
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, AvatarSize, AvatarSize))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, image.Rect(x0, y0, x0+side, y0+side), draw.Src, nil)
	if format == "jpeg" {
		// A centred square crop commutes with the EXIF rotations, so orient after scaling.
		dst = orient(dst, jpegOrientation(data))
	}

	var buf bytes.Buffer
	if dst.Opaque() {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst)
	}
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:6]), nil
}

// orient applies an EXIF orientation (1–8) to a square image.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	n := src.Bounds().Dx()
	m := n - 1
	dst := image.NewRGBA(src.Bounds())
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			sx, sy := x, y
			switch o {
			case 2:
				sx = m - x
			case 3:
				sx, sy = m-x, m-y
			case 4:
				sy = m - y
			case 5:
				sx, sy = y, x
			case 6: // rotate 90° clockwise
				sx, sy = y, m-x
			case 7:
				sx, sy = m-y, m-x
			case 8: // rotate 90° counter-clockwise
				sx, sy = m-y, x
			}
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation tag of a JPEG; 1 when there is none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	count := int(bo.Uint16(t[ifd:]))
	for k := 0; k < count; k++ {
		e := ifd + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 { // Orientation, type SHORT
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
