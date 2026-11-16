package dto

// SendRequest is a message to send, as MCP send takes it. POST
// /api/messages builds one from its form fields.
type SendRequest struct {
	Text    string        `json:"text"`
	To      string        `json:"to,omitempty"`
	Channel string        `json:"channel,omitempty"`
	ReplyTo int64         `json:"reply_to,omitempty"`
	Images  []ImageUpload `json:"images,omitempty"`
	Logs    []LogRequest  `json:"logs,omitempty"`
	Choice  string        `json:"choice,omitempty"`
	Staged  string        `json:"staged,omitempty"`
}

// ImageUpload is an inline image of a SendRequest.
type ImageUpload struct {
	Data string `json:"data"` // base64
	Mime string `json:"mime"`
}

// LogRequest asks for a log attachment. Exactly one of Stream and Channel
// is set; a nil ToSeq means open-ended.
type LogRequest struct {
	Stream      string `json:"stream,omitempty"`
	Channel     string `json:"channel,omitempty"`
	FromSeq     int64  `json:"from_seq"`
	ToSeq       *int64 `json:"to_seq,omitempty"`
	Grep        string `json:"grep,omitempty"`
	MinSeverity *int   `json:"min_severity,omitempty"`
	Title       string `json:"title,omitempty"`
}
