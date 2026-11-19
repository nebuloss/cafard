package pages

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Galleries (PLAN 9.6.3, "Galleries"): a gallery channel's images with
// their titles, grouped by section. Every tile is a link to the item's
// own page, so the grid works without the bundle; the bundle opens the
// viewer instead and adds new items live.

func (s *Site) mountGalleries(mux *http.ServeMux) {
	mux.HandleFunc("GET /g/{project}/{name}", s.Auth(auth.ScopeRead, s.galleryPage))
	mux.HandleFunc("GET /g/{project}/{name}/{seq}", s.Auth(auth.ScopeRead, s.itemPage))
}

// GalleryItemView is partials/gallery_item.html (tpl-gallery-item).
type GalleryItemView struct {
	Seq                    int64
	Href, Src, Full, Title string
	Alt                    string
	Width, Height          int
	NoPreview              bool
}

// GalleryItem is the tile of an item.
func GalleryItem(it dto.GalleryItem) GalleryItemView {
	v := GalleryItemView{Seq: it.Seq, Href: it.Href, Src: it.Image.ThumbURL, Full: it.Image.URL, Title: it.Title,
		Alt: it.Title, Width: it.Image.Width, Height: it.Image.Height}
	if v.Alt == "" {
		v.Alt = "Screenshot " + strconv.FormatInt(it.Seq, 10)
	}
	if !it.Image.Preview || v.Src == "" {
		v.NoPreview, v.Src = true, ""
	}
	return v
}

// galleryGroup is the items of one section on a gallery page.
type galleryGroup struct {
	Name, Label string
	Items       []GalleryItemView
}

type sectionLink struct {
	Label, Href string
	Items       int
	Active      bool
}

type galleryData struct {
	Name, Title, Chat, ChatHref, AllHref string
	Section                              string // the filter, "" with Filtered false for all
	Filtered                             bool
	Sections                             []sectionLink
	Groups                               []galleryGroup
	Older, Newer                         string
	Empty                                bool
	// The newest page follows the feed from NextSeq, read before the items
	// so that none arrives unseen in between.
	Live    bool
	NextSeq int64
}

// sectionLabel is a section's heading: "Unsorted" for the items without.
func sectionLabel(name string) string {
	if name == "" {
		return "Unsorted"
	}
	return name
}

func (s *Site) gallery(w http.ResponseWriter, r *http.Request, p auth.Principal) (messaging.Channel, bool) {
	ch, err := s.chats.svc.Gallery(r.Context(), p, r.PathValue("project")+"/"+r.PathValue("name"))
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		s.Fail(w, r, &p, http.StatusNotFound, "No such gallery", apiErr.Message)
		return ch, false
	}
	if err != nil {
		s.Oops(w, r, &p, err)
		return ch, false
	}
	return ch, true
}

func (s *Site) galleryPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ch, ok := s.gallery(w, r, p)
	if !ok {
		return
	}
	q := r.URL.Query()
	var section *string
	if q.Has("section") {
		sec := q.Get("section")
		section = &sec
	}
	before, _ := strconv.ParseInt(q.Get("before_seq"), 10, 64)
	last, err := s.d.Store.LastSeq(r.Context())
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	page, err := s.chats.svc.GalleryPage(r.Context(), ch, section, before, messaging.GalleryPageSize)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	base := messaging.GalleryHref(ch.Name, section)
	data := galleryData{Name: ch.Name, Title: ch.Title, Chat: ch.Stream(), ChatHref: view.ChatHref(ch.Stream()),
		AllHref: messaging.GalleryHref(ch.Name, nil), Filtered: section != nil, Empty: len(page.Items) == 0,
		Live: before <= 0, NextSeq: last + 1}
	if section != nil {
		data.Section = *section
	}
	for _, sec := range page.Sections {
		data.Sections = append(data.Sections, sectionLink{Label: sectionLabel(sec.Name), Href: sec.Href, Items: sec.Items,
			Active: section != nil && *section == sec.Name})
	}
	// Items come newest first, so a section's first item places it.
	at := map[string]int{}
	for _, it := range page.Items {
		i, ok := at[it.Section]
		if !ok {
			i = len(data.Groups)
			at[it.Section] = i
			data.Groups = append(data.Groups, galleryGroup{Name: it.Section, Label: sectionLabel(it.Section)})
		}
		data.Groups[i].Items = append(data.Groups[i].Items, GalleryItem(it))
	}
	if page.HasMore {
		data.Older = withQuery(base, url.Values{"before_seq": {strconv.FormatInt(page.NextBeforeSeq, 10)}})
	}
	if before > 0 {
		data.Newer = base
	}
	title := "#" + ch.Name
	l := s.Shell(r, &p, title, "gallery", "chats")
	s.Render(w, http.StatusOK, "gallery", l, galleryTwins, data)
}

var galleryTwins = []string{"tpl-gallery-item", "tpl-toast"}

type itemData struct {
	Gallery, GalleryHref, SectionHref, SectionLabel string
	Item                                            GalleryItemView
	Image                                           string // the agent-size image, for the page
	Newer, Older                                    string
}

// itemPage is an item's stable link: the image, its title and section,
// and the next and previous items of the section, all without the bundle.
func (s *Site) itemPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ch, ok := s.gallery(w, r, p)
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, newer, older, err := s.chats.svc.GalleryItem(r.Context(), ch, seq)
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		s.Fail(w, r, &p, http.StatusNotFound, "No longer available", apiErr.Message)
		return
	}
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	section := it.Section
	data := itemData{Gallery: ch.Name, GalleryHref: messaging.GalleryHref(ch.Name, nil), SectionHref: messaging.GalleryHref(ch.Name, &section),
		SectionLabel: sectionLabel(section), Item: GalleryItem(it)}
	if it.Image.Preview {
		data.Image = it.Image.URL + "?v=agent"
	}
	if newer > 0 {
		data.Newer = messaging.ItemHref(ch.Name, newer)
	}
	if older > 0 {
		data.Older = messaging.ItemHref(ch.Name, older)
	}
	title := it.Title
	if title == "" {
		title = data.Item.Alt
	}
	l := s.Shell(r, &p, title, "item", "chats")
	l.Back = data.SectionHref
	s.Render(w, http.StatusOK, "item", l, []string{"tpl-toast"}, data)
}
