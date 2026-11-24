// Package i18n says what cafard shows people in their language: English, or
// French.
//
// The English sentence is the key. Code writes it where it always did, as a
// format with its arguments, and the surfaces that answer people say it in
// the request's language at the edge: the chat app's API, the admin API and
// the join pages. A refusal carries its format and arguments for that
// (service/access, Refusal.In). What machines read stays English: the MCP
// server, whose texts are measured against a real model; the install
// scripts and the errors served to a shell; the log; the audit log's
// actions; the CLI; and every JSON error code.
//
// The French formats are in fr.go, by the file their English comes from. A
// test reads the source: every literal format given to T, to a refusal or to
// a surface's say has a French entry with the same verbs, and every entry is
// still used. A French format may take its arguments in another order with
// explicit indexes (%[2]s).
package i18n

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Lang is a language cafard speaks.
type Lang string

const (
	EN Lang = "en"
	FR Lang = "fr"
)

// Cookie is where the web UIs keep the language a person chose. It beats
// the browser's Accept-Language: a person reading French on an English
// laptop chose it.
const Cookie = "cafard_lang"

var langs = []Lang{EN, FR}

// FromRequest is the language to answer r in: the cookie, else the first of
// Accept-Language's languages, by q-value, that cafard has, else English.
// curl and the MCP clients send neither, and get English.
func FromRequest(r *http.Request) Lang {
	if c, err := r.Cookie(Cookie); err == nil {
		if l, ok := parse(c.Value); ok {
			return l
		}
	}
	return fromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// parse reads a language tag by its primary subtag: fr-CA is French.
func parse(tag string) (Lang, bool) {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	for _, l := range langs {
		if string(l) == base {
			return l, true
		}
	}
	return "", false
}

// fromAcceptLanguage is the language cafard has with the highest q-value;
// of two with the same, the one listed first. q=0 means "not this one".
func fromAcceptLanguage(header string) Lang {
	best, bestQ := EN, 0.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(part, ";")
		l, ok := parse(tag)
		if !ok {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		if q > bestQ {
			best, bestQ = l, q
		}
	}
	return best
}

// T is format, filled in with args, in l: the French format for it when l
// is French and there is one, else the English. It is always formatted, so
// a literal % is written %%, as for fmt.Sprintf.
func T(l Lang, format string, args ...any) string {
	if l == FR {
		if f, ok := fr[format]; ok {
			format = f
		}
	}
	return fmt.Sprintf(format, args...)
}
