package session

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kumoSleeping/iridium/internal/transcript"
)

// record writes a piece of the conversation to the record Iridium keeps itself of an agent that
// keeps none it can read (agents.Info.OwnRecord: agy), with where its output ended in out.log and
// when. What a daemon before this one recorded (one taken up after a crash, before it saved how far
// it had read) is not written again.
func (h *Hosted) record(e transcript.Entry) {
	h.recMu.Lock()
	defer h.recMu.Unlock()
	path := filepath.Join(h.dir, transcript.OwnFile)
	if h.recorded == nil {
		last := lastRecorded(path)
		h.recorded = &last
	}
	e.Off = h.fed.Load()
	if e.Off <= *h.recorded {
		return
	}
	e.Time = time.Now().UnixMilli()
	b, err := json.Marshal(e)
	if err != nil {
		h.logf("record: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		h.logf("record: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		h.logf("record: %v", err)
	}
}

// lastRecorded is where the output of the record's last whole line ended; -1 for none.
func lastRecorded(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return -1
	}
	n := min(st.Size(), 1<<20)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, st.Size()-n); err != nil && err != io.EOF {
		return -1
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var e transcript.Entry
		if json.Unmarshal(lines[i], &e) == nil {
			return e.Off
		}
	}
	return -1
}
