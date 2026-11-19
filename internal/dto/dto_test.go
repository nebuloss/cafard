package dto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAttachmentUnion(t *testing.T) {
	img := ImageOf(ImageAttachment{SHA256: "ab", Mime: "image/png", URL: "/blob/ab"})
	b, err := json.Marshal(img)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"image","sha256":"ab","mime":"image/png","url":"/blob/ab","preview":false}`; string(b) != want {
		t.Errorf("image: got %s, want %s", b, want)
	}
	var back Attachment
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Image == nil || back.Log != nil || back.Image.SHA256 != "ab" {
		t.Errorf("image round trip: %+v", back)
	}

	b, err = json.Marshal(LogOf(LogAttachment{Stream: "serial:lab/dut1", FromSeq: 5}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"log","stream":"serial:lab/dut1","streams":[],"from_seq":5,"to_seq":null,"lines":0,"snapshot":false,"expired":false,"preview":[]}`
	if string(b) != want {
		t.Errorf("log: got %s, want %s", b, want)
	}
	back = Attachment{}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Log == nil || back.Image != nil || back.Log.ToSeq != nil || back.Log.FromSeq != 5 {
		t.Errorf("log round trip: %+v", back)
	}

	if err := json.Unmarshal([]byte(`{"type":"video","url":"/x"}`), &back); err == nil {
		t.Error("unknown type: no error")
	}
	if err := json.Unmarshal([]byte(`{"url":"/x"}`), &back); err == nil {
		t.Error("missing type: no error")
	}
	if _, err := json.Marshal(Attachment{}); err == nil {
		t.Error("empty attachment: no error")
	}
	both := Attachment{Image: &ImageAttachment{}, Log: &LogAttachment{}}
	if _, err := json.Marshal(both); err == nil {
		t.Error("both set: no error")
	}
}

func TestPageNilItems(t *testing.T) {
	b, err := json.Marshal(Page[Entry]{})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"items":[],"has_more":false}`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
	b, err = json.Marshal(&Page[Chat]{HasMore: true, NextSeq: 9})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"items":[],"has_more":true,"next_seq":9}`; string(b) != want {
		t.Errorf("pointer: got %s, want %s", b, want)
	}
}

func TestRequiredArraysNeverNull(t *testing.T) {
	for _, v := range []any{Me{}, Chat{}, Stream{}, Overview{}, Counts{}, Token{}, TokenCreated{}, Share{}, Question{}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%T: %v", v, err)
		}
		if strings.Contains(string(b), "null") {
			t.Errorf("%T: %s", v, b)
		}
	}
}

func TestTokenCreatedInlinesToken(t *testing.T) {
	b, err := json.Marshal(TokenCreated{Token: Token{ID: 3, Label: "x"}, Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"id":3`, `"label":"x"`, `"token":"s"`, `"snippets":[]`, `"scopes":[]`} {
		if !strings.Contains(string(b), s) {
			t.Errorf("missing %s in %s", s, b)
		}
	}
}

func TestTime(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	v := TimeOf(time.Date(2026, 11, 16, 10, 41, 2, 118734502, loc))
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"2026-11-16T09:41:02.118734502Z"`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
	var back Time
	if err := json.Unmarshal([]byte(`"2026-11-16T10:41:02.118734502+01:00"`), &back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(v.Time) || back.Location() != time.UTC {
		t.Errorf("got %v", back)
	}
	if err := json.Unmarshal([]byte(`"yesterday"`), &back); err == nil {
		t.Error("bad timestamp: no error")
	}
	if TimePtr(time.Time{}) != nil {
		t.Error("TimePtr(zero) is not nil")
	}
}

func TestHTTPStatus(t *testing.T) {
	want := map[string]int{
		CodeBadRequest: 400, CodeInvalidPattern: 400, CodeUnknownChannel: 400,
		CodeTooLarge: 413, CodeUnauthenticated: 401, CodeForbidden: 403,
		CodeCrossOrigin: 403, CodeNotFound: 404, CodeConflict: 409,
		CodeRateLimited: 429, CodeBusy: 503, "nonsense": 500,
	}
	for code, status := range want {
		if got := HTTPStatus(code); got != status {
			t.Errorf("%s: got %d, want %d", code, got, status)
		}
	}
	if len(Codes) != len(want)-1 {
		t.Errorf("Codes has %d entries, the test %d", len(Codes), len(want)-1)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, Errorf(CodeRateLimited, "You sent %d messages in the last minute; wait %d s.", 30, 12).RetryAfter(12))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "12" {
		t.Errorf("Retry-After %q", ra)
	}
	want := `{"error":{"code":"rate_limited","message":"You sent 30 messages in the last minute; wait 12 s.","retry_after_s":12}}` + "\n"
	if rec.Body.String() != want {
		t.Errorf("body %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	WriteError(rec, Errorf(CodeNotFound, "Message %d does not exist.", 7))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "" {
		t.Errorf("Retry-After %q on a 404", ra)
	}
	if strings.Contains(rec.Body.String(), "retry_after_s") {
		t.Errorf("body %s", rec.Body.String())
	}
}

func TestAPIErrorIsError(t *testing.T) {
	var err error = Errorf(CodeConflict, "serial:%s is claimed by %s.", "dut1", "claude-laptop")
	if err.Error() != "conflict: serial:dut1 is claimed by claude-laptop." {
		t.Errorf("got %q", err.Error())
	}
}
