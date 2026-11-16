// Package view turns store rows into the DTOs and strings the web pages,
// the JSON API and SSE all show, so the three cannot disagree.
package view

import (
	"context"
	"hash/fnv"
	"sync"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// Kinds remembers the kind of each principal, for entry senders. Kinds do
// not change once a principal exists, so the cache is never invalidated.
type Kinds struct {
	tokens *auth.Tokens
	mu     sync.Mutex
	known  map[string]dto.PrincipalKind
}

// NewKinds reads kinds from tokens; tokens may be nil (every sender is then
// an agent).
func NewKinds(tokens *auth.Tokens) *Kinds {
	return &Kinds{tokens: tokens, known: map[string]dto.PrincipalKind{}}
}

// Of is the kind of name, "agent" when unknown.
func (k *Kinds) Of(ctx context.Context, name string) dto.PrincipalKind {
	k.mu.Lock()
	kind, ok := k.known[name]
	k.mu.Unlock()
	if ok {
		return kind
	}
	kind = dto.KindAgent
	if k.tokens != nil {
		if p, err := k.tokens.Principal(ctx, name); err == nil {
			kind = dto.PrincipalKind(p.Kind)
			k.mu.Lock()
			k.known[name] = kind
			k.mu.Unlock()
		}
	}
	return kind
}

// Entry is the DTO of a line or marker. Messages get their text as one
// text segment here; messaging fills in the rest when it lands.
func Entry(ctx context.Context, e store.Entry, kinds *Kinds) dto.Entry {
	out := dto.Entry{
		Seq:      e.Seq,
		TS:       dto.TimeOf(e.TS),
		Stream:   e.Stream,
		Kind:     dto.EntryKind(e.Kind),
		Severity: e.Severity,
		Host:     e.Host,
		App:      e.App,
		Text:     e.Text,
		Meta:     e.Meta,
		ReplyTo:  e.ReplyTo,
	}
	if e.SrcTS != nil {
		out.SrcTS = dto.TimePtr(*e.SrcTS)
	}
	if e.Sender != "" {
		kind := dto.KindAgent
		if kinds != nil {
			kind = kinds.Of(ctx, e.Sender)
		}
		out.Sender = &dto.Sender{Name: e.Sender, Kind: kind}
	}
	if e.Kind == store.KindMessage {
		out.Recipient = e.Recipient
		out.Segments = []dto.Segment{{T: dto.SegText, V: e.Text}}
	}
	return out
}

// Entries converts a list.
func Entries(ctx context.Context, es []store.Entry, kinds *Kinds) []dto.Entry {
	out := make([]dto.Entry, len(es))
	for i, e := range es {
		out[i] = Entry(ctx, e, kinds)
	}
	return out
}

var sevLetters = [...]string{"M", "A", "C", "E", "W", "N", "I", "D"}

// SeverityLetter is the one-letter column of a log row; meaning never
// depends on colour alone.
func SeverityLetter(sev int) string {
	if sev >= 0 && sev < len(sevLetters) {
		return sevLetters[sev]
	}
	return "?"
}

// SeverityName is the long name, for titles.
func SeverityName(sev int) string { return textclean.SeverityName(sev) }

// Tone is the colour of a stream tag: FNV-1a of the id into 8 tones.
// src/render/StreamTone.ts computes the same value; a test on each side
// pins a few ids.
func Tone(stream string) int {
	h := fnv.New32a()
	h.Write([]byte(stream))
	return int(h.Sum32() % 8)
}

// Streams is status.Streams, for the pages and the API.
func Streams(ctx context.Context, st *store.Store, reg status.Registry) ([]dto.Stream, error) {
	return status.Streams(ctx, st, reg)
}
