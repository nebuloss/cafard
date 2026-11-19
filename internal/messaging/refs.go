package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Gallery refs (PLAN section 6.4): a chat message points at gallery
// items or sections without copying them, and each reader sees only what
// it may read.

const (
	MaxGalleryRefs = 10
	// refStrip is how many images a section ref shows.
	refStrip = 4
)

// ItemRef and SectionRef are the forms an agent writes in a message.
func ItemRef(gallery string, seq int64) string { return fmt.Sprintf("[[%s#%d]]", gallery, seq) }

func SectionRef(gallery, section string) string { return "[[" + gallery + "|" + section + "]]" }

// GalleriesOf are the galleries of project p may read.
func (s *Service) GalleriesOf(ctx context.Context, p auth.Principal, project string) ([]Channel, error) {
	chs, err := s.Channels(ctx, p, false)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(chs, func(c Channel) bool { return !c.Gallery() || c.Project() != project }), nil
}

// refMeta is a message's pointer at a gallery item or section, as stored
// in its meta ("refs"). The gallery is kept with an item ref, so access
// can be decided once the item is gone.
type refMeta struct {
	Gallery string `json:"gallery"`
	Seq     int64  `json:"seq,omitempty"`
	Section string `json:"section,omitempty"`
}

var (
	refRE = regexp.MustCompile(`\[\[([a-z0-9][a-z0-9._-]{0,39})/([a-z0-9][a-z0-9-]{0,39})(?:#([0-9]{1,18})|\|([^\]\[|\n]{1,80}))\]\]`)
	// A link to a gallery page, with or without the server's address.
	galleryLinkRE = regexp.MustCompile(`(?:https?://[^\s/]+)?/g/([a-z0-9][a-z0-9._-]{0,39})/([a-z0-9][a-z0-9-]{0,39})(?:/([0-9]{1,18})|\?section=([^\s#&]+))`)
)

// refMatch is a ref or a gallery link written in a message, and where.
type refMatch struct {
	start, end int
	ref        refMeta
}

// refMatches are the gallery refs and links written in text, in no
// particular order.
func refMatches(text string) []refMatch {
	var out []refMatch
	add := func(loc []int, project, gallery, seq, section string) {
		r := refMeta{Gallery: project + "/" + gallery}
		if seq != "" {
			r.Seq, _ = strconv.ParseInt(seq, 10, 64)
		} else {
			r.Section = strings.TrimSpace(section)
		}
		if r.Seq > 0 || r.Section != "" {
			out = append(out, refMatch{start: loc[0], end: loc[1], ref: r})
		}
	}
	group := func(m []int, i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return text[m[2*i]:m[2*i+1]]
	}
	for _, m := range refRE.FindAllStringSubmatchIndex(text, -1) {
		add(m, group(m, 1), group(m, 2), group(m, 3), group(m, 4))
	}
	for _, m := range galleryLinkRE.FindAllStringSubmatchIndex(text, -1) {
		sec := group(m, 4)
		if u, err := url.QueryUnescape(sec); err == nil {
			sec = u
		}
		add(m, group(m, 1), group(m, 2), group(m, 3), sec)
	}
	return out
}

// textRefs are the distinct gallery refs and links written in a message.
func textRefs(text string) []refMeta {
	var out []refMeta
	for _, m := range refMatches(text) {
		if !slices.Contains(out, m.ref) {
			out = append(out, m.ref)
		}
	}
	return out
}

var spaces = regexp.MustCompile(`[ \t]{2,}`)

// stripRefs removes from text the refs and links that became refs: the
// chat shows each as a card instead.
func stripRefs(text string, refs []refMeta) string {
	ms := slices.DeleteFunc(refMatches(text), func(m refMatch) bool { return !slices.Contains(refs, m.ref) })
	if len(ms) == 0 {
		return text
	}
	slices.SortFunc(ms, func(a, b refMatch) int { return a.start - b.start })
	var b strings.Builder
	at := 0
	for _, m := range ms {
		if m.start < at {
			continue // a link inside a ref, already removed
		}
		b.WriteString(text[at:m.start])
		at = m.end
	}
	b.WriteString(text[at:])
	return strings.TrimSpace(spaces.ReplaceAllString(b.String(), " "))
}

// withoutRefs is a message's meta without its refs: they name galleries
// and seqs a reader may not see, and reach readers resolved instead.
func withoutRefs(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	if _, ok := m["refs"]; !ok {
		return raw
	}
	delete(m, "refs")
	if len(m) == 0 {
		return nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// errBadRef is the one answer for a ref that cannot be used, whatever
// the reason, so it tells nothing about galleries the sender cannot see.
func errBadRef(seq int64, project string) error {
	return dto.Errorf(dto.CodeBadRequest, "gallery_refs: %d is not an image of a gallery of %s you can read.", seq, project)
}

// collectRefs checks the refs a message to channel ch (nil for a direct
// message) carries: the gallery_refs seqs, each an image item of a
// gallery of ch's project the sender reads, else an error; and the refs
// and links in its text, kept only when they pass the same checks.
func (s *Service) collectRefs(ctx context.Context, from auth.Principal, ch *Channel, text string, seqs []int64) ([]refMeta, error) {
	if len(seqs) > 0 && (ch == nil || ch.Gallery()) {
		return nil, dto.Errorf(dto.CodeBadRequest, "gallery_refs point at gallery images from a chat channel; this message goes to a direct conversation or a gallery.")
	}
	if ch == nil || ch.Gallery() {
		return nil, nil
	}
	if len(seqs) > MaxGalleryRefs {
		return nil, dto.Errorf(dto.CodeBadRequest, "A message carries at most %d gallery refs.", MaxGalleryRefs)
	}
	a := s.access(ctx, from)
	project := ch.Project()
	var out []refMeta
	for _, seq := range seqs {
		e, err := s.st.Get(ctx, seq)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, errBadRef(seq, project)
			}
			return nil, err
		}
		if _, ok := galleryItem(e); !ok || store.ProjectOf(e.Stream) != project || !a.Can(e.Stream, auth.ScopeRead) {
			return nil, errBadRef(seq, project)
		}
		g, err := s.channel(ctx, e.Stream)
		if err != nil || !g.Gallery() {
			return nil, errBadRef(seq, project)
		}
		r := refMeta{Gallery: g.Name, Seq: seq}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	for _, r := range textRefs(text) {
		if len(out) >= MaxGalleryRefs {
			break
		}
		if slices.Contains(out, r) || store.ProjectOf("chan:"+r.Gallery) != project || !a.Can("chan:"+r.Gallery, auth.ScopeRead) {
			continue
		}
		g, err := s.channel(ctx, r.Gallery)
		if err != nil || !g.Gallery() {
			continue
		}
		if r.Seq > 0 {
			e, err := s.st.Get(ctx, r.Seq)
			if _, ok := galleryItem(e); err != nil || !ok || e.Stream != g.Stream() {
				continue
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// refDTOs resolve a message's refs for viewer: what it may not read is
// "unavailable" with nothing else, what is gone is "gone".
func (s *Service) refDTOs(ctx context.Context, viewer auth.Principal, refs []refMeta) ([]dto.GalleryRef, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	a := s.access(ctx, viewer)
	out := make([]dto.GalleryRef, 0, len(refs))
	for _, r := range refs {
		if r.Seq > 0 {
			e, err := s.st.Get(ctx, r.Seq)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			stream := "chan:" + r.Gallery
			if err == nil {
				stream = e.Stream
			}
			if !a.Can(stream, auth.ScopeRead) {
				out = append(out, dto.GalleryRef{State: dto.RefUnavailable})
				continue
			}
			it, ok := galleryItem(e)
			if err != nil || !ok {
				out = append(out, dto.GalleryRef{State: dto.RefGone, Gallery: r.Gallery, Seq: r.Seq})
				continue
			}
			gallery := strings.TrimPrefix(e.Stream, "chan:")
			out = append(out, dto.GalleryRef{State: dto.RefOK, Gallery: gallery, Seq: r.Seq, Section: it.Section, Href: it.Href, Item: &it})
			continue
		}
		if !a.Can("chan:"+r.Gallery, auth.ScopeRead) {
			out = append(out, dto.GalleryRef{State: dto.RefUnavailable})
			continue
		}
		section := r.Section
		items, err := s.galleryItems(ctx, "chan:"+r.Gallery, &section, 0, refStrip)
		if err != nil {
			return nil, err
		}
		ref := dto.GalleryRef{State: dto.RefGone, Gallery: r.Gallery, Section: section}
		if len(items) > 0 {
			ref.State, ref.Href, ref.Items = dto.RefOK, GalleryHref(r.Gallery, &section), items
			if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM entries WHERE stream = ? AND `+galleryImage+` AND `+sectionOf+` = ?`,
				"chan:"+r.Gallery, section).Scan(&ref.Count); err != nil {
				return nil, err
			}
		}
		out = append(out, ref)
	}
	return out, nil
}

// GalleryImagesLabel is "[2 gallery images]", the preview of a message
// that only points at gallery images.
func GalleryImagesLabel(n int) string {
	if n == 1 {
		return "[1 gallery image]"
	}
	return fmt.Sprintf("[%d gallery images]", n)
}
