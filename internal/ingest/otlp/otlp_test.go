package otlp

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/nebuloss/cafard/internal/ingest/ingestutil/ingesttest"
	"github.com/nebuloss/cafard/internal/store"
)

// referenceTypes are ExportLogsServiceResponse, ExportLogsPartialSuccess
// and google.rpc.Status, declared as in their .proto files
// (opentelemetry/proto/collector/logs/v1/logs_service.proto and
// google/rpc/status.proto, without its Any details) and built with
// dynamicpb, since the generated collector package pulls in gRPC.
func referenceTypes(t *testing.T) (resp, partial, status protoreflect.MessageDescriptor) {
	t.Helper()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	field := func(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type, json string, msg string) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(num), Label: opt, Type: typ.Enum(), JsonName: proto.String(json)}
		if msg != "" {
			f.TypeName = proto.String(msg)
		}
		return f
	}
	collector := &descriptorpb.FileDescriptorProto{
		Name: proto.String("logs_service.proto"), Package: proto.String("opentelemetry.proto.collector.logs.v1"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("ExportLogsServiceResponse"), Field: []*descriptorpb.FieldDescriptorProto{
				field("partial_success", 1, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, "partialSuccess", ".opentelemetry.proto.collector.logs.v1.ExportLogsPartialSuccess"),
			}},
			{Name: proto.String("ExportLogsPartialSuccess"), Field: []*descriptorpb.FieldDescriptorProto{
				field("rejected_log_records", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64, "rejectedLogRecords", ""),
				field("error_message", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING, "errorMessage", ""),
			}},
		},
	}
	rpc := &descriptorpb.FileDescriptorProto{
		Name: proto.String("status.proto"), Package: proto.String("google.rpc"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Status"), Field: []*descriptorpb.FieldDescriptorProto{
			field("code", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32, "code", ""),
			field("message", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING, "message", ""),
		}}},
	}
	cf, err := protodesc.NewFile(collector, nil)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := protodesc.NewFile(rpc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cf.Messages().Get(0), cf.Messages().Get(1), rf.Messages().Get(0)
}

func TestResponseEncodingMatchesTheReferenceTypes(t *testing.T) {
	respD, partialD, statusD := referenceTypes(t)
	tests := []struct {
		name     string
		rejected int64
		msg      string
		set      bool
	}{
		{"empty", 0, "", false},
		{"partial success", 3, "3 log records were refused.", true},
		{"rejected only", 1, "", true},
		{"message only", 0, "warning", true},
		{"big count", 1 << 40, strings.Repeat("é", 200), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := dynamicpb.NewMessage(respD)
			if tt.set {
				ps := dynamicpb.NewMessage(partialD)
				if tt.rejected != 0 {
					ps.Set(partialD.Fields().ByName("rejected_log_records"), protoreflect.ValueOfInt64(tt.rejected))
				}
				if tt.msg != "" {
					ps.Set(partialD.Fields().ByName("error_message"), protoreflect.ValueOfString(tt.msg))
				}
				ref.Set(respD.Fields().ByName("partial_success"), protoreflect.ValueOfMessage(ps))
			}
			want, err := proto.MarshalOptions{Deterministic: true}.Marshal(ref)
			if err != nil {
				t.Fatal(err)
			}
			if got := encodeResponse(false, tt.rejected, tt.msg); !bytes.Equal(got, want) {
				t.Errorf("protobuf\n got %x\nwant %x", got, want)
			}
			// protojson output is deliberately unstable, so compare as messages.
			back := dynamicpb.NewMessage(respD)
			if err := protojson.Unmarshal(encodeResponse(true, tt.rejected, tt.msg), back); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(back, ref) {
				t.Errorf("json %s decodes to %v, want %v", encodeResponse(true, tt.rejected, tt.msg), back, ref)
			}
			refJSON, _ := protojson.Marshal(ref)
			if !jsonEqual(t, refJSON, encodeResponse(true, tt.rejected, tt.msg)) {
				t.Errorf("json %s, reference %s", encodeResponse(true, tt.rejected, tt.msg), refJSON)
			}
		})
	}

	for _, code := range []int32{codeInvalidArgument, codeUnavailable} {
		ref := dynamicpb.NewMessage(statusD)
		ref.Set(statusD.Fields().ByName("code"), protoreflect.ValueOfInt32(code))
		ref.Set(statusD.Fields().ByName("message"), protoreflect.ValueOfString("why"))
		want, _ := proto.MarshalOptions{Deterministic: true}.Marshal(ref)
		if got := encodeStatus(false, code, "why"); !bytes.Equal(got, want) {
			t.Errorf("status %d: got %x want %x", code, got, want)
		}
		refJSON, _ := protojson.Marshal(ref)
		if !jsonEqual(t, refJSON, encodeStatus(true, code, "why")) {
			t.Errorf("status json %s, reference %s", encodeStatus(true, code, "why"), refJSON)
		}
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

func TestSeverity(t *testing.T) {
	tests := []struct {
		n    logsv1.SeverityNumber
		text string
		want int
	}{
		{logsv1.SeverityNumber_SEVERITY_NUMBER_TRACE, "", 7},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_TRACE4, "", 7},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_DEBUG, "", 7},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_DEBUG4, "", 7},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_INFO, "", 6},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_INFO4, "", 6},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_WARN, "", 4},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_WARN4, "", 4},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR, "", 3},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR4, "", 3},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_FATAL, "", 2},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_FATAL4, "", 2},
		{logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR, "info", 3}, // the number wins
		{0, "WARN", 4},
		{0, "Warning", 4},
		{0, "INFO2", 6},
		{0, "error", 3},
		{0, "CRITICAL", 2},
		{0, "notice", 5},
		{0, "emerg", 0},
		{0, "trace", 7},
		{0, "", 6},
		{0, "whatever", 6},
		{99, "", 6},
	}
	for _, tt := range tests {
		if got := Severity(tt.n, tt.text); got != tt.want {
			t.Errorf("Severity(%d, %q) = %d, want %d", tt.n, tt.text, got, tt.want)
		}
	}
}

func str(s string) *commonv1.AnyValue {
	return &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: s}}
}

func kv(k string, v *commonv1.AnyValue) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: k, Value: v}
}

func sampleLogs() *logsv1.LogsData {
	return &logsv1.LogsData{ResourceLogs: []*logsv1.ResourceLogs{
		{
			Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{kv("service.name", str("checkout")), kv("host.name", str("web1"))}},
			ScopeLogs: []*logsv1.ScopeLogs{{
				Scope: &commonv1.InstrumentationScope{Name: "app", Version: "1.2"},
				LogRecords: []*logsv1.LogRecord{
					{TimeUnixNano: 1763287200000000000, SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR, SeverityText: "ERROR",
						Body: str("payment failed\nretrying"), TraceId: []byte{0x5b, 0x8e, 0xff, 0xf7, 0x98, 0x03, 0x81, 0x03, 0xd2, 0x69, 0xb6, 0x33, 0x81, 0x3f, 0xc6, 0x0c},
						SpanId: []byte{0xee, 0xe1, 0x9b, 0x7e, 0xc3, 0xc1, 0xb1, 0x74}, Attributes: []*commonv1.KeyValue{kv("order", &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: 42}})}},
					{ObservedTimeUnixNano: 1763287201000000000, Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_KvlistValue{KvlistValue: &commonv1.KeyValueList{Values: []*commonv1.KeyValue{kv("a", str("b"))}}}}},
				},
			}},
		},
		{
			Resource:  &resourcev1.Resource{Attributes: []*commonv1.KeyValue{kv("host.name", str("db1"))}},
			ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{SeverityText: "warn", Body: str("slow query")}}}},
		},
		{ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{Body: str("who am i")}}}}},
	}}
}

// sampleJSON is sampleLogs in OTLP JSON, ids in hex.
const sampleJSON = `{"resourceLogs":[
 {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}},{"key":"host.name","value":{"stringValue":"web1"}}]},
  "scopeLogs":[{"scope":{"name":"app","version":"1.2"},"logRecords":[
   {"timeUnixNano":"1763287200000000000","severityNumber":"SEVERITY_NUMBER_ERROR","severityText":"ERROR","body":{"stringValue":"payment failed\nretrying"},
    "traceId":"5B8EFFF798038103D269B633813FC60C","spanId":"eee19b7ec3c1b174","attributes":[{"key":"order","value":{"intValue":42}}],"futureField":1},
   {"observedTimeUnixNano":1763287201000000000,"body":{"kvlistValue":{"values":[{"key":"a","value":{"stringValue":"b"}}]}}}]}]},
 {"resource":{"attributes":[{"key":"host.name","value":{"stringValue":"db1"}}]},
  "scope_logs":[{"log_records":[{"severity_text":"warn","body":{"stringValue":"slow query"},"trace_id":"not hex","span_id":"0000000000000000"}]}]},
 {"scopeLogs":[{"logRecords":[{"body":{"stringValue":"who am i"}}]}]}]}`

func gz(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

type call struct {
	ctype, encoding string
	body            []byte
}

func do(h http.Handler, c call) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(c.body))
	r.Header.Set("Content-Type", c.ctype)
	if c.encoding != "" {
		r.Header.Set("Content-Encoding", c.encoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRequestsLandAsEntries(t *testing.T) {
	pb, err := proto.Marshal(sampleLogs())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		c    call
	}{
		{"protobuf", call{"application/x-protobuf", "", pb}},
		{"protobuf gzip", call{"application/x-protobuf", "gzip", gz(pb)}},
		{"json", call{"application/json", "", []byte(sampleJSON)}},
		{"json gzip", call{"application/json; charset=utf-8", "gzip", gz([]byte(sampleJSON))}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, st := ingesttest.NewWriter(), ingesttest.NewStatus()
			rec := do(NewHandler(ingesttest.Deps(w, st), 8<<20), tt.c)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d %s", rec.Code, rec.Body)
			}
			wantType, wantBody := "application/x-protobuf", ""
			if strings.HasPrefix(tt.c.ctype, "application/json") {
				wantType, wantBody = "application/json", "{}"
			}
			if rec.Header().Get("Content-Type") != wantType || rec.Body.String() != wantBody {
				t.Errorf("response %q %q", rec.Header().Get("Content-Type"), rec.Body)
			}
			es := w.Entries()
			if len(es) != 5 {
				t.Fatalf("%d entries: %+v", len(es), es)
			}
			type want struct {
				stream, text, host, app string
				sev                     int
				src                     int64
			}
			for i, wt := range []want{
				{"otlp:checkout", "payment failed", "web1", "checkout", 3, 1763287200000000000},
				{"otlp:checkout", "retrying", "web1", "checkout", 3, 1763287200000000000},
				{"otlp:checkout", `{"a":"b"}`, "web1", "checkout", 6, 1763287201000000000},
				{"otlp:db1", "slow query", "db1", "", 4, 0},
				{"otlp:unknown", "who am i", "", "", 6, 0},
			} {
				e := es[i]
				if e.Stream != wt.stream || e.Text != wt.text || e.Host != wt.host || e.App != wt.app || e.Severity != wt.sev {
					t.Errorf("entry %d = %+v, want %+v", i, e, wt)
				}
				if (wt.src == 0) != (e.SrcTS == nil) || (e.SrcTS != nil && e.SrcTS.UnixNano() != wt.src) {
					t.Errorf("entry %d src_ts %v, want %d", i, e.SrcTS, wt.src)
				}
			}
			var meta map[string]any
			if err := json.Unmarshal(es[0].Meta, &meta); err != nil {
				t.Fatal(err)
			}
			if meta["trace_id"] != "5b8efff798038103d269b633813fc60c" || meta["span_id"] != "eee19b7ec3c1b174" ||
				meta["scope"] != "app" || meta["attributes"].(map[string]any)["order"] != float64(42) ||
				meta["resource"].(map[string]any)["host.name"] != "web1" {
				t.Errorf("meta %s", es[0].Meta)
			}
			if strings.Contains(string(es[3].Meta), "trace_id") || strings.Contains(string(es[3].Meta), "span_id") {
				t.Errorf("invalid ids kept: %s", es[3].Meta)
			}
			st.Get(func(s *ingesttest.Status) {
				if s.Declared["otlp:checkout"] != "otlp" {
					t.Errorf("declared %v", s.Declared)
				}
			})
		})
	}
}

func TestConcatenatedJSONRequests(t *testing.T) {
	w := ingesttest.NewWriter()
	body := sampleJSON + "\n" + `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"next"},"traceId":"0102030405060708090a0b0c0d0e0f10"}]}]}]}` + "\n"
	rec := do(newHandler(w), call{"application/json", "", []byte(body)})
	es := w.Entries()
	if rec.Code != http.StatusOK || len(es) != 6 || es[5].Text != "next" || !strings.Contains(string(es[5].Meta), "0102030405060708090a0b0c0d0e0f10") {
		t.Fatalf("status %d entries %+v", rec.Code, es)
	}
	if !strings.Contains(string(es[0].Meta), "5b8efff798038103d269b633813fc60c") {
		t.Errorf("first request lost its ids: %s", es[0].Meta)
	}
	if rec := do(newHandler(w), call{"application/json", "", []byte("  ")}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: status %d", rec.Code)
	}
}

func TestPartialSuccess(t *testing.T) {
	w := ingesttest.NewWriter()
	w.Refuse = func(e store.Entry) error {
		if e.Stream == "otlp:db1" {
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	h := newHandler(w)
	pb, _ := proto.Marshal(sampleLogs())
	rec := do(h, call{"application/x-protobuf", "", pb})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), encodeResponse(false, 1, "1 log records were refused.")) {
		t.Fatalf("status %d body %x", rec.Code, rec.Body.Bytes())
	}
	rec = do(h, call{"application/json", "", []byte(sampleJSON)})
	if rec.Code != http.StatusOK || rec.Body.String() != `{"partialSuccess":{"errorMessage":"1 log records were refused.","rejectedLogRecords":"1"}}` {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestBusyWriter(t *testing.T) {
	w := ingesttest.NewWriter()
	w.SetFull(true)
	h := newHandler(w)
	h.wait = 50 * time.Millisecond
	start := time.Now()
	rec := do(h, call{"application/json", "", []byte(sampleJSON)})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d retry-after %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Error("did not wait for the writer")
	}
	var st struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &st) != nil || st.Code != codeUnavailable {
		t.Errorf("body %s", rec.Body)
	}

	// Room for two entries: the first record (two lines) is stored, the rest refused.
	w2 := ingesttest.NewWriter()
	w2.Limit = 2
	h2 := newHandler(w2)
	h2.wait = 20 * time.Millisecond
	rec = do(h2, call{"application/json", "", []byte(sampleJSON)})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"rejectedLogRecords":"3"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}

	// A writer that frees up within the wait is waited for.
	w3 := ingesttest.NewWriter()
	w3.SetFull(true)
	go func() { time.Sleep(30 * time.Millisecond); w3.SetFull(false) }()
	rec = do(newHandler(w3), call{"application/json", "", []byte(sampleJSON)})
	if rec.Code != http.StatusOK || len(w3.Entries()) != 5 {
		t.Fatalf("status %d, %d entries", rec.Code, len(w3.Entries()))
	}
}

func TestRefusals(t *testing.T) {
	h := NewHandler(ingesttest.Deps(ingesttest.NewWriter(), ingesttest.NewStatus()), 1<<20)
	big := bytes.Repeat([]byte(" "), 1<<20+1)
	tests := []struct {
		name string
		c    call
		code int
	}{
		{"too large", call{"application/json", "", big}, http.StatusRequestEntityTooLarge},
		{"too large after gunzip", call{"application/json", "gzip", gz(big)}, http.StatusRequestEntityTooLarge},
		{"bad gzip", call{"application/json", "gzip", []byte("nope")}, http.StatusBadRequest},
		{"bad json", call{"application/json", "", []byte(`{"resourceLogs":[`)}, http.StatusBadRequest},
		{"bad protobuf", call{"application/x-protobuf", "", []byte{0x0a, 0xff}}, http.StatusBadRequest},
		{"wrong type", call{"text/plain", "", []byte("x")}, http.StatusUnsupportedMediaType},
		{"wrong encoding", call{"application/json", "br", []byte("{}")}, http.StatusUnsupportedMediaType},
	}
	for _, tt := range tests {
		rec := do(h, tt.c)
		if rec.Code != tt.code {
			t.Errorf("%s: status %d, want %d (%s)", tt.name, rec.Code, tt.code, rec.Body)
		}
	}
	rec := do(h, call{"application/x-protobuf", "", []byte{0x0a, 0xff}})
	if rec.Header().Get("Content-Type") != "application/x-protobuf" || len(rec.Body.Bytes()) == 0 || rec.Body.Bytes()[0] != 0x08 {
		t.Errorf("protobuf error body %q %x", rec.Header().Get("Content-Type"), rec.Body.Bytes())
	}
}

func TestMetaIsCapped(t *testing.T) {
	var attrs []*commonv1.KeyValue
	for i := range 300 {
		attrs = append(attrs, kv(strings.Repeat("k", 10)+string(rune('a'+i%26))+strings.Repeat("x", i%7), str(strings.Repeat("v", 50))))
	}
	data := &logsv1.LogsData{ResourceLogs: []*logsv1.ResourceLogs{{
		Resource:  &resourcev1.Resource{Attributes: append(attrs, kv("service.name", str("svc")))},
		ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{Body: str("x"), Attributes: attrs, TraceId: bytes.Repeat([]byte{1}, 16)}}}},
	}}}
	recs := mapRecords(decoded{data: data}, time.Now())
	m := recs[0].entries[0].Meta
	if len(m) > store.MaxMetaBytes || !json.Valid(m) || !strings.Contains(string(m), `"truncated":true`) || !strings.Contains(string(m), `"trace_id"`) {
		t.Fatalf("meta of %d bytes: %s", len(m), m)
	}
}

func FuzzDecodeJSON(f *testing.F) {
	f.Add([]byte(sampleJSON))
	f.Add([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"traceId":1}]}]}]}`))
	f.Add([]byte(`{"resource_logs":null}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := decodeJSON(b)
		if err != nil {
			return
		}
		checkRecords(t, mapRecords(d, time.Now()))
	})
}

func FuzzDecodeProtobuf(f *testing.F) {
	pb, _ := proto.Marshal(sampleLogs())
	f.Add(pb)
	f.Add([]byte{0x0a, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := decodeProtobuf(b)
		if err != nil {
			return
		}
		checkRecords(t, mapRecords(d, time.Now()))
	})
}

func checkRecords(t *testing.T, recs []record) {
	for _, r := range recs {
		for _, e := range r.entries {
			if err := store.ValidateStreamID(e.Stream); err != nil {
				t.Fatal(err)
			}
			if len(e.Meta) > store.MaxMetaBytes || (len(e.Meta) > 0 && !json.Valid(e.Meta)) || strings.ContainsAny(e.Text, "\r\n") {
				t.Fatalf("entry %+v", e)
			}
		}
	}
}

// newHandler is the handler on w with an 8 MiB body limit.
func newHandler(w *ingesttest.Writer) *Handler {
	return NewHandler(ingesttest.Deps(w, ingesttest.NewStatus()), 8<<20)
}
