package httpx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// ETag answers a JSON read the browser already has with 304 Not Modified,
// as claudication's cached reads do: a page told by its event stream that
// something changed reads again, and most of what it reads has not changed.
// The answer is still computed (cafard's reads are a few SQLite queries),
// but an unchanged one costs no bytes on a phone's connection.
//
// Only a 200 to a GET whose Content-Type is application/json is held and
// tagged; everything else (files, event streams, pages, errors) passes
// through as written. A tagged answer is "private, no-cache" rather than
// the "no-store" the APIs write: the browser keeps it, for this person
// only, and asks every time whether it may use it.
func ETag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		ew := &etagWriter{w: w}
		next.ServeHTTP(ew, r)
		ew.finish(r)
	})
}

type etagWriter struct {
	w       http.ResponseWriter
	decided bool
	buf     *bytes.Buffer
}

func (e *etagWriter) Header() http.Header { return e.w.Header() }

func (e *etagWriter) WriteHeader(code int) {
	if e.decided {
		return
	}
	e.decided = true
	h := e.w.Header()
	if code == http.StatusOK && strings.HasPrefix(h.Get("Content-Type"), "application/json") && h.Get("ETag") == "" {
		e.buf = &bytes.Buffer{}
		return
	}
	e.w.WriteHeader(code)
}

func (e *etagWriter) Write(b []byte) (int, error) {
	if !e.decided {
		e.WriteHeader(http.StatusOK)
	}
	if e.buf != nil {
		return e.buf.Write(b)
	}
	return e.w.Write(b)
}

// Flush passes through for what is not held: an event stream must reach
// the browser as it is written.
func (e *etagWriter) Flush() {
	if e.buf != nil {
		return
	}
	if f, ok := e.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection underneath.
func (e *etagWriter) Unwrap() http.ResponseWriter { return e.w }

func (e *etagWriter) finish(r *http.Request) {
	if e.buf == nil {
		return
	}
	sum := sha256.Sum256(e.buf.Bytes())
	tag := `"` + hex.EncodeToString(sum[:12]) + `"`
	h := e.w.Header()
	h.Set("ETag", tag)
	h.Set("Cache-Control", "private, no-cache")
	for _, t := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if t = strings.TrimPrefix(strings.TrimSpace(t), "W/"); t == tag {
			h.Del("Content-Type")
			e.w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	e.w.WriteHeader(http.StatusOK)
	e.w.Write(e.buf.Bytes())
}
