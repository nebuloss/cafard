package dto

import "encoding/json"

// GallerySection is a batch of a gallery's items.
type GallerySection struct {
	Name    string `json:"name"` // "" for the items without one
	Items   int    `json:"items"`
	LastSeq int64  `json:"last_seq"`
	Href    string `json:"href"` // the gallery page on this section
}

// GalleryItem is one image of a gallery, with its title.
type GalleryItem struct {
	Seq     int64           `json:"seq"`
	Chat    string          `json:"chat"` // chan:<project>/<gallery>
	Title   string          `json:"title"`
	Section string          `json:"section,omitempty"`
	Image   ImageAttachment `json:"image"`
	Href    string          `json:"href"` // /g/<project>/<gallery>/<seq>
}

// GalleryPage answers GET /api/galleries/{chat}: items newest first, and
// every section of the gallery.
type GalleryPage struct {
	Chat          string           `json:"chat"`
	Section       *string          `json:"section,omitempty"` // the filter, when one is applied
	Items         []GalleryItem    `json:"items"`
	Sections      []GallerySection `json:"sections"`
	HasMore       bool             `json:"has_more"`
	NextBeforeSeq int64            `json:"next_before_seq,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (g GalleryPage) MarshalJSON() ([]byte, error) {
	type plain GalleryPage
	p := plain(g)
	p.Items = nonNil(p.Items)
	p.Sections = nonNil(p.Sections)
	return json.Marshal(p)
}
