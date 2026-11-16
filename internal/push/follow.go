package push

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"time"
)

// FollowOptions tune Follow.
type FollowOptions struct {
	// FromStart reads the file from the beginning; by default only what
	// is written after Follow starts is read, as tail -f does.
	FromStart bool
	Poll      time.Duration // default 250 ms
	Log       *slog.Logger
}

// Follow reads path and keeps reading as it grows. When the file shrinks
// (truncated, as PuTTY does when a session log is overwritten) it reads
// again from the start; when another file takes its name (rotation) it
// finishes the old one and opens the new one from the start. A file that
// does not exist yet is waited for. Read returns ctx's error once ctx ends.
func Follow(ctx context.Context, path string, opts FollowOptions) io.ReadCloser {
	if opts.Poll <= 0 {
		opts.Poll = 250 * time.Millisecond
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &follower{ctx: ctx, path: path, opts: opts, seekEnd: !opts.FromStart}
}

type follower struct {
	ctx     context.Context
	path    string
	opts    FollowOptions
	f       *os.File
	fi      os.FileInfo
	off     int64
	seekEnd bool // only for the first open
	waiting bool // already said the file is missing
}

func (fl *follower) open() error {
	f, err := os.Open(fl.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	fl.f, fl.fi, fl.off = f, fi, 0
	if fl.seekEnd {
		if fl.off, err = f.Seek(0, io.SeekEnd); err != nil {
			f.Close()
			fl.f = nil
			return err
		}
	}
	fl.seekEnd = false
	return nil
}

func (fl *follower) Read(p []byte) (int, error) {
	for {
		if err := fl.ctx.Err(); err != nil {
			return 0, err
		}
		if fl.f == nil {
			err := fl.open()
			switch {
			case errors.Is(err, fs.ErrNotExist):
				if !fl.waiting {
					fl.waiting = true
					fl.opts.Log.Info("waiting for the file to appear", "path", fl.path)
				}
				// A file created later is new: read it all.
				fl.seekEnd = false
				if !fl.sleep() {
					return 0, fl.ctx.Err()
				}
				continue
			case err != nil:
				return 0, err
			}
			fl.waiting = false
		}
		n, err := fl.f.Read(p)
		if n > 0 {
			fl.off += int64(n)
			return n, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		// At the end: was the file truncated or replaced?
		cur, err := os.Stat(fl.path)
		switch {
		case err != nil:
			// Gone for now (between a rename and a create): keep waiting.
		case !os.SameFile(fl.fi, cur):
			fl.opts.Log.Info("file replaced, reading the new one", "path", fl.path)
			fl.f.Close()
			fl.f = nil
			continue
		case cur.Size() < fl.off:
			fl.opts.Log.Info("file truncated, reading from the start", "path", fl.path)
			if _, err := fl.f.Seek(0, io.SeekStart); err != nil {
				return 0, err
			}
			fl.off = 0
			continue
		}
		if !fl.sleep() {
			return 0, fl.ctx.Err()
		}
	}
}

func (fl *follower) sleep() bool {
	t := time.NewTimer(fl.opts.Poll)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-fl.ctx.Done():
		return false
	}
}

func (fl *follower) Close() error {
	if fl.f != nil {
		return fl.f.Close()
	}
	return nil
}
