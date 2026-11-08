package remote

import (
	"github.com/kumoSleeping/iridium/internal/remote/wire"

	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Paths there are opaque strings in the executor's own form; Join and Base use its separator.

// Join joins elements with sep, as the executor's paths are.
func Join(sep string, elem ...string) string {
	out := ""
	for _, e := range elem {
		if e == "" {
			continue
		}
		if out == "" || strings.HasSuffix(out, sep) {
			out += e
		} else {
			out += sep + strings.TrimPrefix(e, sep)
		}
	}
	return out
}

// Base is path's last element by sep (and / on Windows, which takes both).
func Base(sep, path string) string {
	path = strings.TrimRight(path, sep)
	if sep == `\` {
		path = strings.TrimRight(path, "/")
		if i := strings.LastIndexAny(path, `\/`); i >= 0 {
			return path[i+1:]
		}
		return path
	}
	if i := strings.LastIndex(path, sep); i >= 0 {
		return path[i+1:]
	}
	return path
}

// IsAbs reports whether path is absolute there: /… on Mac and Linux; C:\… or \\server\… on
// Windows.
func IsAbs(sep, path string) bool {
	if sep != `\` {
		return strings.HasPrefix(path, "/")
	}
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') &&
		(path[0]|0x20 >= 'a' && path[0]|0x20 <= 'z')
}

// GroupPath is the folder a session at machine is grouped by: "<machine>:<cwd with />", a path
// no folder here holds; cwd itself for this machine ("").
func GroupPath(machine, cwd string) string {
	if machine == "" {
		return cwd
	}
	return machine + ":" + strings.ReplaceAll(cwd, `\`, "/")
}

func (m *Machine) Stat(ctx context.Context, path string) (wire.Stat, error) {
	var st wire.Stat
	err := m.Call(ctx, "fs.stat", wire.StatParams{Path: path}, &st)
	return st, err
}

func (m *Machine) List(ctx context.Context, path string) ([]wire.Entry, error) {
	var r wire.ListReply
	err := m.Call(ctx, "fs.list", wire.PathParams{Path: path}, &r)
	return r.Entries, err
}

// ReadAt reads up to length bytes from offset (at most wire.MaxRead a call).
func (m *Machine) ReadAt(ctx context.Context, path string, offset, length int64) ([]byte, error) {
	var r wire.ReadReply
	err := m.Call(ctx, "fs.read", wire.ReadParams{Path: path, Offset: offset, Length: min(length, wire.MaxRead)}, &r)
	return r.Data, err
}

// ReadFile reads a whole file there, of at most limit bytes.
func (m *Machine) ReadFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	st, err := m.Stat(ctx, path)
	if err != nil {
		return nil, err
	}
	if st.Size > limit {
		return nil, fmt.Errorf("executor: %s: %d bytes, more than %d", path, st.Size, limit)
	}
	var out []byte
	for int64(len(out)) < st.Size {
		b, err := m.ReadAt(ctx, path, int64(len(out)), st.Size-int64(len(out)))
		if err != nil {
			return nil, err
		}
		if len(b) == 0 {
			break
		}
		out = append(out, b...)
	}
	return out, nil
}

// WriteFile writes data whole at path there (in one frame: for small files), replacing it if
// replace.
func (m *Machine) WriteFile(ctx context.Context, path string, data []byte, replace bool) error {
	sum := sha256.Sum256(data)
	return m.Call(ctx, "fs.write", wire.WriteFileParams{Path: path, Data: data, SHA256: hex.EncodeToString(sum[:]), Replace: replace}, nil)
}

func (m *Machine) Mkdir(ctx context.Context, path string) error {
	return m.Call(ctx, "fs.mkdir", wire.PathParams{Path: path}, nil)
}

// DshOpen is the sessions open in the dsh the executor runs in (DSH Desktop's, through the dsh
// plugin); an error of wire.CodeUnsupported from one that cannot say.
func (m *Machine) DshOpen(ctx context.Context) (wire.DshOpenReply, error) {
	var r wire.DshOpenReply
	err := m.Call(ctx, "dsh.open", struct{}{}, &r)
	return r, err
}

// DshPrompt gives a message to the agent of a session the dsh the executor runs in holds (DSH
// Desktop's).
func (m *Machine) DshPrompt(ctx context.Context, session, text string, steer bool) error {
	return m.Call(ctx, "dsh.prompt", wire.DshPromptParams{Session: session, Text: text, Steer: steer}, nil)
}

// DshAttach files session under the workspace of its folder cwd in the dsh the executor runs in
// (DSH Desktop's sidebar), made if there is none.
func (m *Machine) DshAttach(ctx context.Context, session, cwd string) error {
	return m.Call(ctx, "dsh.attach", wire.DshAttachParams{Session: session, Cwd: cwd}, nil)
}

// headSize is how much of a mirrored file's start is checked for its being the same file.
const headSize = 4096

// Mirror brings local up to date with the file at path there, which only grows: what was added
// is appended; if it shrank or its start differs (rewritten), it is fetched whole again.
func (m *Machine) Mirror(ctx context.Context, path, local string) error {
	st, err := m.statHead(ctx, path)
	if err != nil {
		return err
	}
	have := size(local)
	same := have <= st.Size
	switch {
	case !same || have == 0:
	case have >= headSize:
		mine, err := localHead(local, headSize)
		if err != nil {
			return err
		}
		same = mine == st.Head
	default:
		theirs, err := m.ReadAt(ctx, path, 0, have)
		if err != nil {
			return err
		}
		mine, err := os.ReadFile(local)
		if err != nil {
			return err
		}
		same = bytes.Equal(theirs, mine[:min(int64(len(mine)), have)])
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	flag := os.O_WRONLY | os.O_APPEND | os.O_CREATE
	if !same {
		have, flag = 0, flag|os.O_TRUNC
	}
	if have >= st.Size && same {
		return nil
	}
	f, err := os.OpenFile(local, flag, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for have < st.Size {
		b, err := m.ReadAt(ctx, path, have, st.Size-have)
		if err != nil {
			return err
		}
		if len(b) == 0 {
			return nil // it shrank meanwhile: the next look sees
		}
		if _, err := f.Write(b); err != nil {
			return err
		}
		have += int64(len(b))
	}
	return nil
}

// statHead is fs.stat of a file with the sha256 of its first headSize bytes.
func (m *Machine) statHead(ctx context.Context, path string) (wire.Stat, error) {
	var st wire.Stat
	err := m.Call(ctx, "fs.stat", wire.StatParams{Path: path, Head: headSize}, &st)
	if err == nil && st.Type != "file" {
		err = &wire.Error{Code: wire.CodeBadRequest, Message: path + " is not a file"}
	}
	return st, err
}

func localHead(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyN(h, f, n); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
