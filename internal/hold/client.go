package hold

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Prepare readies dir for a new holder: the pipe, the spec, and none of the last one's exit or
// pid. out.log, in.log and err.log are kept and appended to, so byte offsets stay valid.
func Prepare(dir string, spec Spec) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := unix.Mkfifo(filepath.Join(dir, FileIn), 0o600); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, FileSpec), b); err != nil {
		return err
	}
	for _, f := range []string{FileExit, FilePID} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ReadExit is the exit file, or nil while there is none.
func ReadExit(dir string) (*Exit, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileExit))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e Exit
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("exit file: %w", err)
	}
	return &e, nil
}

// ReadPID is the agent's pid, 0 until the holder started it.
func ReadPID(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, FilePID))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// OutSize is out.log's length, 0 if there is none.
func OutSize(dir string) int64 {
	st, err := os.Stat(filepath.Join(dir, FileOutLog))
	if err != nil {
		return 0
	}
	return st.Size()
}

// ErrNoHolder: no holder has the input pipe open, so nothing written would reach an agent.
var ErrNoHolder = errors.New("hold: no holder reads the input pipe")

// Input writes lines into a holder's pipe. One per hold directory; safe for concurrent use.
type Input struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

func NewInput(dir string) *Input { return &Input{path: filepath.Join(dir, FileIn)} }

// Ready reports whether a holder reads the pipe.
func (in *Input) Ready() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.open() == nil
}

// WriteLine writes line and a newline. The line must not hold a newline itself. A write blocks
// while the pipe is full, as when the agent does not read its stdin.
func (in *Input) WriteLine(line []byte) error {
	if bytes.IndexByte(line, '\n') >= 0 {
		return errors.New("hold: a line with a newline in it")
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.open(); err != nil {
		return err
	}
	buf := make([]byte, 0, len(line)+1)
	buf = append(append(buf, line...), '\n')
	if _, err := in.f.Write(buf); err != nil {
		in.f.Close()
		in.f = nil
		if errors.Is(err, syscall.EPIPE) {
			return ErrNoHolder
		}
		return err
	}
	return nil
}

// open opens the pipe for writing without waiting for a reader: none (ENXIO) is ErrNoHolder.
func (in *Input) open() error {
	if in.f != nil {
		return nil
	}
	f, err := os.OpenFile(in.path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ENXIO) || errors.Is(err, os.ErrNotExist) {
		return ErrNoHolder
	}
	if err != nil {
		return err
	}
	in.f = f
	return nil
}

func (in *Input) Close() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.f == nil {
		return nil
	}
	err := in.f.Close()
	in.f = nil
	return err
}

// Line is one complete line of out.log. Data is nil for a line longer than the Tail's max, whose
// length is still given.
type Line struct {
	Off  int64
	Len  int64
	Data []byte
}

// Tail follows out.log from a byte offset, giving only complete lines, split on 0x0A alone and
// never decoded (U+2028 inside a line is just bytes). Not safe for concurrent use.
type Tail struct {
	path    string
	max     int64
	f       *os.File
	pos     int64  // next byte to read
	start   int64  // offset of the line being assembled
	partial []byte // its bytes so far, unless it outgrew max
	skip    bool   // the line being assembled outgrew max
	buf     []byte
}

// NewTail follows path from off, which must be the start of a line (0, or an Offset from before).
// max: lines longer are given without their bytes; 0 for no limit.
func NewTail(path string, off int64, max int64) *Tail {
	return &Tail{path: path, max: max, pos: off, start: off}
}

// Offset is where the next line starts: what to save to read on from after a restart.
func (t *Tail) Offset() int64 { return t.start }

// Read gives the lines completed since the last call; none while the file does not exist yet.
func (t *Tail) Read() ([]Line, error) {
	if t.f == nil {
		f, err := os.Open(t.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		t.f = f
	}
	if t.buf == nil {
		t.buf = make([]byte, 256<<10)
	}
	var lines []Line
	for {
		n, err := t.f.ReadAt(t.buf, t.pos)
		chunk := t.buf[:n]
		base := t.pos
		t.pos += int64(n)
		for len(chunk) > 0 {
			i := bytes.IndexByte(chunk, '\n')
			if i < 0 {
				t.add(chunk)
				break
			}
			t.add(chunk[:i])
			end := base + int64(len(t.buf[:n])-len(chunk)) + int64(i)
			l := Line{Off: t.start, Len: end - t.start}
			if !t.skip {
				l.Data = t.partial
			}
			lines = append(lines, l)
			t.partial, t.skip = nil, false
			t.start = end + 1
			chunk = chunk[i+1:]
		}
		if err == io.EOF || n == 0 {
			return lines, nil
		}
		if err != nil {
			return lines, err
		}
	}
}

func (t *Tail) add(b []byte) {
	if t.skip {
		return
	}
	if t.max > 0 && int64(len(t.partial)+len(b)) > t.max {
		t.partial, t.skip = nil, true
		return
	}
	t.partial = append(t.partial, b...)
}

func (t *Tail) Close() error {
	if t.f == nil {
		return nil
	}
	return t.f.Close()
}
