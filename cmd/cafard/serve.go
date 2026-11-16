package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/ingest/httpapi"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/api"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/writer"
)

func cmdServe(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard serve", flag.ContinueOnError)
	loader := config.NewLoader(set)
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	cfg, path, err := loader.Load(e.getenv)
	if err != nil {
		return err
	}
	log := newLogger(e, cfg.LogLevel)
	if path == "" {
		log.Info("no configuration file, using defaults, environment and flags", "looked_at", config.DefaultPath)
	} else {
		log.Info("configuration loaded", "path", path)
	}
	return serve(ctx, cfg, log, e.stdout, nil)
}

func newLogger(e env, level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: l}))
}

func serverSettings(cfg *config.Config) deps.Server {
	return deps.Server{PublicURL: cfg.PublicURL, HTTPListen: cfg.HTTPListen, Secure: cfg.Secure, TLS: cfg.TLS(), DataDir: cfg.DataDir}
}

// openStore creates the data directory and opens the database in it.
func openStore(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	if err := store.EnsureDataDir(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("data_dir: %w", err)
	}
	return store.Open(ctx, filepath.Join(cfg.DataDir, store.FileName))
}

// app is everything serve runs.
type app struct {
	deps    deps.Deps
	handler http.Handler
	writer  *writer.W
	status  *status.Memory
	janitor *store.Janitor
	starts  []func(context.Context) error
}

// build wires every part, building the mux once.
func build(cfg *config.Config, st *store.Store, log *slog.Logger) (*app, error) {
	clk := clock.System{}
	tokens := auth.NewTokens(st.DB(), clk)
	h := hub.New(nil)
	stat := status.New(clk)
	w := writer.New(st, h, stat, clk, log.With("component", "writer"), writer.Options{})
	jan := store.NewJanitor(st, w.Exec, cfg.Retention, clk, log.With("component", "janitor"))
	d := deps.Deps{
		Server:  serverSettings(cfg),
		Store:   st,
		Writer:  w,
		Hub:     h,
		Status:  stat,
		Auth:    &auth.TokenAuthenticator{Tokens: tokens},
		Tokens:  tokens,
		Janitor: jan,
		Clock:   clk,
		Log:     log,
		Version: version,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintln(w, "ok "+version)
	})
	mux.HandleFunc("/v1/traces", http.NotFound)
	mux.HandleFunc("/v1/metrics", http.NotFound)
	if err := assets.Register(mux); err != nil {
		return nil, err
	}
	// Before mcpserver.New, which copies d.
	msg, err := messaging.New(context.Background(), d, messaging.Options{Config: cfg.Messaging, Channels: cfg.Channels,
		Images: cfg.Images, BlobsMaxBytes: int64(cfg.Retention.BlobsMaxBytes)})
	if err != nil {
		return nil, err
	}
	d.Messaging, d.Blobs = msg, msg.Blobs()
	mcp := mcpserver.New(d, cfg.MCP)
	a := &app{deps: d, writer: w, status: stat, janitor: jan}
	// One line per feature: Register(mux, mcp, deps[, its config section]).
	for _, register := range []func() (func(context.Context) error, error){
		func() (func(context.Context) error, error) { return httpapi.Register(mux, mcp, d) },
		func() (func(context.Context) error, error) { return api.RegisterMessages(mux, mcp, d, msg) },
	} {
		start, err := register()
		if err != nil {
			return nil, err
		}
		if start != nil {
			a.starts = append(a.starts, start)
		}
	}
	// Last: tools registered above must exist before /mcp serves.
	mcpserver.Register(mux, mcp)
	a.handler = mux
	return a, nil
}

// serve runs the server until ctx is cancelled, then shuts it down: HTTP
// first, so no handler is left waiting on the writer, then the writer,
// which commits what is queued. ready, when set, gets the bound address.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, stdout io.Writer, ready func(net.Addr)) error {
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	a, err := build(cfg, st, log)
	if err != nil {
		return err
	}
	secret, err := a.deps.Tokens.Bootstrap(ctx)
	if err != nil {
		return fmt.Errorf("first start: %w", err)
	}
	if secret != "" {
		// Printed, never logged: stdout is the operator's terminal or the
		// installer, the log may be shipped elsewhere.
		fmt.Fprintf(stdout, "\nFirst start: created the human principal %q and an admin token.\n", auth.AdminName)
		fmt.Fprintf(stdout, "It is shown once and only its hash is stored:\n\n  %s\n\n", secret)
		fmt.Fprintf(stdout, "Sign in with it at %s/signin, or give agents their own tokens:\n", a.deps.Server.BaseURL())
		fmt.Fprintf(stdout, "  cafard token create --name claude-laptop --kind agent --scopes read,message\n\n")
	}
	if err := auth.ExposureGuard(ctx, cfg.HTTPListen, a.deps.Tokens.CountAdminTokens); err != nil {
		return err
	}

	srv := &http.Server{
		Handler: a.handler,
		// No ReadTimeout or WriteTimeout: SSE streams and wait_for calls stay
		// open for minutes, and phone uploads are slow. Handlers that need a
		// deadline set one through http.ResponseController.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	ln, err := net.Listen("tcp", cfg.HTTPListen)
	if err != nil {
		return err
	}

	writerCtx, stopWriter := context.WithCancel(context.Background())
	defer stopWriter()
	writerDone := make(chan error, 1)
	go func() { writerDone <- a.writer.Run(writerCtx) }()

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.status.Run(gctx) })
	g.Go(func() error { return a.janitor.Run(gctx) })
	for _, start := range a.starts {
		g.Go(func() error { return start(gctx) })
	}
	g.Go(func() error {
		var err error
		if cfg.TLS() {
			err = srv.ServeTLS(ln, cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.Serve(ln)
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})

	log.Info("listening", "addr", ln.Addr().String(), "version", version, "tls", cfg.TLS(), "bundle", assets.BundleBuilt(),
		"data_dir", cfg.DataDir)
	if ready != nil {
		ready(ln.Addr())
	}

	err = g.Wait()
	stopWriter()
	if werr := <-writerDone; werr != nil && err == nil {
		err = werr
	}
	if err == nil {
		log.Info("stopped")
	}
	return err
}
