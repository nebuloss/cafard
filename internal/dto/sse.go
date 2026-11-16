package dto

// SSE payloads that have no DTO of their own (CONTRACTS.md section 4).

// Seen is the "seen" event: someone acked a message.
type Seen struct {
	Seq int64  `json:"seq"`
	By  string `json:"by"`
}

// Answered is the "answered" event: a question got its answer.
type Answered struct {
	Seq    int64  `json:"seq"`
	Answer Answer `json:"answer"`
}

// Reset is the "reset" event: the client must reload and reconnect.
type Reset struct {
	Reason string `json:"reason"`
}
