package push

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultSpoolMax caps the spool file.
const DefaultSpoolMax = 100 << 20

// DefaultSpoolPath is where lines wait while the server is unreachable:
// the user cache directory ($XDG_CACHE_HOME or ~/.cache on Linux,
// %LocalAppData% on Windows, ~/Library/Caches on macOS), one file per
// server and stream. getenv is os.Getenv outside tests.
func DefaultSpoolPath(getenv func(string) string, server, stream string) (string, error) {
	dir, err := cacheDir(getenv)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.TrimRight(server, "/")))
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, stream)
	return filepath.Join(dir, "cafard", "spool", fmt.Sprintf("%s-%s.ndjson", safe, hex.EncodeToString(sum[:4]))), nil
}

// cacheDir is os.UserCacheDir reading the environment through getenv.
func cacheDir(getenv func(string) string) (string, error) {
	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = getenv("LocalAppData")
		if dir == "" {
			dir = getenv("LOCALAPPDATA")
		}
	case "darwin", "ios":
		if h := getenv("HOME"); h != "" {
			dir = filepath.Join(h, "Library", "Caches")
		}
	default:
		dir = getenv("XDG_CACHE_HOME")
		if dir == "" || !filepath.IsAbs(dir) {
			dir = ""
			if h := getenv("HOME"); h != "" {
				dir = filepath.Join(h, ".cache")
			}
		}
	}
	if dir == "" {
		return "", errors.New("no user cache directory (neither $XDG_CACHE_HOME, $HOME nor %LocalAppData% is set)")
	}
	return dir, nil
}

// Spool is an append-only file of lines not yet delivered, NDJSON as sent
// to the server, with the read offset in a ".pos" file beside it. Lines
// keep the time they were read.
type Spool struct {
	path    string
	max     int64
	f       *os.File
	size    int64
	pos     int64
	dropped int
}

// OpenSpool opens the spool at path. Lines left by an earlier run are
// kept for replay; a half-written last line is cut off. The file and its
// directory are created only when a line first needs them.
func OpenSpool(path string, max int64) (*Spool, error) {
	if max <= 0 {
		max = DefaultSpoolMax
	}
	s := &Spool{path: path, max: max}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.f = f
	if err := s.recover(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *Spool) create() error {
	if s.f != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	s.f = f
	return s.recover()
}

func (s *Spool) recover() error {
	fi, err := s.f.Stat()
	if err != nil {
		return err
	}
	s.size = fi.Size()
	// Cut back to the last complete line.
	if s.size > 0 {
		const window = 64 << 10
		end := s.size
		for end > 0 {
			start := max(0, end-window)
			buf := make([]byte, end-start)
			if _, err := s.f.ReadAt(buf, start); err != nil {
				return err
			}
			if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
				end = start + int64(i) + 1
				break
			}
			end = start
		}
		if end != s.size {
			if err := s.f.Truncate(end); err != nil {
				return err
			}
			s.size = end
		}
	}
	if b, err := os.ReadFile(s.posPath()); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && n >= 0 && n <= s.size {
			s.pos = n
		}
	}
	return nil
}

func (s *Spool) posPath() string { return s.path + ".pos" }

// Path is the spool file.
func (s *Spool) Path() string { return s.path }

// Empty reports whether every spooled line was delivered.
func (s *Spool) Empty() bool { return s.pos >= s.size }

// Pending is how many bytes wait for delivery.
func (s *Spool) Pending() int64 { return s.size - s.pos }

// Append adds lines at the end. When the file is at its cap, lines that do
// not fit are dropped and counted; a line saying so goes in front of the
// next ones that fit.
func (s *Spool) Append(lines []Line) (dropped int, err error) {
	if len(lines) == 0 {
		return 0, nil
	}
	if err := s.create(); err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	for i, l := range lines {
		rec := encodeLine(l)
		if s.size+int64(buf.Len()+len(rec)) > s.max && s.pos > 0 {
			if err := s.flushBuf(&buf); err != nil {
				return dropped, err
			}
			if err := s.compact(); err != nil {
				return dropped, err
			}
		}
		if s.size+int64(buf.Len()+len(rec)) > s.max {
			n := len(lines) - i
			s.dropped += n
			dropped += n
			break
		}
		if s.dropped > 0 {
			note := encodeLine(Line{Text: fmt.Sprintf("cafard push: the spool was full (%d MiB), %d lines were dropped here", s.max>>20, s.dropped), At: time.Now()})
			if s.size+int64(buf.Len()+len(note)+len(rec)) > s.max {
				n := len(lines) - i
				s.dropped += n
				dropped += n
				break
			}
			buf.Write(note)
			s.dropped = 0
		}
		buf.Write(rec)
	}
	return dropped, s.flushBuf(&buf)
}

func (s *Spool) flushBuf(buf *bytes.Buffer) error {
	if buf.Len() == 0 {
		return nil
	}
	n, err := s.f.WriteAt(buf.Bytes(), s.size)
	s.size += int64(n)
	buf.Reset()
	return err
}

// compact drops the delivered head of the file.
func (s *Spool) compact() error {
	tmp := s.path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.NewSectionReader(s.f, s.pos, s.size-s.pos))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	// Windows cannot rename over an open file.
	if err := s.f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	s.f, s.size, s.pos = f, n, 0
	return s.savePos()
}

// peekBytes keeps a replayed batch well under the server's body limit.
const peekBytes = 4 << 20

// Peek reads up to n lines from the read offset, and the offset after
// them.
func (s *Spool) Peek(n int) ([]Line, int64, error) {
	if s.f == nil {
		return nil, s.pos, nil
	}
	r := bufio.NewReader(io.NewSectionReader(s.f, s.pos, s.size-s.pos))
	var out []Line
	next := s.pos
	for len(out) < n && next-s.pos < peekBytes {
		b, err := r.ReadBytes('\n')
		if len(b) > 0 && b[len(b)-1] == '\n' {
			next += int64(len(b))
			var w wireLine
			if json.Unmarshal(b, &w) == nil {
				at, _ := time.Parse(time.RFC3339Nano, w.TS)
				out = append(out, Line{Text: w.Text, At: at})
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, next, err
		}
	}
	return out, next, nil
}

// Advance marks everything before next as delivered. An empty spool is
// truncated so it does not grow forever.
func (s *Spool) Advance(next int64) error {
	if s.f == nil {
		return nil
	}
	s.pos = next
	if s.pos >= s.size {
		// Delivered: leave nothing behind.
		err := s.f.Close()
		s.f, s.size, s.pos = nil, 0, 0
		if rerr := os.Remove(s.path); err == nil {
			err = rerr
		}
		if rerr := os.Remove(s.posPath()); err == nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = rerr
		}
		return err
	}
	return s.savePos()
}

func (s *Spool) savePos() error {
	return os.WriteFile(s.posPath(), []byte(strconv.FormatInt(s.pos, 10)+"\n"), 0o600)
}

// Close closes the file; what is left is replayed by the next run.
func (s *Spool) Close() error {
	if s.f == nil {
		return nil
	}
	return s.f.Close()
}

func encodeLine(l Line) []byte {
	b, _ := json.Marshal(wireLine{Text: l.Text, TS: l.At.UTC().Format(time.RFC3339Nano)})
	return append(b, '\n')
}
