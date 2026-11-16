package otlp

import (
	"encoding/json"
	"strconv"

	"google.golang.org/protobuf/encoding/protowire"
)

// The responses are encoded by hand: their types live in the collector
// packages, which pull in gRPC. A test checks these bytes against the
// reference messages.

// encodeResponse is an ExportLogsServiceResponse: empty, or with
// partial_success (field 1) holding rejected_log_records (1, int64) and
// error_message (2, string).
func encodeResponse(asJSON bool, rejected int64, msg string) []byte {
	if asJSON {
		if rejected == 0 && msg == "" {
			return []byte("{}")
		}
		ps := map[string]string{}
		if rejected != 0 {
			// protojson writes 64-bit integers as strings.
			ps["rejectedLogRecords"] = strconv.FormatInt(rejected, 10)
		}
		if msg != "" {
			ps["errorMessage"] = msg
		}
		b, _ := json.Marshal(map[string]any{"partialSuccess": ps})
		return b
	}
	if rejected == 0 && msg == "" {
		return []byte{}
	}
	var inner []byte
	if rejected != 0 {
		inner = protowire.AppendTag(inner, 1, protowire.VarintType)
		inner = protowire.AppendVarint(inner, uint64(rejected))
	}
	if msg != "" {
		inner = protowire.AppendTag(inner, 2, protowire.BytesType)
		inner = protowire.AppendString(inner, msg)
	}
	out := protowire.AppendTag(nil, 1, protowire.BytesType)
	return protowire.AppendBytes(out, inner)
}

// gRPC status codes used in error bodies.
const (
	codeInvalidArgument   = 3
	codeResourceExhausted = 8
	codeUnavailable       = 14
)

// encodeStatus is a google.rpc.Status, the OTLP error body: code (1,
// int32) and message (2, string).
func encodeStatus(asJSON bool, code int32, msg string) []byte {
	if asJSON {
		b, _ := json.Marshal(struct {
			Code    int32  `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		}{code, msg})
		return b
	}
	var out []byte
	if code != 0 {
		out = protowire.AppendTag(out, 1, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(int64(code)))
	}
	if msg != "" {
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		out = protowire.AppendString(out, msg)
	}
	return out
}
