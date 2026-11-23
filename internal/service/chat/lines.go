package chat

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"cafard/internal/service/access"
)

// A log is read a page at a time, never whole: an agent that asked for a
// 40 MB file would spend its context on it (docs/design.md, "Decisions that
// come from the reader being a model").
const (
	MaxLines     = 400
	MaxPageBytes = 64 << 10
	// MaxLineBytes is where one line is cut. A minified bundle or a binary
	// mistaken for text is one enormous line, and would otherwise be a page
	// of its own.
	MaxLineBytes = 2 << 10
	maxContext   = 10
)

// LinesQuery is which lines to read. Exactly one of the three ways is used:
// Grep if set, else Tail if set, else From.
type LinesQuery struct {
	From    int    // first line, from 1
	Count   int    // how many, at most MaxLines
	Tail    int    // the last n lines
	Grep    string // lines containing this, case-insensitively
	Context int    // with this many lines around each match, at most maxContext
}

// Line is one line of a file, numbered from 1.
type Line struct {
	N    int    `json:"n"`
	Text string `json:"text"`
	Cut  bool   `json:"cut,omitempty"` // longer than MaxLineBytes, cut there
}

// LinesPage is a page of a file and where it sits in the whole.
type LinesPage struct {
	Lines []Line `json:"lines"`
	// Total is how many lines the file has (so far, if live).
	Total int `json:"total"`
	// Next is the line to read from to continue, or 0 at the end. For grep,
	// the line to search from.
	Next int `json:"next,omitempty"`
	// Matches counts every matching line in the file, for grep, including
	// those past the page.
	Matches int `json:"matches,omitempty"`
}

// Lines reads a page of a text file.
func (s *Service) Lines(ctx context.Context, who access.Who, fileID int64, q LinesQuery) (LinesPage, error) {
	fw, r, err := s.File(ctx, who, fileID)
	if err != nil {
		return LinesPage{}, err
	}
	defer r.Close()
	if !isText(fw.ContentType) {
		return LinesPage{}, &access.Refusal{Code: access.BadValue, Message: fmt.Sprintf(
			"%s is %s, not text: read it whole, or open it as an image.", fw.Name, fw.ContentType)}
	}
	return ReadLines(r, q)
}

func isText(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "json") || strings.Contains(ctype, "xml") ||
		strings.Contains(ctype, "yaml") || strings.Contains(ctype, "javascript")
}

// ReadLines reads the page q asks for from r. It reads the whole of r once,
// to count lines; a line index kept beside the file is the later answer for
// very large logs, when a range should be a seek and not a scan.
func ReadLines(r io.Reader, q LinesQuery) (LinesPage, error) {
	if q.Count <= 0 || q.Count > MaxLines {
		q.Count = MaxLines
	}
	if q.From <= 0 {
		q.From = 1
	}
	if q.Context < 0 {
		q.Context = 0
	}
	if q.Context > maxContext {
		q.Context = maxContext
	}
	switch {
	case q.Grep != "":
		return grep(r, q)
	case q.Tail > 0:
		return tail(r, min(q.Tail, MaxLines))
	default:
		return lineRange(r, q.From, q.Count)
	}
}

// eachLine calls fn with every line of r, numbered from 1, cut at
// MaxLineBytes and with invalid UTF-8 replaced.
func eachLine(r io.Reader, fn func(Line)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	n := 0
	for {
		raw, err := readLine(br)
		if len(raw) > 0 || err == nil {
			n++
			fn(toLine(n, raw))
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLine reads up to and without the next newline, keeping at most
// MaxLineBytes+1 bytes of a long line and discarding the rest.
func readLine(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(out) <= MaxLineBytes {
			out = append(out, chunk[:min(len(chunk), MaxLineBytes+1-len(out))]...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == nil {
			out = trimNewline(out)
		}
		return out, err
	}
}

func trimNewline(b []byte) []byte {
	return bytes.TrimSuffix(bytes.TrimSuffix(b, []byte("\n")), []byte("\r"))
}

func toLine(n int, raw []byte) Line {
	l := Line{N: n}
	if len(raw) > MaxLineBytes {
		raw, l.Cut = raw[:MaxLineBytes], true
	}
	l.Text = strings.ToValidUTF8(strings.TrimSuffix(string(raw), "\r"), "�")
	return l
}

// page adds l to p unless the page is full, and says whether it did.
func page(p *LinesPage, bytes *int, l Line, max int) bool {
	if len(p.Lines) >= max || *bytes+len(l.Text) > MaxPageBytes {
		return false
	}
	p.Lines = append(p.Lines, l)
	*bytes += len(l.Text)
	return true
}

func lineRange(r io.Reader, from, count int) (LinesPage, error) {
	var p LinesPage
	size, full := 0, false
	err := eachLine(r, func(l Line) {
		p.Total = l.N
		if l.N < from || full {
			return
		}
		if !page(&p, &size, l, count) {
			full = true
			p.Next = l.N
		}
	})
	return p, err
}

// tail keeps the last n lines in a ring as it reads, so a long log costs one
// pass and n lines of memory.
func tail(r io.Reader, n int) (LinesPage, error) {
	ring := make([]Line, n)
	total := 0
	err := eachLine(r, func(l Line) {
		ring[total%n] = l
		total = l.N
	})
	kept := min(total, n)
	last := make([]Line, 0, kept)
	for i := total - kept; i < total; i++ {
		last = append(last, ring[i%n])
	}
	// Keep the end: drop from the front until the page fits in MaxPageBytes.
	size := 0
	for _, l := range last {
		size += len(l.Text)
	}
	for len(last) > 0 && size > MaxPageBytes {
		size -= len(last[0].Text)
		last = last[1:]
	}
	return LinesPage{Lines: last, Total: total}, err
}

// grep returns matching lines from q.From on, each with q.Context lines
// around it, as one list in file order without repeats.
func grep(r io.Reader, q LinesQuery) (LinesPage, error) {
	needle := strings.ToLower(q.Grep)
	var p LinesPage
	size, full := 0, false
	before := make([]Line, 0, q.Context)
	after := 0
	lastAdded := 0
	add := func(l Line) {
		if full || l.N <= lastAdded {
			return
		}
		if !page(&p, &size, l, q.Count) {
			full = true
			p.Next = l.N
			return
		}
		lastAdded = l.N
	}
	err := eachLine(r, func(l Line) {
		p.Total = l.N
		match := l.N >= q.From && strings.Contains(strings.ToLower(l.Text), needle)
		if match {
			p.Matches++
			for _, b := range before {
				add(b)
			}
			add(l)
			after = q.Context
		} else if after > 0 {
			add(l)
			after--
		}
		if q.Context > 0 {
			if len(before) == q.Context {
				before = before[1:]
			}
			before = append(before, l)
		}
	})
	return p, err
}
