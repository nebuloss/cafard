package view

import (
	"net/http"

	"github.com/nebuloss/cafard/internal/dto"
)

// ThemeCookie holds the theme choice. It belongs to the screen, not the
// person, so it is a cookie rather than a setting of the principal, and
// the server renders the right palette on the first paint.
const ThemeCookie = "cafard_theme"

// ThemeOf reads the choice; anything unknown follows the device.
func ThemeOf(r *http.Request) dto.Theme {
	c, err := r.Cookie(ThemeCookie)
	if err != nil {
		return dto.ThemeSystem
	}
	switch t := dto.Theme(c.Value); t {
	case dto.ThemeLight, dto.ThemeDark, dto.ThemeSystem:
		return t
	}
	return dto.ThemeSystem
}
