// Package otlp is POST /v1/logs, OTLP/HTTP logs in protobuf or JSON, so
// OpenTelemetry exporters (Collector, Vector, Fluent Bit, SDKs) can send
// to cafard unchanged. Only the message types are used: the collector
// service packages pull in gRPC.
package otlp

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/ingest/ingestutil"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/writer"
)

// Config is the `otlp:` config section.
type Config struct {
	Enabled bool `yaml:"enabled"`
	// MaxBodyMiB bounds a request body after decompression.
	MaxBodyMiB int `yaml:"max_body_mib"`
}

// SetDefaults implements config.Defaulter.
func (c *Config) SetDefaults() {
	c.Enabled = true
	c.MaxBodyMiB = 8
}

// Validate implements config.Validator.
func (c *Config) Validate() error {
	if c.MaxBodyMiB < 1 || c.MaxBodyMiB > 256 {
		return fmt.Errorf("max_body_mib: %d is not between 1 and 256", c.MaxBodyMiB)
	}
	return nil
}

// WriterWait is how long a request waits for a full writer queue before
// it is answered 503.
const WriterWait = 2 * time.Second

// RetryAfter is the Retry-After sent with a 503, in seconds.
const RetryAfter = 5

// Register mounts POST /v1/logs when OTLP is enabled.
func Register(mux *http.ServeMux, _ *mcpserver.Server, d deps.Deps, cfg *Config) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	mux.Handle("POST /v1/logs", auth.Require(auth.BearerOnly{Next: d.Auth}, auth.ScopeIngest, NewHandler(d, cfg.MaxBodyMiB<<20)))
	return nil, nil
}

// Handler serves POST /v1/logs. It does no authentication itself.
type Handler struct {
	d       deps.Deps
	maxBody int
	declare ingestutil.Declarer
	wait    time.Duration
}

// NewHandler returns the handler with a body limit in bytes.
func NewHandler(d deps.Deps, maxBody int) *Handler {
	return &Handler{d: d, maxBody: maxBody, declare: ingestutil.Declarer{Status: d.Status, Transport: "otlp"}, wait: WriterWait}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctype, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var asJSON bool
	switch ctype {
	case "application/x-protobuf", "application/protobuf":
	case "application/json":
		asJSON = true
	default:
		writeStatus(w, true, http.StatusUnsupportedMediaType, codeInvalidArgument,
			fmt.Sprintf("Content-Type %q is not application/x-protobuf or application/json.", ctype))
		return
	}

	body, apiStatus, msg := h.readBody(w, r)
	if apiStatus != 0 {
		writeStatus(w, asJSON, apiStatus, statusCode(apiStatus), msg)
		return
	}
	var (
		dec decoded
		err error
	)
	if asJSON {
		dec, err = decodeJSON(body)
	} else {
		dec, err = decodeProtobuf(body)
	}
	if err != nil {
		writeStatus(w, asJSON, http.StatusBadRequest, codeInvalidArgument, "The body is not a valid OTLP logs request: "+err.Error())
		return
	}

	records := mapRecords(dec, h.d.Clock.Now())
	queued, rejected, busy := h.submit(r.Context(), records)
	if busy && queued == 0 {
		w.Header().Set("Retry-After", strconv.Itoa(RetryAfter))
		writeStatus(w, asJSON, http.StatusServiceUnavailable, codeUnavailable, "cafard is busy writing; retry later.")
		return
	}
	var note string
	switch {
	case busy:
		note = fmt.Sprintf("cafard is busy writing; %d log records were not stored.", rejected)
	case rejected > 0:
		note = fmt.Sprintf("%d log records were refused.", rejected)
	}
	writeBody(w, asJSON, http.StatusOK, encodeResponse(asJSON, int64(rejected), note))
}

// readBody returns the decompressed body, or an HTTP status and a message.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request) ([]byte, int, string) {
	tooBig := fmt.Sprintf("The body is larger than %d MiB.", h.maxBody>>20)
	in := io.Reader(http.MaxBytesReader(w, r.Body, int64(h.maxBody)))
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(in)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, http.StatusRequestEntityTooLarge, tooBig
			}
			return nil, http.StatusBadRequest, "The body is not valid gzip."
		}
		defer zr.Close()
		in = zr
	default:
		return nil, http.StatusUnsupportedMediaType, fmt.Sprintf("Content-Encoding %q is not gzip.", enc)
	}
	body, err := io.ReadAll(io.LimitReader(in, int64(h.maxBody)+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, http.StatusRequestEntityTooLarge, tooBig
		}
		return nil, http.StatusBadRequest, "Reading the body failed: " + err.Error()
	}
	if len(body) > h.maxBody {
		return nil, http.StatusRequestEntityTooLarge, tooBig
	}
	return body, 0, ""
}

// submit queues every record's entries. A full queue is waited on for up
// to h.wait; when that runs out, busy is set and the remaining records
// count as rejected.
func (h *Handler) submit(ctx context.Context, records []record) (queued, rejected int, busy bool) {
	for i, rec := range records {
		ok := true
		for _, e := range rec.entries {
			h.declare.Declare(e.Stream)
			err := h.d.Writer.Submit(ctx, e, true)
			if errors.Is(err, writer.ErrFull) {
				wctx, cancel := context.WithTimeout(ctx, h.wait)
				err = h.d.Writer.Submit(wctx, e, false)
				cancel()
			}
			if err == nil {
				queued++
				continue
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, writer.ErrClosed) {
				return queued, rejected + len(records) - i, true
			}
			// The writer refused this entry itself; the record is rejected.
			ok = false
			break
		}
		if !ok {
			rejected++
		}
	}
	return queued, rejected, false
}

func statusCode(httpStatus int) int32 {
	switch httpStatus {
	case http.StatusRequestEntityTooLarge:
		return codeResourceExhausted
	case http.StatusServiceUnavailable:
		return codeUnavailable
	}
	return codeInvalidArgument
}

func writeStatus(w http.ResponseWriter, asJSON bool, httpStatus int, code int32, msg string) {
	writeBody(w, asJSON, httpStatus, encodeStatus(asJSON, code, msg))
}

func writeBody(w http.ResponseWriter, asJSON bool, status int, b []byte) {
	if asJSON {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "application/x-protobuf")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
