package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	"golang.org/x/image/vector"

	"github.com/kumoSleeping/iridium/internal/join"
)

// The menu (Windows, macOS): the Iridium and its state, then these.
const (
	labelOpen    = "打开"
	labelAtLogin = "登录时运行"
	labelQuit    = "退出"
)

// line is the menu's first line and the icon's tip: ir.example.com · 已连接.
func line(u string, st join.State) string {
	host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if st.Text == "" {
		return host
	}
	return host + " · " + st.Text
}

// pen draws a path in a view's coordinates.
type pen struct {
	z      *vector.Rasterizer
	x0, y0 float32 // the view's corner
	k      float32 // pixels a unit
}

func (p pen) at(x, y float32) (float32, float32) { return (x - p.x0) * p.k, (y - p.y0) * p.k }
func (p pen) move(x, y float32)                  { p.z.MoveTo(p.at(x, y)) }
func (p pen) line(x, y float32)                  { p.z.LineTo(p.at(x, y)) }
func (p pen) cube(ax, ay, bx, by, x, y float32) {
	a1, a2 := p.at(ax, ay)
	b1, b2 := p.at(bx, by)
	c1, c2 := p.at(x, y)
	p.z.CubeTo(a1, a2, b1, b2, c1, c2)
}

// letters is Ir.
func (p pen) letters() {
	p.move(20, 18)
	p.line(26, 18)
	p.line(26, 46)
	p.line(20, 46)
	p.z.ClosePath()
	p.move(32, 28)
	p.line(37.6, 28)
	p.line(37.6, 31.2)
	p.cube(39, 28.8, 41.2, 27.6, 44, 27.6)
	p.line(44, 33.4)
	p.cube(40.2, 33.4, 37.8, 35, 37.8, 39.4)
	p.line(37.8, 46)
	p.line(32, 46)
	p.z.ClosePath()
}

// square is the rounded square under them.
func (p pen) square() {
	const r, c = 14, 14 * 0.5523 // the corners, as arcs
	p.move(r, 0)
	p.line(64-r, 0)
	p.cube(64-r+c, 0, 64, r-c, 64, r)
	p.line(64, 64-r)
	p.cube(64, 64-r+c, 64-r+c, 64, 64-r, 64)
	p.line(r, 64)
	p.cube(r-c, 64, 0, 64-r+c, 0, 64-r)
	p.line(0, r)
	p.cube(0, r-c, r-c, 0, r, 0)
	p.z.ClosePath()
}

// mark draws Iridium's mark (web/public/favicon.svg, a 64×64 view) into a size×size image: the
// dark square with the letters (Windows), or the letters alone in black, filling it (a macOS
// template image). dim: faded, as while it is not connected.
func mark(size int, lettersOnly, dim bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	alpha := uint8(255)
	if dim {
		alpha = 110
	}
	fill := func(c color.Color, view [3]float32, path func(pen)) {
		layer := image.NewRGBA(img.Bounds())
		z := vector.NewRasterizer(size, size)
		path(pen{z, view[0], view[1], float32(size) / view[2]})
		z.Draw(layer, layer.Bounds(), image.NewUniform(c), image.Point{})
		draw.Draw(img, img.Bounds(), layer, image.Point{}, draw.Over)
	}
	if lettersOnly {
		// The letters' box (x 20–44, y 17.6–46) with a margin.
		fill(color.NRGBA{0, 0, 0, alpha}, [3]float32{14, 14, 36}, pen.letters)
		return img
	}
	all := [3]float32{0, 0, 64}
	fill(color.NRGBA{0x2b, 0x2b, 0x2b, alpha}, all, pen.square)
	fill(color.NRGBA{0xf4, 0xf4, 0xf4, alpha}, all, pen.letters)
	return img
}

// pngOf is img as a PNG; dpi > 0 writes its resolution (macOS sizes an image by it: 144 is @2x).
func pngOf(img image.Image, dpi int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	out := b.Bytes()
	if dpi <= 0 {
		return out
	}
	// A pHYs chunk after IHDR (8 bytes of signature, then IHDR's 25).
	ppm := uint32(float64(dpi)/0.0254 + 0.5)
	chunk := make([]byte, 4+4+9+4)
	binary.BigEndian.PutUint32(chunk, 9)
	copy(chunk[4:], "pHYs")
	binary.BigEndian.PutUint32(chunk[8:], ppm)
	binary.BigEndian.PutUint32(chunk[12:], ppm)
	chunk[16] = 1 // per metre
	binary.BigEndian.PutUint32(chunk[17:], crc32.ChecksumIEEE(chunk[4:17]))
	return append(append(append([]byte{}, out[:33]...), chunk...), out[33:]...)
}
