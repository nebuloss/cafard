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
	"github.com/nebuloss/cafard/internal/projects"
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
	Admin     *auth.Admin // nil in tests without the admin account
	Janitor   *store.Janitor
	Projects  *projects.Service // projects, sinks, grants and Access
	Blobs     BlobStore         // nil in tests without messaging
	Messaging Messaging         // nil in tests without messaging
	Clock     clock.Clock
	Log       *slog.Logger
	Version   string
}

// Server is the part of the root configuration features need. Features
// get their own config section as a separate Register argument: a feature
// package cannot import internal/config, which imports it.
type Server struct {
	PublicURL   string // as configured, may be empty
	HTTPListen  string
	AdminListen string // empty: the admin routes are on HTTPListen too
	Secure      bool
	TLS         bool
	DataDir     string
}

// BaseURL is the address to print in links and snippets: public_url, or
// one derived from the listen address.
func (s Server) BaseURL() string {
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/")
	}
	return s.listenURL(s.HTTPListen)
}

// AdminURL is where the admin signs in: the admin interface when it has an
// address of its own, else BaseURL.
func (s Server) AdminURL() string {
	if s.AdminListen == "" {
		return s.BaseURL()
	}
	return s.listenURL(s.AdminListen)
}

func (s Server) listenURL(address string) string {
	scheme := "http"
	if s.TLS {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(address)
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
	// ResolveChannel returns the message stream and the binding patterns
	// (ids and globs) of the channel p names and may read.
	ResolveChannel(ctx context.Context, p auth.Principal, name string) (chat string, patterns []string, err error)
	// ChatOf is "chan:x", "dm:a~b" or "" for an entry.
	ChatOf(e store.Entry) string
}
