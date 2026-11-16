// Package dto holds every JSON type of the API (CONTRACTS.md sections 3
// and 4), shared by the web API, SSE, MCP structured output and the
// fixture generator.
package dto

import (
	"encoding/json"
	"fmt"
	"time"
)

// Time is a timestamp that marshals as RFC 3339 with nanoseconds, in UTC.
type Time struct{ time.Time }

// TimeOf wraps t.
func TimeOf(t time.Time) Time { return Time{t} }

// TimePtr wraps t, returning nil for the zero time, for optional fields.
func TimePtr(t time.Time) *Time {
	if t.IsZero() {
		return nil
	}
	return &Time{t}
}

// MarshalJSON implements json.Marshaler.
func (t Time) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Format(time.RFC3339Nano))
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Time) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	t.Time = v.UTC()
	return nil
}

// nonNil returns s, or an empty slice when s is nil, so required arrays
// marshal as [] and never as null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
