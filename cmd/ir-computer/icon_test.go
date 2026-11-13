package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestMark(t *testing.T) {
	for _, letters := range []bool{false, true} {
		img := mark(32, letters, false)
		// The I's stem (x 20–26, y 30 in the view) is drawn, the corner is not (it is rounded).
		x, y := 11, 15
		if letters {
			x, y = 8, 14
		}
		if r, _, _, a := img.At(x, y).RGBA(); a == 0 || (!letters && r < 0xe000) {
			t.Fatalf("no I (letters %v): %v", letters, img.At(x, y))
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatalf("corner drawn (letters %v)", letters)
		}
	}
	b := pngOf(mark(36, true, false), 144)
	if !bytes.Contains(b, []byte("pHYs")) {
		t.Fatal("no pHYs")
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
}
