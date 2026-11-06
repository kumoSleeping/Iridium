package transcript

import (
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// A zstd transcript (dsh's session.vN.jsonl.zstd) is a run of frames, one per batch of lines
// appended; the last may be half-written. Its offsets, as File gives them, are in the decoded
// text: frames indexes where each frame's text starts, so a line is read by decoding only its
// frame.

// frame is one frame: where it is in the file (coff, clen) and where its text goes (doff, dlen).
type frame struct{ coff, clen, doff, dlen int64 }

// frames is a zstd file's frames, read so far. Only appended to.
type frames struct {
	list []frame // those with text, in order; each starts where the one before ends
	end  int64   // in the file: just past the last whole frame
	size int64   // the text's length
}

var (
	zdecOnce sync.Once
	zdec     *zstd.Decoder
	zdecErr  error
)

func decoder() (*zstd.Decoder, error) {
	zdecOnce.Do(func() { zdec, zdecErr = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(1<<30)) })
	return zdec, zdecErr
}

// errFrame: a frame's bytes are not what they were.
var errFrame = errors.New("transcript: zstd frame changed")

// frameLen is the length of the frame at the start of b; false if b does not hold all of it yet
// (or it is not a frame).
func frameLen(b []byte) (int, bool) {
	if len(b) < 8 {
		return 0, false
	}
	magic := binary.LittleEndian.Uint32(b)
	if magic&^0xF == 0x184D2A50 { // skippable
		n := 8 + int(binary.LittleEndian.Uint32(b[4:]))
		return n, n <= len(b)
	}
	if magic != 0xFD2FB528 {
		return 0, false
	}
	d := b[4]
	single := d&0x20 != 0
	n := 5 + [4]int{0, 1, 2, 4}[d&3] + [4]int{0, 2, 4, 8}[d>>6]
	if !single {
		n++ // window descriptor
	}
	if single && d>>6 == 0 {
		n++ // a one-byte content size
	}
	for last := false; !last; {
		if n+3 > len(b) {
			return 0, false
		}
		h := uint32(b[n]) | uint32(b[n+1])<<8 | uint32(b[n+2])<<16
		n += 3
		last = h&1 != 0
		switch (h >> 1) & 3 {
		case 1: // RLE: one byte, repeated
			n++
		case 3: // reserved
			return 0, false
		default:
			n += int(h >> 3)
		}
	}
	if d&4 != 0 {
		n += 4 // checksum
	}
	return n, n <= len(b)
}

// from is the text from at on: what the frames already read hold past at, then the frames
// written since, each indexed as it is read. The file is size long now.
func (z *frames) from(r io.ReaderAt, size, at int64) (io.Reader, error) {
	buf := make([]byte, max(0, size-z.end))
	n, err := r.ReadAt(buf, z.end)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]
	old := io.NewSectionReader(&zReader{r: r, list: z.list}, at, max(0, z.size-at))
	return io.MultiReader(old, &zNew{z: z, buf: buf}), nil
}

// zNew reads the frames in buf, which starts at z.end, indexing each.
type zNew struct {
	z   *frames
	buf []byte
	cur []byte
}

func (n *zNew) Read(p []byte) (int, error) {
	for len(n.cur) == 0 {
		l, ok := frameLen(n.buf)
		if !ok {
			return 0, io.EOF // the rest is not written whole yet
		}
		dec, err := decoder()
		if err != nil {
			return 0, err
		}
		text, err := dec.DecodeAll(n.buf[:l], nil)
		if err != nil {
			return 0, io.EOF
		}
		z := n.z
		if len(text) > 0 {
			z.list = append(z.list, frame{coff: z.end, clen: int64(l), doff: z.size, dlen: int64(len(text))})
		}
		z.end += int64(l)
		z.size += int64(len(text))
		n.buf, n.cur = n.buf[l:], text
	}
	c := copy(p, n.cur)
	n.cur = n.cur[c:]
	return c, nil
}

// zReader reads a zstd file's text at offsets, by the frames in list; it keeps the frame it read
// last.
type zReader struct {
	r    io.ReaderAt
	list []frame
	k    int
	text []byte
}

func (z *zReader) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		k := sort.Search(len(z.list), func(i int) bool { return z.list[i].doff+z.list[i].dlen > off })
		if k == len(z.list) || off < 0 {
			return n, io.EOF
		}
		fr := z.list[k]
		if z.text == nil || z.k != k {
			raw := make([]byte, fr.clen)
			if _, err := z.r.ReadAt(raw, fr.coff); err != nil {
				return n, err
			}
			dec, err := decoder()
			if err != nil {
				return n, err
			}
			text, err := dec.DecodeAll(raw, nil)
			if err != nil {
				return n, err
			}
			if int64(len(text)) != fr.dlen {
				return n, errFrame
			}
			z.k, z.text = k, text
		}
		c := copy(p[n:], z.text[off-fr.doff:])
		n += c
		off += int64(c)
	}
	return n, nil
}
