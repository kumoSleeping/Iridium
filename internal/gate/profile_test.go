package gate

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"
)

// halves is a w×h image, left half red and right half blue, with alpha a.
func halves(w, h int, a uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{0, 0, 255, a}
			if x < w/2 {
				c = color.NRGBA{255, 0, 0, a}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 95})
	return b.Bytes()
}

// withOrientation inserts an EXIF APP1 segment with the given orientation after SOI.
func withOrientation(j []byte, o uint16) []byte {
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08") // big endian, IFD0 at 8
	tiff = binary.BigEndian.AppendUint16(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.BigEndian.AppendUint16(tiff, 3) // SHORT
	tiff = binary.BigEndian.AppendUint32(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, o)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0) // padding, next IFD = 0
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(seg)+2))
	app1 = append(app1, seg...)
	return append(append(append([]byte{}, j[:2]...), app1...), j[2:]...)
}

func decodeAvatar(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != AvatarSize || b.Dy() != AvatarSize {
		t.Fatalf("avatar is %v", b)
	}
	return img
}

func reddish(c color.Color) bool { r, _, b, _ := c.RGBA(); return r > 0xc000 && b < 0x4000 }
func bluish(c color.Color) bool  { r, _, b, _ := c.RGBA(); return b > 0xc000 && r < 0x4000 }

func TestMakeAvatar(t *testing.T) {
	// Opaque, wide: centre-cropped, scaled, stored as JPEG; red left, blue right.
	data, ver, err := makeAvatar(encodePNG(halves(800, 400, 255)))
	if err != nil {
		t.Fatal(err)
	}
	if http.DetectContentType(data) != "image/jpeg" || len(ver) != 12 {
		t.Fatalf("type %s version %q", http.DetectContentType(data), ver)
	}
	img := decodeAvatar(t, data)
	if !reddish(img.At(10, 64)) || !bluish(img.At(118, 64)) {
		t.Fatal("crop lost the halves")
	}
	// Transparent: stays PNG with alpha.
	data, _, err = makeAvatar(encodePNG(halves(64, 300, 128)))
	if err != nil || http.DetectContentType(data) != "image/png" {
		t.Fatalf("transparent: %v %s", err, http.DetectContentType(data))
	}
	if _, _, _, a := decodeAvatar(t, data).At(64, 64).RGBA(); a > 0x9000 || a < 0x7000 {
		t.Fatalf("alpha %x", a)
	}
	// A phone JPEG that says "rotate 90° clockwise": red ends up on top.
	data, _, err = makeAvatar(withOrientation(encodeJPEG(halves(300, 300, 255)), 6))
	if err != nil {
		t.Fatal(err)
	}
	img = decodeAvatar(t, data)
	if !reddish(img.At(64, 10)) || !bluish(img.At(64, 118)) {
		t.Fatal("EXIF orientation 6 not applied")
	}
	if jpegOrientation(encodeJPEG(halves(8, 8, 255))) != 1 {
		t.Fatal("orientation without EXIF")
	}
	// Refused: GIF, junk, too big, too many pixels.
	var g bytes.Buffer
	gif.Encode(&g, halves(10, 10, 255), nil)
	for name, in := range map[string][]byte{
		"gif":  g.Bytes(),
		"junk": []byte("not an image"),
		"huge": pngHeader(100000, 100000),
	} {
		if _, _, err := makeAvatar(in); err != ErrAvatarFormat {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := makeAvatar(make([]byte, MaxAvatarBytes+1)); err != ErrAvatarTooLarge {
		t.Errorf("too large: %v", err)
	}
}

// pngHeader is the start of a PNG that claims the given size (a decompression bomb).
func pngHeader(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := b.Bytes()
	binary.BigEndian.PutUint32(data[16:], uint32(w))
	binary.BigEndian.PutUint32(data[20:], uint32(h))
	return data
}
