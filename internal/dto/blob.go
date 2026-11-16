package dto

// Blob is a stored file and its derived image variants.
type Blob struct {
	SHA256      string `json:"sha256"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	CreatedAt   Time   `json:"created_at"`
	AgentSHA256 string `json:"agent_sha256,omitempty"`
	ThumbSHA256 string `json:"thumb_sha256,omitempty"`
}
