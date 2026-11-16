package dto

import "encoding/json"

// Page is one page of a list. Zero cursors are left out: seqs start at 1.
type Page[T any] struct {
	Items         []T   `json:"items"`
	HasMore       bool  `json:"has_more"`
	NextSeq       int64 `json:"next_seq,omitempty"`
	NextBeforeSeq int64 `json:"next_before_seq,omitempty"`
	FocusSeq      int64 `json:"focus_seq,omitempty"`
}

// pageJSON is Page without its methods.
type pageJSON[T any] Page[T]

// MarshalJSON implements json.Marshaler; nil Items marshal as [].
func (p Page[T]) MarshalJSON() ([]byte, error) {
	q := pageJSON[T](p)
	q.Items = nonNil(q.Items)
	return json.Marshal(q)
}
