package push

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/nebuloss/cafard/internal/ingest/serial"
)

// SerialOptions tune SerialReader.
type SerialOptions struct {
	Port serial.Port // Device and line settings
	Log  *slog.Logger
	// Open opens the port; default serial.OpenLocal. Tests use pipes.
	Open       func(p serial.Port) (io.ReadCloser, error)
	MinBackoff time.Duration // default 1 s
	MaxBackoff time.Duration // default 30 s
}

// SerialReader opens a local serial port and reads it. The first open
// must succeed, so a mistyped COM5 fails at once; after that, a port that
// goes away (a USB adapter unplugged) is reopened with backoff from 1 s
// to 30 s.
func SerialReader(ctx context.Context, opts SerialOptions) (io.ReadCloser, error) {
	if opts.Open == nil {
		opts.Open = func(p serial.Port) (io.ReadCloser, error) { return serial.OpenLocalRW(p) }
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = time.Second
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = max(30*time.Second, opts.MinBackoff)
	}
	port, err := opts.Open(opts.Port)
	if err != nil {
		return nil, err
	}
	return &serialReader{ctx: ctx, opts: opts, port: port}, nil
}

type serialReader struct {
	ctx  context.Context
	opts SerialOptions
	port io.ReadCloser
}

func (s *serialReader) Read(p []byte) (int, error) {
	backoff := s.opts.MinBackoff
	for {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		if s.port == nil {
			port, err := s.opts.Open(s.opts.Port)
			if err != nil {
				s.opts.Log.Debug("serial port still missing", "port", s.opts.Port.Device, "err", err)
				t := time.NewTimer(backoff)
				select {
				case <-t.C:
				case <-s.ctx.Done():
					t.Stop()
					return 0, s.ctx.Err()
				}
				backoff = min(2*backoff, s.opts.MaxBackoff)
				continue
			}
			s.opts.Log.Info("serial port back", "port", s.opts.Port.Device)
			s.port, backoff = port, s.opts.MinBackoff
		}
		n, err := s.port.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil {
			if s.ctx.Err() != nil {
				return 0, s.ctx.Err()
			}
			s.opts.Log.Warn("serial port lost, reopening", "port", s.opts.Port.Device, "err", err)
			s.port.Close()
			s.port = nil
		}
	}
}

func (s *serialReader) Close() error {
	if s.port != nil {
		return s.port.Close()
	}
	return nil
}
