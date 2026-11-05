package webui

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

var gzips = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression); return w }}

// Gzip compresses next's JSON answers for a browser that takes gzip. Anything else, an event
// stream above all, goes as it is, flushed as next flushes it.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || !accepts(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		z := &zipWriter{ResponseWriter: w}
		defer z.close()
		next.ServeHTTP(z, r)
	})
}

type zipWriter struct {
	http.ResponseWriter
	decided bool
	gz      *gzip.Writer
}

func (z *zipWriter) decide(status int) {
	if z.decided {
		return
	}
	z.decided = true
	h := z.Header()
	ct := h.Get("Content-Type")
	if status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified && h.Get("Content-Encoding") == "" && strings.HasPrefix(ct, "application/json") {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		z.gz = gzips.Get().(*gzip.Writer)
		z.gz.Reset(z.ResponseWriter)
	}
}

func (z *zipWriter) WriteHeader(status int) {
	z.decide(status)
	z.ResponseWriter.WriteHeader(status)
}

func (z *zipWriter) Write(b []byte) (int, error) {
	if !z.decided {
		z.decide(http.StatusOK)
	}
	if z.gz != nil {
		return z.gz.Write(b)
	}
	return z.ResponseWriter.Write(b)
}

func (z *zipWriter) Flush() {
	if z.gz != nil {
		_ = z.gz.Flush()
	}
	if f, ok := z.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.NewResponseController reach the connection (an event stream's write deadline).
func (z *zipWriter) Unwrap() http.ResponseWriter { return z.ResponseWriter }

func (z *zipWriter) close() {
	if z.gz != nil {
		_ = z.gz.Close()
		gzips.Put(z.gz)
		z.gz = nil
	}
}
