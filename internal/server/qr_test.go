package server

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"rsc.io/qr"
)

// The SVG draws exactly the code's dark modules, inside a quiet zone of 4, on white.
func TestQRSVG(t *testing.T) {
	t.Parallel()
	text := "https://ir.example.com/_ir/join#ir_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	url, err := qrSVG(text)
	if err != nil {
		t.Fatal(err)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	svg := string(b)
	c, _ := qr.Encode(text, qr.M)
	side := c.Size + 8
	if !strings.Contains(svg, fmt.Sprintf(`viewBox="0 0 %d %d"`, side, side)) || !strings.Contains(svg, `shape-rendering="crispEdges"`) || !strings.Contains(svg, `fill="#fff"`) {
		t.Fatalf("svg: %.200s", svg)
	}
	drawn := map[[2]int]bool{}
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`).FindAllStringSubmatch(svg, -1) {
		var x, y, n int
		fmt.Sscan(m[1], &x)
		fmt.Sscan(m[2], &y)
		fmt.Sscan(m[3], &n)
		for i := range n {
			drawn[[2]int{x + i - 4, y - 4}] = true
		}
	}
	for y := range c.Size {
		for x := range c.Size {
			if drawn[[2]int{x, y}] != c.Black(x, y) {
				t.Fatalf("module %d,%d: drawn %v, code %v", x, y, drawn[[2]int{x, y}], c.Black(x, y))
			}
		}
	}
	if len(url) > 8000 {
		t.Errorf("an invite's QR code is %d bytes", len(url))
	}
}
