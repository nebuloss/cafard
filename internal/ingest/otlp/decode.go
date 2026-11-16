package otlp

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ids are a record's trace and span ids, as lowercase hex ("" when unset).
type ids struct {
	trace, span string
}

// decoded is a request body: the records, plus the hex ids JSON carried
// (which protojson would have read as base64). jsonIDs is nil for
// protobuf, where the ids are in the records.
type decoded struct {
	data    *logsv1.LogsData
	jsonIDs [][][]ids
}

func (d decoded) ids(r, s, l int, rec *logsv1.LogRecord) ids {
	if d.jsonIDs == nil {
		return ids{trace: hexID(rec.GetTraceId(), 16), span: hexID(rec.GetSpanId(), 8)}
	}
	if r < len(d.jsonIDs) && s < len(d.jsonIDs[r]) && l < len(d.jsonIDs[r][s]) {
		return d.jsonIDs[r][s][l]
	}
	return ids{}
}

// hexID renders an id of the given size; all-zero and wrongly sized ids
// are invalid in OTLP and render as "".
func hexID(b []byte, size int) string {
	if len(b) != size || bytes.Count(b, []byte{0}) == size {
		return ""
	}
	return hex.EncodeToString(b)
}

// cleanHexID checks an id from OTLP JSON.
func cleanHexID(s string, size int) string {
	b, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	return hexID(b, size)
}

// decodeProtobuf reads an ExportLogsServiceRequest, which is LogsData on
// the wire (field 1, repeated ResourceLogs).
func decodeProtobuf(b []byte) (decoded, error) {
	var data logsv1.LogsData
	if err := proto.Unmarshal(b, &data); err != nil {
		return decoded{}, err
	}
	return decoded{data: &data}, nil
}

// decodeJSON reads OTLP JSON. traceId and spanId are hex there, so they
// are lifted out of each record before protojson sees the body. Several
// requests one after the other (Vector's legacy json codec sends
// newline-delimited objects) are read as one.
func decodeJSON(b []byte) (decoded, error) {
	out := decoded{data: &logsv1.LogsData{}, jsonIDs: [][][]ids{}}
	dec := json.NewDecoder(bytes.NewReader(b))
	for n := 0; ; n++ {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == io.EOF {
			if n == 0 {
				return decoded{}, io.ErrUnexpectedEOF
			}
			return out, nil
		} else if err != nil {
			return decoded{}, err
		}
		fixed, found, err := liftJSONIDs(raw)
		if err != nil {
			return decoded{}, err
		}
		var data logsv1.LogsData
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(fixed, &data); err != nil {
			return decoded{}, err
		}
		// Keep ids aligned with resources, whatever protojson made of them.
		for len(found) < len(data.ResourceLogs) {
			found = append(found, nil)
		}
		out.data.ResourceLogs = append(out.data.ResourceLogs, data.ResourceLogs...)
		out.jsonIDs = append(out.jsonIDs, found[:len(data.ResourceLogs)]...)
	}
}

type object = map[string]json.RawMessage

// field returns the value of the first of names present in o (OTLP JSON
// uses lowerCamelCase; protojson also accepts the proto names).
func field(o object, names ...string) (string, json.RawMessage) {
	for _, n := range names {
		if v, ok := o[n]; ok {
			return n, v
		}
	}
	return "", nil
}

func objects(raw json.RawMessage) ([]object, error) {
	if raw == nil || string(raw) == "null" {
		return nil, nil
	}
	var out []object
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// liftJSONIDs removes traceId and spanId from every log record, returning
// the rewritten JSON and the ids by resource, scope and record index.
func liftJSONIDs(b []byte) ([]byte, [][][]ids, error) {
	var root object
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, nil, err
	}
	rName, rRaw := field(root, "resourceLogs", "resource_logs")
	rls, err := objects(rRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("resourceLogs: %w", err)
	}
	found := make([][][]ids, len(rls))
	changed := false
	for ri, rl := range rls {
		if rl == nil {
			continue
		}
		sName, sRaw := field(rl, "scopeLogs", "scope_logs")
		sls, err := objects(sRaw)
		if err != nil {
			return nil, nil, fmt.Errorf("scopeLogs: %w", err)
		}
		found[ri] = make([][]ids, len(sls))
		scopeChanged := false
		for si, sl := range sls {
			if sl == nil {
				continue
			}
			lName, lRaw := field(sl, "logRecords", "log_records")
			recs, err := objects(lRaw)
			if err != nil {
				return nil, nil, fmt.Errorf("logRecords: %w", err)
			}
			found[ri][si] = make([]ids, len(recs))
			recChanged := false
			for li, rec := range recs {
				if rec == nil {
					continue
				}
				for _, k := range []string{"traceId", "trace_id", "spanId", "span_id"} {
					v, ok := rec[k]
					if !ok {
						continue
					}
					delete(rec, k)
					recChanged = true
					var s string
					if json.Unmarshal(v, &s) != nil {
						continue
					}
					if strings.HasPrefix(k, "trace") {
						found[ri][si][li].trace = cleanHexID(s, 16)
					} else {
						found[ri][si][li].span = cleanHexID(s, 8)
					}
				}
			}
			if recChanged {
				raw, err := json.Marshal(recs)
				if err != nil {
					return nil, nil, err
				}
				sl[lName] = raw
				scopeChanged = true
			}
		}
		if scopeChanged {
			raw, err := json.Marshal(sls)
			if err != nil {
				return nil, nil, err
			}
			rl[sName] = raw
			changed = true
		}
	}
	if !changed {
		return b, found, nil
	}
	raw, err := json.Marshal(rls)
	if err != nil {
		return nil, nil, err
	}
	root[rName] = raw
	out, err := json.Marshal(root)
	return out, found, err
}
