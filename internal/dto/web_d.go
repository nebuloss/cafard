package dto

// Request and answer bodies of the track D chat API (CONTRACTS.md 2.3).

// ChatPrefs is the body of PUT /api/chats/{chat}/prefs. A nil field is
// left as it is. Followed applies to channels only.
type ChatPrefs struct {
	Pinned   *bool `json:"pinned,omitempty"`
	Muted    *bool `json:"muted,omitempty"`
	Followed *bool `json:"followed,omitempty"`
}

// ChatRead is the body of POST /api/chats/{chat}/read: ack every message
// of the chat up to a seq ("mark read").
type ChatRead struct {
	UpToSeq int64 `json:"up_to_seq"`
}

// AckRequest is the body of POST /api/acks.
type AckRequest struct {
	Seqs []int64 `json:"seqs"`
}

// Acked answers both: how many messages were newly acked.
type Acked struct {
	Acked int `json:"acked"`
}
