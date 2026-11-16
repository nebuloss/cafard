// Package deps is what every feature's Register receives. It also holds the
// interfaces later tracks fill in, so earlier packages can code against
// them without importing those tracks.
package deps

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// Deps is built once in main.
type Deps struct {
	Server    Server
	Store     *store.Store
	Writer    writer.Writer
	Hub       hub.Hub
	Status    status.Registry
	Auth      auth.Authenticator
	Tokens    *auth.Tokens
	Janitor   *store.Janitor
	Blobs     BlobStore // nil until track A
	Messaging Messaging // nil until track A
	Clock     clock.Clock
	Log       *slog.Logger
	Version   string
}

// Server is the part of the root configuration features need. Features
// get their own config section as a separate Register argument: a feature
// package cannot import internal/config, which imports it.
type Server struct {
	PublicURL  string // as configured, may be empty
	HTTPListen string
	Secure     bool
	TLS        bool
	DataDir    string
}

// BaseURL is the address to print in links and snippets: public_url, or
// one derived from the listen address.
func (s Server) BaseURL() string {
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/")
	}
	scheme := "http"
	if s.TLS {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(s.HTTPListen)
	if err != nil {
		return scheme + "://localhost"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// BlobStore stores files by content.
type BlobStore interface {
	Put(ctx context.Context, r io.Reader, mime string) (dto.Blob, error)
	// Open returns the original ("") or a variant ("agent", "thumb").
	Open(ctx context.Context, sha string, variant string) (io.ReadSeekCloser, dto.Blob, error)
	VisibleTo(ctx context.Context, sha string, p auth.Principal) (bool, error)
}

// Messaging is messages, channels and their visibility.
type Messaging interface {
	Send(ctx context.Context, from auth.Principal, m dto.SendRequest) (dto.Entry, error)
	Visible(ctx context.Context, p auth.Principal, e store.Entry) bool
	// ResolveChannel returns the channel's binding patterns (ids and globs).
	ResolveChannel(ctx context.Context, name string) (streams []string, err error)
	// ChatOf is "chan:x", "dm:a~b" or "" for an entry.
	ChatOf(e store.Entry) string
}
