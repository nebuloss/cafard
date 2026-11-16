package view

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/dto"
)

// ShareTTL is how long a staged share waits to be sent (CONTRACTS 2.4).
const ShareTTL = time.Hour

// Shares holds what the share target staged, in memory: a restart loses
// them, which is acceptable. The blobs themselves are on disk, kept by
// the blob janitor for an hour like any unreferenced upload.
type Shares struct {
	clock clock.Clock
	mu    sync.Mutex
	m     map[string]Share
}

// Share is one staged share.
type Share struct {
	ID        string
	Principal string
	Text      string
	Blobs     []dto.Blob
	Expires   time.Time
}

// NewShares makes an empty store.
func NewShares(c clock.Clock) *Shares { return &Shares{clock: c, m: map[string]Share{}} }

// Put stages text and blobs for principal and returns the share.
func (s *Shares) Put(principal, text string, blobs []dto.Blob) Share {
	var b [12]byte
	_, _ = rand.Read(b[:])
	sh := Share{ID: "shr_" + hex.EncodeToString(b[:]), Principal: principal, Text: text, Blobs: slices.Clone(blobs),
		Expires: s.clock.Now().Add(ShareTTL)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	s.m[sh.ID] = sh
	return sh
}

func (s *Shares) expire() {
	now := s.clock.Now()
	for id, sh := range s.m {
		if !now.Before(sh.Expires) {
			delete(s.m, id)
		}
	}
}

// Get returns principal's share id. Someone else's share does not exist.
func (s *Shares) Get(principal, id string) (Share, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	sh, ok := s.m[id]
	if !ok || sh.Principal != principal {
		return Share{}, false
	}
	return sh, true
}

// Lookup is the messaging staged lookup: the blobs of p's share id.
func (s *Shares) Lookup(_ context.Context, p auth.Principal, id string) ([]dto.Blob, error) {
	sh, ok := s.Get(p.Name, id)
	if !ok {
		return nil, dto.Errorf(dto.CodeNotFound, "The shared files are gone (they wait 1 hour, and a restart loses them). Share them again.")
	}
	return sh.Blobs, nil
}

// Visible reports whether sha is in one of p's shares, so the drop page
// can show the staged images before any message references them.
func (s *Shares) Visible(_ context.Context, sha string, p auth.Principal) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	for _, sh := range s.m {
		if sh.Principal != p.Name {
			continue
		}
		for _, b := range sh.Blobs {
			if b.SHA256 == sha {
				return true
			}
		}
	}
	return false
}

// DTO is the share as GET /api/shares/{id} answers it.
func (sh Share) DTO() dto.Share {
	out := dto.Share{ID: sh.ID, Text: sh.Text, ExpiresAt: dto.TimeOf(sh.Expires)}
	for _, b := range sh.Blobs {
		img := dto.ImageAttachment{SHA256: b.SHA256, Mime: b.Mime, Width: b.Width, Height: b.Height,
			URL: "/blob/" + b.SHA256, Preview: b.Mime != "image/heic"}
		if img.Preview {
			img.ThumbURL = img.URL + "?v=thumb"
		}
		out.Files = append(out.Files, img)
	}
	return out
}
