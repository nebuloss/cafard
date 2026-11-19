package messaging

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Gallery limits (PLAN section 6.3).
const (
	MaxSectionRunes = 80
	// GalleryPageSize is how many items a gallery page shows.
	GalleryPageSize = 60
	itemTitle       = 140
	maxSection      = 200 // sections listed per gallery
)

// galleryImage is the SQL condition of a gallery item: a message with an
// image. Other messages of a gallery (markers) are not items.
const galleryImage = `kind = 'message' AND EXISTS (SELECT 1 FROM json_each(coalesce(attachments, '[]')) WHERE json_extract(value, '$.type') = 'image')`

// sectionOf is the SQL expression of an entry's section.
const sectionOf = `coalesce(json_extract(meta, '$.section'), '')`

// NormalizeSection trims a section name and checks it: at most 80
// characters, no control characters, and no '[', ']' or '|', which the
// ref syntax uses.
func NormalizeSection(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxSectionRunes {
		return "", dto.Errorf(dto.CodeBadRequest, "A section name is at most %d characters.", MaxSectionRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '[' || r == ']' || r == '|' {
			return "", dto.Errorf(dto.CodeBadRequest, "A section name cannot hold control characters, '[', ']' or '|'.")
		}
	}
	return s, nil
}

// GalleryHref is the page of a gallery ("lab/shots"), on a section when
// section is not nil ("" is the unsorted items).
func GalleryHref(gallery string, section *string) string {
	project, local, _ := strings.Cut(gallery, "/")
	h := "/g/" + url.PathEscape(project) + "/" + url.PathEscape(local)
	if section != nil {
		h += "?" + url.Values{"section": {*section}}.Encode()
	}
	return h
}

// ItemHref is the stable page of one gallery item.
func ItemHref(gallery string, seq int64) string {
	return GalleryHref(gallery, nil) + "/" + strconv.FormatInt(seq, 10)
}

// galleryItem is e as a gallery item, if it is one: its first image,
// and the first line of its text as the title.
func galleryItem(e store.Entry) (dto.GalleryItem, bool) {
	if e.Kind != store.KindMessage || !strings.HasPrefix(e.Stream, "chan:") {
		return dto.GalleryItem{}, false
	}
	i := slices.IndexFunc(e.Attachments, func(a store.Attachment) bool { return a.Type == dto.AttachmentImage })
	if i < 0 {
		return dto.GalleryItem{}, false
	}
	gallery := strings.TrimPrefix(e.Stream, "chan:")
	return dto.GalleryItem{Seq: e.Seq, Chat: e.Stream, Title: previewText(e.Text, itemTitle), Section: readMeta(e).Section,
		Image: ImageDTO(e.Attachments[i]), Href: ItemHref(gallery, e.Seq)}, true
}

// Gallery is the gallery channel p names and may read.
func (s *Service) Gallery(ctx context.Context, p auth.Principal, name string) (Channel, error) {
	ch, err := s.Channel(ctx, p, name)
	if err != nil {
		return Channel{}, err
	}
	if !ch.Gallery() {
		return Channel{}, dto.Errorf(dto.CodeNotFound, "#%s is a chat channel, not a gallery.", ch.Name)
	}
	return ch, nil
}

// GallerySections are the sections of a gallery, the one with the newest
// image first; "" holds the items without a section.
func (s *Service) GallerySections(ctx context.Context, ch Channel) ([]dto.GallerySection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sectionOf+`, count(*), max(seq) FROM entries
		WHERE stream = ? AND `+galleryImage+` GROUP BY 1 ORDER BY 3 DESC LIMIT ?`, ch.Stream(), maxSection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dto.GallerySection{}
	for rows.Next() {
		var sec dto.GallerySection
		if err := rows.Scan(&sec.Name, &sec.Items, &sec.LastSeq); err != nil {
			return nil, err
		}
		name := sec.Name
		sec.Href = GalleryHref(ch.Name, &name)
		out = append(out, sec)
	}
	return out, rows.Err()
}

// GalleryPage is a page of a gallery's items, newest first, before
// beforeSeq (0 for the newest), in section when it is not nil.
func (s *Service) GalleryPage(ctx context.Context, ch Channel, section *string, beforeSeq int64, limit int) (dto.GalleryPage, error) {
	if limit <= 0 || limit > GalleryPageSize {
		limit = GalleryPageSize
	}
	out := dto.GalleryPage{Chat: ch.Stream(), Section: section}
	items, err := s.galleryItems(ctx, ch.Stream(), section, beforeSeq, limit+1)
	if err != nil {
		return out, err
	}
	if len(items) > limit {
		items = items[:limit]
		out.HasMore, out.NextBeforeSeq = true, items[limit-1].Seq
	}
	out.Items = items
	out.Sections, err = s.GallerySections(ctx, ch)
	return out, err
}

func (s *Service) galleryItems(ctx context.Context, stream string, section *string, beforeSeq int64, limit int) ([]dto.GalleryItem, error) {
	filter, name := 0, ""
	if section != nil {
		filter, name = 1, *section
	}
	seqs, err := store.QueryColumn[int64](ctx, s.db, `SELECT seq FROM entries WHERE stream = ? AND `+galleryImage+`
		AND (? = 0 OR seq < ?) AND (? = 0 OR `+sectionOf+` = ?) ORDER BY seq DESC LIMIT ?`,
		stream, beforeSeq, beforeSeq, filter, name, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dto.GalleryItem, 0, len(seqs))
	for _, seq := range seqs {
		e, err := s.st.Get(ctx, seq)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if it, ok := galleryItem(e); ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// GalleryItem is one item of ch, and its neighbours in its section:
// newer (0 when it is the newest) and older.
func (s *Service) GalleryItem(ctx context.Context, ch Channel, seq int64) (it dto.GalleryItem, newer, older int64, err error) {
	e, err := s.st.Get(ctx, seq)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = dto.Errorf(dto.CodeNotFound, "Gallery #%s has no item %d; retention may have removed it.", ch.Name, seq)
		}
		return it, 0, 0, err
	}
	it, ok := galleryItem(e)
	if !ok || e.Stream != ch.Stream() {
		return it, 0, 0, dto.Errorf(dto.CodeNotFound, "Gallery #%s has no item %d.", ch.Name, seq)
	}
	q := `SELECT seq FROM entries WHERE stream = ? AND ` + galleryImage + ` AND ` + sectionOf + ` = ? AND seq %s ? ORDER BY seq %s LIMIT 1`
	if n, err := store.QueryColumn[int64](ctx, s.db, fmt.Sprintf(q, ">", "ASC"), ch.Stream(), it.Section, seq); err == nil && len(n) > 0 {
		newer = n[0]
	}
	if n, err := store.QueryColumn[int64](ctx, s.db, fmt.Sprintf(q, "<", "DESC"), ch.Stream(), it.Section, seq); err == nil && len(n) > 0 {
		older = n[0]
	}
	return it, newer, older, nil
}
