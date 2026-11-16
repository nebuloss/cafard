package otlp

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/nebuloss/cafard/internal/ingest/ingestutil"
	"github.com/nebuloss/cafard/internal/store"
)

// Severity maps an OTLP severity to syslog: TRACE and DEBUG to 7, INFO to
// 6, WARN to 4, ERROR to 3, FATAL to 2. An unset number falls back to the
// text, then to 6.
func Severity(n logsv1.SeverityNumber, text string) int {
	switch {
	case n >= 1 && n <= 8:
		return 7
	case n >= 9 && n <= 12:
		return 6
	case n >= 13 && n <= 16:
		return 4
	case n >= 17 && n <= 20:
		return 3
	case n >= 21 && n <= 24:
		return 2
	}
	return severityText(text)
}

func severityText(text string) int {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimRight(t, "0123456789") // "INFO2", "WARN4"
	switch t {
	case "":
		return store.DefaultSeverity
	case "trace", "debug", "dbg", "verbose":
		return 7
	case "info", "information":
		return 6
	case "warn", "warning":
		return 4
	case "error", "err":
		return 3
	case "fatal", "critical", "crit":
		return 2
	}
	if n, err := store.ParseSeverity(t); err == nil {
		return n
	}
	return store.DefaultSeverity
}

// record is the entries one log record became, for partial success.
type record struct {
	entries []store.Entry
}

// mapRecords turns decoded logs into entries, one record at a time.
func mapRecords(d decoded, now time.Time) []record {
	var out []record
	for ri, rl := range d.data.GetResourceLogs() {
		resAttrs := rl.GetResource().GetAttributes()
		service := stringAttr(resAttrs, "service.name")
		host := stringAttr(resAttrs, "host.name")
		name := ingestutil.StreamName(service)
		if name == "" {
			name = ingestutil.StreamName(host)
		}
		if name == "" {
			name = "unknown"
		}
		stream := "otlp:" + name
		resource := attrMap(resAttrs)
		for si, sl := range rl.GetScopeLogs() {
			scope := sl.GetScope()
			for li, lr := range sl.GetLogRecords() {
				id := d.ids(ri, si, li, lr)
				var meta ingestutil.Meta
				meta.Add("trace_id", id.trace)
				meta.Add("span_id", id.span)
				meta.Add("severity_text", lr.GetSeverityText())
				meta.Add("event_name", lr.GetEventName())
				meta.Add("scope", scope.GetName())
				meta.Add("scope_version", scope.GetVersion())
				meta.Add("attributes", attrMap(lr.GetAttributes()))
				meta.Add("resource", resource)
				raw := meta.JSON()

				text := bodyText(lr.GetBody())
				if text == "" {
					text = lr.GetEventName()
				}
				lines := ingestutil.SplitLines([]byte(text))
				if len(lines) == 0 {
					lines = []string{""}
				}
				src := srcTime(lr)
				sev := Severity(lr.GetSeverityNumber(), lr.GetSeverityText())
				rec := record{entries: make([]store.Entry, len(lines))}
				for i, line := range lines {
					rec.entries[i] = store.Entry{
						TS: now, SrcTS: src, Stream: stream, Kind: store.KindLine, Severity: sev,
						Host: ingestutil.StreamName(host), App: ingestutil.StreamName(service),
						Text: line, Meta: raw,
					}
				}
				out = append(out, rec)
			}
		}
	}
	return out
}

func srcTime(lr *logsv1.LogRecord) *time.Time {
	ns := lr.GetTimeUnixNano()
	if ns == 0 {
		ns = lr.GetObservedTimeUnixNano()
	}
	if ns == 0 || ns > math.MaxInt64 {
		return nil
	}
	t := time.Unix(0, int64(ns)).UTC()
	return &t
}

func stringAttr(kvs []*commonv1.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}

func attrMap(kvs []*commonv1.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		m[kv.GetKey()] = anyValue(kv.GetValue())
	}
	return m
}

// anyValue converts an AnyValue to plain Go values for JSON. Bytes are
// base64, as in OTLP JSON; 64-bit integers stay numbers.
func anyValue(v *commonv1.AnyValue) any {
	switch x := v.GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return x.StringValue
	case *commonv1.AnyValue_BoolValue:
		return x.BoolValue
	case *commonv1.AnyValue_IntValue:
		return x.IntValue
	case *commonv1.AnyValue_DoubleValue:
		if math.IsNaN(x.DoubleValue) || math.IsInf(x.DoubleValue, 0) {
			return nil
		}
		return x.DoubleValue
	case *commonv1.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	case *commonv1.AnyValue_ArrayValue:
		vals := x.ArrayValue.GetValues()
		out := make([]any, len(vals))
		for i, e := range vals {
			out[i] = anyValue(e)
		}
		return out
	case *commonv1.AnyValue_KvlistValue:
		m := attrMap(x.KvlistValue.GetValues())
		if m == nil {
			m = map[string]any{}
		}
		return m
	}
	return nil
}

// bodyText is a string body as is, anything else as compact JSON.
func bodyText(v *commonv1.AnyValue) string {
	if v == nil {
		return ""
	}
	if s, ok := v.GetValue().(*commonv1.AnyValue_StringValue); ok {
		return s.StringValue
	}
	val := anyValue(v)
	if val == nil {
		return ""
	}
	b, err := json.Marshal(val)
	if err != nil {
		return ""
	}
	return string(b)
}
