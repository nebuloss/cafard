package messaging

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/nebuloss/cafard/internal/dto"
)

var linkRE = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// Segments parses a message's text into the pieces the web UI renders:
// ``` fenced code blocks, `inline code`, http(s) links and plain text.
func Segments(text string) []dto.Segment {
	var out []dto.Segment
	for text != "" {
		i := strings.Index(text, "```")
		if i < 0 {
			out = append(out, inline(text)...)
			break
		}
		end := strings.Index(text[i+3:], "```")
		if end < 0 {
			out = append(out, inline(text)...)
			break
		}
		out = append(out, inline(text[:i])...)
		body := text[i+3 : i+3+end]
		lang := ""
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			first := strings.TrimSpace(body[:nl])
			if first != "" && !strings.ContainsAny(first, " \t") && len(first) <= 20 {
				lang = first
				body = body[nl+1:]
			} else if first == "" {
				body = body[nl+1:]
			}
		}
		out = append(out, dto.Segment{T: dto.SegCodeblock, V: strings.TrimSuffix(body, "\n"), Lang: lang})
		text = text[i+3+end+3:]
	}
	return merge(out)
}

func inline(text string) []dto.Segment {
	var out []dto.Segment
	for text != "" {
		i := strings.IndexByte(text, '`')
		if i < 0 {
			out = append(out, links(text)...)
			break
		}
		end := strings.IndexByte(text[i+1:], '`')
		if end < 0 || strings.Contains(text[i+1:i+1+end], "\n") {
			out = append(out, links(text[:i+1])...)
			text = text[i+1:]
			continue
		}
		out = append(out, links(text[:i])...)
		if code := text[i+1 : i+1+end]; code != "" {
			out = append(out, dto.Segment{T: dto.SegCode, V: code})
		}
		text = text[i+1+end+1:]
	}
	return out
}

func links(text string) []dto.Segment {
	var out []dto.Segment
	for text != "" {
		m := linkRE.FindStringIndex(text)
		if m == nil {
			out = append(out, dto.Segment{T: dto.SegText, V: text})
			break
		}
		href := strings.TrimRight(text[m[0]:m[1]], ".,;:!?)]}")
		if m[0] > 0 {
			out = append(out, dto.Segment{T: dto.SegText, V: text[:m[0]]})
		}
		if u, err := url.Parse(href); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			out = append(out, dto.Segment{T: dto.SegLink, V: href, Href: u.String()})
		} else {
			out = append(out, dto.Segment{T: dto.SegText, V: href})
		}
		text = text[m[0]+len(href):]
	}
	return out
}

// merge joins adjacent text segments.
func merge(in []dto.Segment) []dto.Segment {
	var out []dto.Segment
	for _, s := range in {
		if s.T == dto.SegText && s.V == "" {
			continue
		}
		if n := len(out); n > 0 && s.T == dto.SegText && out[n-1].T == dto.SegText {
			out[n-1].V += s.V
			continue
		}
		out = append(out, s)
	}
	return out
}
