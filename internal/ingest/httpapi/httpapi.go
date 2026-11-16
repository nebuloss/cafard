// Package httpapi is POST /api/log: plain text or NDJSON log lines over
// HTTP, for curl, shell scripts and `cafard push`.
package httpapi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/ingest/ingestutil"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// MaxBody bounds a request body after decompression.
const MaxBody = 16 << 20

// commitChunk is how many entries go to the writer per transaction.
const commitChunk = 1000

// Register mounts POST /api/log. It has no background work.
func Register(mux *http.ServeMux, _ *mcpserver.Server, d deps.Deps) (func(context.Context) error, error) {
	h := &handler{d: d}
	mux.Handle("POST /api/log", auth.Require(auth.BearerOnly{Next: d.Auth}, auth.ScopeIngest, h))
	return nil, nil
}

type handler struct{ d deps.Deps }

// streamID turns a name into a stream id: "push:<name>" unless the name
// already has a kind prefix. Message streams are refused.
func streamID(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a stream name is required (?stream=<name>)")
	}
	id := name
	if !strings.Contains(name, ":") {
		id = "push:" + name
	}
	if store.IsMessageStream(id) {
		return "", fmt.Errorf("stream %q holds messages; post them to /api/messages", id)
	}
	if err := store.ValidateStreamID(id); err != nil {
		return "", err
	}
	return id, nil
}

type defaults struct {
	stream    string
	host, app string
	severity  int
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	def := defaults{host: q.Get("host"), app: q.Get("app"), severity: store.DefaultSeverity}
	if s := q.Get("stream"); s != "" {
		id, err := streamID(s)
		if err != nil {
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "%v.", err))
			return
		}
		def.stream = id
	}
	if s := q.Get("severity"); s != "" {
		sev, err := store.ParseSeverity(s)
		if err != nil {
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "%v.", err))
			return
		}
		def.severity = sev
	}

	body := io.Reader(http.MaxBytesReader(w, r.Body, MaxBody))
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(body)
		if err != nil {
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "The body is not valid gzip."))
			return
		}
		defer zr.Close()
		body = io.LimitReader(zr, MaxBody+1)
	}

	ctype, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	now := h.d.Clock.Now()
	var (
		entries []store.Entry
		err     error
	)
	switch ctype {
	case "application/x-ndjson", "application/jsonl", "application/jsonlines":
		entries, err = parseNDJSON(body, def, now)
	case "", "text/plain":
		if def.stream == "" {
			err = dto.Errorf(dto.CodeBadRequest, "A plain text body needs ?stream=<name>.")
			break
		}
		entries, err = parseText(body, def, now)
	default:
		err = dto.Errorf(dto.CodeBadRequest, "Content-Type %q is not text/plain or application/x-ndjson.", ctype)
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	declared := map[string]bool{}
	for _, e := range entries {
		if !declared[e.Stream] {
			declared[e.Stream] = true
			transport := "http"
			if store.StreamKind(e.Stream) == "push" {
				transport = "push"
			}
			h.d.Status.Declare(status.Source{Stream: e.Stream, Transport: transport})
		}
	}

	var last int64
	for start := 0; start < len(entries); start += commitChunk {
		end := min(start+commitChunk, len(entries))
		last, err = h.d.Writer.CommitBatch(r.Context(), entries[start:end])
		if err != nil {
			if errors.Is(err, writer.ErrClosed) || errors.Is(err, context.Canceled) {
				dto.WriteError(w, dto.Errorf(dto.CodeBusy, "The server is shutting down; %d lines were accepted.", start).RetryAfter(5))
				return
			}
			writeErr(w, dto.Errorf(dto.CodeBadRequest, "%v.", err))
			return
		}
	}
	dto.WriteJSON(w, http.StatusOK, dto.LogAccepted{Accepted: len(entries), LastSeq: last})
}

func writeErr(w http.ResponseWriter, err error) {
	var apiErr *dto.APIError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &apiErr):
		dto.WriteError(w, apiErr)
	case errors.As(err, &tooBig), errors.Is(err, errTooLarge):
		dto.WriteError(w, dto.Errorf(dto.CodeTooLarge, "The body is larger than %d MiB.", MaxBody>>20))
	default:
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "Reading the body failed: %v.", err))
	}
}

var errTooLarge = errors.New("too large")

func lineEntry(def defaults, text string, at time.Time) store.Entry {
	return store.Entry{Stream: def.stream, Kind: store.KindLine, Severity: def.severity, Host: def.host, App: def.app, Text: text, TS: at}
}

func parseText(body io.Reader, def defaults, now time.Time) ([]store.Entry, error) {
	b, err := io.ReadAll(io.LimitReader(body, MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBody {
		return nil, errTooLarge
	}
	// An empty body is no lines, not one empty line.
	if len(b) == 0 {
		return nil, nil
	}
	lines := ingestutil.SplitLines(b)
	entries := make([]store.Entry, len(lines))
	for i, line := range lines {
		entries[i] = lineEntry(def, line, now)
	}
	return entries, nil
}

type ndjsonLine struct {
	Text     *string         `json:"text"`
	TS       string          `json:"ts"`
	Severity json.RawMessage `json:"severity"`
	Host     string          `json:"host"`
	App      string          `json:"app"`
	Stream   string          `json:"stream"`
}

func parseNDJSON(body io.Reader, def defaults, now time.Time) ([]store.Entry, error) {
	var entries []store.Entry
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	total := 0
	for sc.Scan() {
		n++
		raw := bytes.TrimSpace(sc.Bytes())
		total += len(raw)
		if total > MaxBody {
			return nil, errTooLarge
		}
		if len(raw) == 0 {
			continue
		}
		var l ndjsonLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, dto.Errorf(dto.CodeBadRequest, "Line %d is not a JSON object: %v.", n, err)
		}
		if l.Text == nil {
			return nil, dto.Errorf(dto.CodeBadRequest, "Line %d has no \"text\".", n)
		}
		d := def
		if l.Stream != "" {
			id, err := streamID(l.Stream)
			if err != nil {
				return nil, dto.Errorf(dto.CodeBadRequest, "Line %d: %v.", n, err)
			}
			d.stream = id
		}
		if d.stream == "" {
			return nil, dto.Errorf(dto.CodeBadRequest, "Line %d has no \"stream\" and the request has no ?stream=.", n)
		}
		if l.Host != "" {
			d.host = l.Host
		}
		if l.App != "" {
			d.app = l.App
		}
		if len(l.Severity) > 0 && string(l.Severity) != "null" {
			sev, err := parseSeverityJSON(l.Severity)
			if err != nil {
				return nil, dto.Errorf(dto.CodeBadRequest, "Line %d: %v.", n, err)
			}
			d.severity = sev
		}
		var src *time.Time
		if l.TS != "" {
			t, err := time.Parse(time.RFC3339Nano, l.TS)
			if err != nil {
				return nil, dto.Errorf(dto.CodeBadRequest, "Line %d: \"ts\" is not RFC 3339: %v.", n, err)
			}
			t = t.UTC()
			src = &t
		}
		for _, line := range ingestutil.SplitLines([]byte(*l.Text)) {
			e := lineEntry(d, line, now)
			e.SrcTS = src
			entries = append(entries, e)
		}
	}
	if err := sc.Err(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || errors.Is(err, bufio.ErrTooLong) {
			return nil, errTooLarge
		}
		return nil, err
	}
	return entries, nil
}

func parseSeverityJSON(raw json.RawMessage) (int, error) {
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		if n < 0 || n > 7 {
			return 0, fmt.Errorf("severity %d is not 0 to 7", n)
		}
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, errors.New("\"severity\" must be a number or a name")
	}
	return store.ParseSeverity(s)
}
