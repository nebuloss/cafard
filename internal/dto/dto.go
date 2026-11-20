package dto

import (
	"encoding/json"
)

// PrincipalKind is who a principal is.
type PrincipalKind string

const (
	KindAgent  PrincipalKind = "agent"
	KindHuman  PrincipalKind = "human"
	KindDevice PrincipalKind = "device"
)

// Scope is a permission a token or session carries.
type Scope string

const (
	ScopeIngest      Scope = "ingest"
	ScopeRead        Scope = "read"
	ScopeMessage     Scope = "message"
	ScopeSerialWrite Scope = "serial_write"
	ScopeAdmin       Scope = "admin"
)

// Theme is the viewer's colour scheme preference.
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Me is the authenticated caller.
type Me struct {
	Name      string        `json:"name"`
	Kind      PrincipalKind `json:"kind"`
	Scopes    []Scope       `json:"scopes"`
	SessionID int64         `json:"session_id,omitempty"`
	Theme     Theme         `json:"theme"`
}

// MarshalJSON implements json.Marshaler.
func (m Me) MarshalJSON() ([]byte, error) {
	type plain Me
	p := plain(m)
	p.Scopes = nonNil(p.Scopes)
	return json.Marshal(p)
}

// Principal is someone who can be messaged.
type Principal struct {
	Name     string        `json:"name"`
	Kind     PrincipalKind `json:"kind"`
	LastSeen *Time         `json:"last_seen,omitempty"`
}

// Member is one principal who can read a channel, as the members panel
// shows it (GET /api/channels/{name}/members): only those the viewer may
// know about (list_principals' rule), the viewer included.
type Member struct {
	Name      string        `json:"name"`
	Kind      PrincipalKind `json:"kind"`
	Online    bool          `json:"online"` // a live feed open, or a request in the last 5 minutes
	LastSeen  *Time         `json:"last_seen,omitempty"`
	Following bool          `json:"following"`
	CanDM     bool          `json:"can_dm"` // the viewer may open a direct conversation with them
}

// MemberList answers GET /api/channels/{name}/members: people, then
// agents, then devices, each online first, then by name.
type MemberList struct {
	Members []Member `json:"members"`
}

// MarshalJSON implements json.Marshaler.
func (m MemberList) MarshalJSON() ([]byte, error) {
	type plain MemberList
	p := plain(m)
	p.Members = nonNil(p.Members)
	return json.Marshal(p)
}

// EntryKind is what an entry is.
type EntryKind string

const (
	EntryLine    EntryKind = "line"
	EntryMarker  EntryKind = "marker"
	EntryMessage EntryKind = "message"
)

// Recipient value addressing every human.
const RecipientHumans = "@humans"

// Entry is one item of a timeline, as the API shows it.
type Entry struct {
	Seq      int64     `json:"seq"`
	TS       Time      `json:"ts"`
	SrcTS    *Time     `json:"src_ts,omitempty"`
	Stream   string    `json:"stream"`
	Kind     EntryKind `json:"kind"`
	Severity int       `json:"severity"`
	Host     string    `json:"host,omitempty"`
	App      string    `json:"app,omitempty"`
	Text     string    `json:"text"`
	// Meta is an object, kept raw so it survives untouched.
	Meta json.RawMessage `json:"meta,omitempty"`

	// Messages only.
	Chat        string       `json:"chat,omitempty"`
	Sender      *Sender      `json:"sender,omitempty"`
	Recipient   string       `json:"recipient,omitempty"`
	ReplyTo     int64        `json:"reply_to,omitempty"`
	Segments    []Segment    `json:"segments,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	SeenBy      []string     `json:"seen_by,omitempty"`
	Question    *Question    `json:"question,omitempty"`
	// Section is the section of a gallery item.
	Section string `json:"section,omitempty"`
	// GalleryRefs are the gallery items and sections the message points
	// at, as the viewer may see them.
	GalleryRefs []GalleryRef `json:"gallery_refs,omitempty"`

	// Event marks a line or marker of a bound stream shown as a timeline row.
	Event bool `json:"event,omitempty"`
}

// Sender is the author of a message.
type Sender struct {
	Name string        `json:"name"`
	Kind PrincipalKind `json:"kind"`
}

// Question is a message that asks for an answer.
type Question struct {
	Choices []string `json:"choices"`
	Open    bool     `json:"open"`
	Answer  *Answer  `json:"answer,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (q Question) MarshalJSON() ([]byte, error) {
	type plain Question
	p := plain(q)
	p.Choices = nonNil(p.Choices)
	return json.Marshal(p)
}

// Answer is the reply that closed a question.
type Answer struct {
	By     string `json:"by"`
	Choice string `json:"choice,omitempty"`
	Seq    int64  `json:"seq"`
}

// SegmentType is the kind of a parsed text segment.
type SegmentType string

const (
	SegText      SegmentType = "text"
	SegCode      SegmentType = "code"
	SegCodeblock SegmentType = "codeblock"
	SegLink      SegmentType = "link"
)

// Segment is one piece of a message's parsed text. Lang is for codeblock
// only, Href for link only (http or https, validated by the server).
type Segment struct {
	T    SegmentType `json:"t"`
	V    string      `json:"v"`
	Lang string      `json:"lang,omitempty"`
	Href string      `json:"href,omitempty"`
}

// ChatKind is channel or direct.
type ChatKind string

const (
	ChatChannel ChatKind = "channel"
	ChatDirect  ChatKind = "direct"
)

// ChannelKind is how a channel presents its messages: a conversation, or
// a gallery of images with titles.
type ChannelKind string

const (
	ChannelChat    ChannelKind = "chat"
	ChannelGallery ChannelKind = "gallery"
)

// Surface is which entries of bound streams a channel shows as event rows.
type Surface string

const (
	SurfaceNone    Surface = "none"
	SurfaceMarkers Surface = "markers"
	SurfaceErrors  Surface = "errors"
)

// ChatSource says where a channel was defined.
type ChatSource string

const (
	SourceRuntime ChatSource = "runtime"
	SourceConfig  ChatSource = "config"
)

// Chat is a channel or a direct conversation, from the viewer's side.
type Chat struct {
	ID            string       `json:"id"`
	Kind          ChatKind     `json:"kind"`
	Project       string       `json:"project,omitempty"` // channels only
	ProjectTitle  string       `json:"project_title,omitempty"`
	Name          string       `json:"name"`                   // inside its project for a channel
	ChannelKind   ChannelKind  `json:"channel_kind,omitempty"` // channels only
	Title         string       `json:"title,omitempty"`
	Topic         string       `json:"topic,omitempty"`
	Peer          *Principal   `json:"peer,omitempty"`
	Patterns      []string     `json:"patterns"`
	Streams       []StreamRef  `json:"streams"`
	Surface       Surface      `json:"surface"`
	Followed      bool         `json:"followed"`
	Pinned        bool         `json:"pinned"`
	Muted         bool         `json:"muted"`
	Archived      bool         `json:"archived"`
	Source        ChatSource   `json:"source"`
	CreatedBy     string       `json:"created_by,omitempty"`
	CanManage     bool         `json:"can_manage"`
	Unread        int          `json:"unread"`
	OpenQuestions int          `json:"open_questions"`
	LastActivity  *Time        `json:"last_activity,omitempty"`
	Preview       *ChatPreview `json:"preview,omitempty"`
	// Sections of a gallery, newest first; only in list_channels, where an
	// agent picks one.
	Sections []GallerySection `json:"sections,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (c Chat) MarshalJSON() ([]byte, error) {
	type plain Chat
	p := plain(c)
	p.Patterns = nonNil(p.Patterns)
	p.Streams = nonNil(p.Streams)
	return json.Marshal(p)
}

// ChatPreview is the latest message or event row of a chat.
type ChatPreview struct {
	Seq    int64  `json:"seq"`
	Sender string `json:"sender,omitempty"`
	Text   string `json:"text"`
	Event  bool   `json:"event"`
}

// StreamState is a source's connection state.
type StreamState string

const (
	StreamConnected StreamState = "connected"
	StreamIdle      StreamState = "idle"
	StreamDown      StreamState = "down"
)

// StreamRef is a stream a channel's patterns match.
type StreamRef struct {
	ID     string      `json:"id"`
	Status StreamState `json:"status"`
}

// Stream is a source and its live status.
type Stream struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Transport    string            `json:"transport"`
	Labels       map[string]string `json:"labels"`
	Project      string            `json:"project,omitempty"` // "lab" for serial:lab/dut1
	Sink         string            `json:"sink,omitempty"`    // "dut1"
	Status       StreamState       `json:"status"`
	LastSeq      int64             `json:"last_seq"`
	LastActivity *Time             `json:"last_activity,omitempty"`
	RatePerMin   float64           `json:"rate_per_min"`
	Dropped      int64             `json:"dropped"`
	Claim        *Claim            `json:"claim,omitempty"`
	LineState    *LineState        `json:"line_state,omitempty"`
	LastLine     string            `json:"last_line,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (s Stream) MarshalJSON() ([]byte, error) {
	type plain Stream
	p := plain(s)
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	return json.Marshal(p)
}

// StreamList answers GET /api/streams. Unrouted, for the admin only,
// counts by transport the log entries that reached no sink since start.
type StreamList struct {
	Streams  []Stream         `json:"streams"`
	Unrouted map[string]int64 `json:"unrouted,omitempty"`
}

// Claim is an exclusive hold on a stream.
type Claim struct {
	Principal string `json:"principal"`
	Note      string `json:"note,omitempty"`
	ExpiresAt Time   `json:"expires_at"`
}

// LineState is a serial port's modem lines; a nil field is unknown.
type LineState struct {
	DTR *bool `json:"dtr,omitempty"`
	RTS *bool `json:"rts,omitempty"`
	CTS *bool `json:"cts,omitempty"`
	DCD *bool `json:"dcd,omitempty"`
}

// Overview is the home page.
type Overview struct {
	Questions []Entry `json:"questions"`
	Direct    []Chat  `json:"direct"`
	Recent    []Entry `json:"recent"`
}

// MarshalJSON implements json.Marshaler.
func (o Overview) MarshalJSON() ([]byte, error) {
	type plain Overview
	p := plain(o)
	p.Questions = nonNil(p.Questions)
	p.Direct = nonNil(p.Direct)
	p.Recent = nonNil(p.Recent)
	return json.Marshal(p)
}

// Counts are the badges.
type Counts struct {
	OpenQuestions int         `json:"open_questions"`
	UnreadDirect  int         `json:"unread_direct"`
	SourcesDown   int         `json:"sources_down"`
	Chats         []ChatCount `json:"chats"`
}

// MarshalJSON implements json.Marshaler.
func (c Counts) MarshalJSON() ([]byte, error) {
	type plain Counts
	p := plain(c)
	p.Chats = nonNil(p.Chats)
	return json.Marshal(p)
}

// ChatCount is one chat's badges.
type ChatCount struct {
	ID            string `json:"id"`
	Unread        int    `json:"unread"`
	OpenQuestions int    `json:"open_questions"`
}

// ChannelCreate is the body that creates a channel.
type ChannelCreate struct {
	Name     string      `json:"name"`              // "dut1", or "lab/dut1"
	Project  string      `json:"project,omitempty"` // needed when the creator may make channels in several projects
	Title    string      `json:"title,omitempty"`
	Topic    string      `json:"topic,omitempty"`
	Patterns []string    `json:"patterns,omitempty"`
	Surface  Surface     `json:"surface,omitempty"`
	Kind     ChannelKind `json:"kind,omitempty"` // default chat
	// Private makes the channel in the creator's personal space, readable
	// by the creator and Members only (no Project, no Patterns).
	Private bool     `json:"private,omitempty"`
	Members []string `json:"members,omitempty"` // principals the creator may reach
}

// ChannelUpdate is the body that changes a channel. Title, Topic and
// Archived are pointers so that "clear" and "false" differ from "unchanged".
type ChannelUpdate struct {
	Rename         string      `json:"rename,omitempty"`
	Title          *string     `json:"title,omitempty"`
	Topic          *string     `json:"topic,omitempty"`
	AddPatterns    []string    `json:"add_patterns,omitempty"`
	RemovePatterns []string    `json:"remove_patterns,omitempty"`
	Surface        Surface     `json:"surface,omitempty"`
	Archived       *bool       `json:"archived,omitempty"`
	Kind           ChannelKind `json:"kind,omitempty"`
}

// Token is an API token, without its secret.
type Token struct {
	ID        int64         `json:"id"`
	Label     string        `json:"label"`
	Principal string        `json:"principal"`
	Kind      PrincipalKind `json:"kind"`
	Scopes    []Scope       `json:"scopes"`
	CreatedAt Time          `json:"created_at"`
	LastUsed  *Time         `json:"last_used,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (t Token) MarshalJSON() ([]byte, error) {
	p := tokenFields(t)
	p.Scopes = nonNil(p.Scopes)
	return json.Marshal(p)
}

// tokenFields is Token without its methods, so TokenCreated can inline it.
type tokenFields Token

// TokenCreated is a new token with its secret, shown once.
type TokenCreated struct {
	Token
	Secret   string    `json:"token"`
	Snippets []Snippet `json:"snippets"`
}

// MarshalJSON implements json.Marshaler. It is required: the promoted
// Token.MarshalJSON would otherwise drop Secret and Snippets.
func (t TokenCreated) MarshalJSON() ([]byte, error) {
	p := struct {
		tokenFields
		Secret   string    `json:"token"`
		Snippets []Snippet `json:"snippets"`
	}{tokenFields(t.Token), t.Secret, nonNil(t.Snippets)}
	p.Scopes = nonNil(p.Scopes)
	return json.Marshal(p)
}

// SnippetLanguage is the syntax of a setup snippet.
type SnippetLanguage string

const (
	LangShell SnippetLanguage = "shell"
	LangTOML  SnippetLanguage = "toml"
	LangJSON  SnippetLanguage = "json"
	LangEnv   SnippetLanguage = "env"
)

// Snippet is a ready-to-paste configuration using a new token.
type Snippet struct {
	Label    string          `json:"label"`
	Language SnippetLanguage `json:"language"`
	Text     string          `json:"text"`
}

// Session is a browser session.
type Session struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	Principal string `json:"principal"`
	UserAgent string `json:"user_agent,omitempty"`
	CreatedAt Time   `json:"created_at"`
	ExpiresAt Time   `json:"expires_at"`
	Current   bool   `json:"current"`
}

// Pairing is a pending device pairing.
type Pairing struct {
	ID        int64    `json:"id"`
	URL       string   `json:"url"`
	QRURL     string   `json:"qr_url"`
	ExpiresAt Time     `json:"expires_at"`
	Principal string   `json:"principal,omitempty"`
	NewName   string   `json:"new_name,omitempty"`
	Grants    []string `json:"grants,omitempty"` // "lab:dut1=read,message"
}

// Share is content staged by the share target, waiting to be sent.
type Share struct {
	ID        string            `json:"id"`
	Text      string            `json:"text,omitempty"`
	Files     []ImageAttachment `json:"files"`
	ExpiresAt Time              `json:"expires_at"`
}

// MarshalJSON implements json.Marshaler.
func (s Share) MarshalJSON() ([]byte, error) {
	type plain Share
	p := plain(s)
	p.Files = nonNil(p.Files)
	return json.Marshal(p)
}

// LogAccepted answers POST /api/log.
type LogAccepted struct {
	Accepted int   `json:"accepted"`
	LastSeq  int64 `json:"last_seq"`
	// Dropped counts the lines that reached no sink and were not stored.
	Dropped int `json:"dropped,omitempty"`
}

// Project is a project as its viewer reaches it.
type Project struct {
	Name      string           `json:"name"`
	Title     string           `json:"title,omitempty"`
	CreatedAt Time             `json:"created_at"`
	Scopes    []Scope          `json:"scopes"`              // the viewer's project-wide scopes
	Channels  []string         `json:"channels"`            // chat ids the viewer can read
	Galleries []string         `json:"galleries,omitempty"` // those of Channels that are galleries
	Sinks     []string         `json:"sinks"`               // when the viewer may read the project's logs
	Retention ProjectRetention `json:"retention"`
}

// ProjectRetention is a project's limits; 0 means none.
type ProjectRetention struct {
	LinesMaxAgeS    int64 `json:"lines_max_age_s"`
	LinesMaxBytes   int64 `json:"lines_max_bytes"`
	MessagesMaxAgeS int64 `json:"messages_max_age_s"`
	BlobsMaxBytes   int64 `json:"blobs_max_bytes"`
}

// MarshalJSON implements json.Marshaler.
func (p Project) MarshalJSON() ([]byte, error) {
	type plain Project
	q := plain(p)
	q.Scopes, q.Channels, q.Sinks = nonNil(q.Scopes), nonNil(q.Channels), nonNil(q.Sinks)
	return json.Marshal(q)
}
