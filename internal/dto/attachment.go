package dto

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Attachment types, the value of "type".
const (
	AttachmentImage = "image"
	AttachmentLog   = "log"
)

// Attachment is ImageAttachment or LogAttachment: exactly one is non-nil.
type Attachment struct {
	Image *ImageAttachment
	Log   *LogAttachment
}

// ImageOf wraps an image attachment.
func ImageOf(a ImageAttachment) Attachment { return Attachment{Image: &a} }

// LogOf wraps a log attachment.
func LogOf(a LogAttachment) Attachment { return Attachment{Log: &a} }

// MarshalJSON implements json.Marshaler.
func (a Attachment) MarshalJSON() ([]byte, error) {
	switch {
	case a.Image != nil && a.Log == nil:
		return a.Image.MarshalJSON()
	case a.Log != nil && a.Image == nil:
		return a.Log.MarshalJSON()
	}
	return nil, errors.New("attachment: exactly one of Image and Log must be set")
}

// UnmarshalJSON implements json.Unmarshaler.
func (a *Attachment) UnmarshalJSON(b []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return fmt.Errorf("attachment: %w", err)
	}
	*a = Attachment{}
	switch head.Type {
	case AttachmentImage:
		a.Image = new(ImageAttachment)
		return json.Unmarshal(b, a.Image)
	case AttachmentLog:
		a.Log = new(LogAttachment)
		return json.Unmarshal(b, a.Log)
	}
	return fmt.Errorf("attachment: unknown type %q", head.Type)
}

// ImageAttachment is an image of a message or a share.
type ImageAttachment struct {
	SHA256   string `json:"sha256"`
	Mime     string `json:"mime"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url,omitempty"`
	// Preview is false when browsers cannot show the format (HEIC).
	Preview bool `json:"preview"`
}

// MarshalJSON implements json.Marshaler, adding "type": "image".
func (a ImageAttachment) MarshalJSON() ([]byte, error) {
	type plain ImageAttachment
	return json.Marshal(struct {
		Type string `json:"type"`
		plain
	}{AttachmentImage, plain(a)})
}

// LogAttachment is a slice of one or more streams attached to a message.
type LogAttachment struct {
	Stream  string   `json:"stream,omitempty"`
	Channel string   `json:"channel,omitempty"`
	Streams []string `json:"streams"`
	FromSeq int64    `json:"from_seq"`
	// ToSeq is nil for an open-ended slice, and marshals as null.
	ToSeq       *int64 `json:"to_seq"`
	Grep        string `json:"grep,omitempty"`
	MinSeverity *int   `json:"min_severity,omitempty"`
	Title       string `json:"title,omitempty"`
	Lines       int    `json:"lines"`
	FirstTS     *Time  `json:"first_ts,omitempty"`
	LastTS      *Time  `json:"last_ts,omitempty"`
	Snapshot    bool   `json:"snapshot"`
	Expired     bool   `json:"expired"`
	// Hidden: the viewer may not read every stream, so no lines are shown.
	Hidden  bool    `json:"hidden,omitempty"`
	Preview []Entry `json:"preview"`
}

// MarshalJSON implements json.Marshaler, adding "type": "log".
func (a LogAttachment) MarshalJSON() ([]byte, error) {
	type plain LogAttachment
	p := plain(a)
	p.Streams = nonNil(p.Streams)
	p.Preview = nonNil(p.Preview)
	return json.Marshal(struct {
		Type string `json:"type"`
		plain
	}{AttachmentLog, p})
}
