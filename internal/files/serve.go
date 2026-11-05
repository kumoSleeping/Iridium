package files

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Files whose bytes are hashed for the sha256 header on first serving; larger ones get it once
// worked out (by .../info or an upload's check).
const sumOnServe = 64 << 20

// sandbox is every served file's Content-Security-Policy: nothing in it runs or loads, so an
// SVG or HTML file cannot act as the page.
const sandbox = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; media-src 'self'; sandbox"

// ServeFile serves the file at path (checked by the caller): Range and If-Range, ETag from its
// size and modification time, its sha256 in Repr-Digest and X-Sha256 where known, its sniffed
// type, and the sandbox CSP. Images, video, audio, PDF and plain text show inline; the rest,
// and any with ?download, download.
func (s *Service) ServeFile(w http.ResponseWriter, r *http.Request, path string) {
	s.serve(w, r, path, sniff(path), r.URL.Query().Has("download"))
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request, path, typ string, download bool) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("ETag", ETag(st))
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Content-Security-Policy", sandbox)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Accept-Ranges", "bytes")
	sum, ok := s.knownSum(path, st)
	if !ok && st.Size() <= sumOnServe && r.Method == http.MethodGet && r.Header.Get("Range") == "" {
		sum, err = s.Sum(path)
		ok = err == nil
	}
	if ok {
		h.Set("X-Sha256", sum)
		if b, err := hexToBase64(sum); err == nil {
			h.Set("Repr-Digest", "sha-256=:"+b+":")
		}
	}
	disp := "attachment"
	if !download && inline(typ) {
		disp = "inline"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": st.Name()}))
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// ETag is a file's validator: its size and modification time.
func ETag(st os.FileInfo) string {
	return fmt.Sprintf(`"%s-%s"`, strconv.FormatInt(st.Size(), 36), strconv.FormatInt(st.ModTime().UnixNano(), 36))
}

func inline(typ string) bool {
	b := baseType(typ)
	switch {
	case strings.HasPrefix(b, "image/"), strings.HasPrefix(b, "video/"), strings.HasPrefix(b, "audio/"):
		return true
	case b == "application/pdf", b == "text/plain", b == "text/markdown", b == "application/json", b == "text/csv":
		return true
	}
	return false
}
