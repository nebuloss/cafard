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
	"github.com/nebuloss/cafard/internal/ingest/netconsole"
	"github.com/nebuloss/cafard/internal/ingest/otlp"
	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/ingest/syslog"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web"
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
	return deps.Server{PublicURL: cfg.PublicURL, HTTPListen: cfg.HTTPListen, AdminListen: cfg.AdminListen, Secure: cfg.Secure,
		TLS: cfg.TLS(), DataDir: cfg.DataDir}
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
	deps deps.Deps
	// public serves what anyone signed in, any agent and any device may
	// reach; admin serves that and the admin routes, and is the only
	// handler that honours the admin cookie.
	public, admin http.Handler
	writer        *writer.W
	status        *status.Memory
	janitor       *store.Janitor
	starts        []func(context.Context) error
}

// build wires every part, building each mux once: the public one, and
// the admin one, which holds the admin-only routes and hands everything
// else to the public one.
func build(cfg *config.Config, st *store.Store, log *slog.Logger) (*app, error) {
	clk := clock.System{}
	projs, err := projects.New(context.Background(), st.DB(), clk, cfg.Retention.Limits(), log.With("component", "projects"))
	if err != nil {
		return nil, err
	}
	tokens := auth.NewTokens(st.DB(), clk)
	admin := auth.NewAdmin(st.DB(), clk)
	h := hub.New(nil)
	stat := status.New(clk)
	w := writer.New(st, h, stat, clk, log.With("component", "writer"), writer.Options{})
	jan := store.NewJanitor(st, w.Exec, cfg.Retention, clk, log.With("component", "janitor"))
	jan.SetProjects(projs.Limits)
	d := deps.Deps{
		Server:   serverSettings(cfg),
		Store:    st,
		Writer:   w,
		Hub:      h,
		Status:   stat,
		Auth:     &auth.TokenAuthenticator{Tokens: tokens, Sessions: auth.NewSessions(st.DB(), clk).Lookup, Admin: admin},
		Tokens:   tokens,
		Admin:    admin,
		Janitor:  jan,
		Projects: projs,
		Clock:    clk,
		Log:      log,
		Version:  version,
	}

	mux := http.NewServeMux()
	adminMux := http.NewServeMux()
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
	// After messaging, which gives a new project its #general, and before
	// serial.Register, which runs the ports of the sinks.
	warnings, err := projs.ImportConfig(context.Background(), cfg.Serial.Ports, cfg.Netconsole.Names)
	if err != nil {
		return nil, fmt.Errorf("importing the serial list and netconsole.names: %w", err)
	}
	for _, w := range warnings {
		log.Warn(w)
	}
	mcp := mcpserver.New(d, cfg.MCP)
	a := &app{deps: d, writer: w, status: stat, janitor: jan}
	// One line per feature: Register(mux, mcp, deps[, its config section]).
	for _, register := range []func() (func(context.Context) error, error){
		func() (func(context.Context) error, error) { return httpapi.Register(mux, mcp, d) },
		func() (func(context.Context) error, error) { return syslog.Register(mux, mcp, d, &cfg.Syslog) },
		func() (func(context.Context) error, error) { return netconsole.Register(mux, mcp, d, &cfg.Netconsole) },
		func() (func(context.Context) error, error) { return otlp.Register(mux, mcp, d, &cfg.OTLP) },
		func() (func(context.Context) error, error) { return serial.Register(mux, mcp, d) },
		func() (func(context.Context) error, error) { return api.RegisterMessages(mux, mcp, d, msg) },
		func() (func(context.Context) error, error) { return web.Register(mux, adminMux, mcp, d) },
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
	adminMux.Handle("/", mux)
	a.public = mux
	a.admin = auth.AdminInterface(adminMux)
	return a, nil
}

// listener is one address serve binds and what it serves there.
type listener struct {
	name, address string
	handler       http.Handler
}

// listeners is one address serving everything, or, with admin_listen
// set, the public routes on http_listen and all of them on admin_listen.
func (a *app) listeners(cfg *config.Config) []listener {
	if cfg.AdminListen == "" {
		return []listener{{name: "http", address: cfg.HTTPListen, handler: a.admin}}
	}
	return []listener{
		{name: "http", address: cfg.HTTPListen, handler: a.public},
		{name: "admin", address: cfg.AdminListen, handler: a.admin},
	}
}

// serve runs the server until ctx is cancelled, then shuts it down: HTTP
// first, so no handler is left waiting on the writer, then the writer,
// which commits what is queued. ready, when set, gets the bound addresses;
// admin is nil when one address serves everything.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, stdout io.Writer, ready func(public, admin net.Addr)) error {
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	a, err := build(cfg, st, log)
	if err != nil {
		return err
	}
	password, err := a.deps.Admin.EnsurePassword(ctx)
	if err != nil {
		return fmt.Errorf("first start: %w", err)
	}
	if password != "" {
		// Printed, never logged: stdout is the operator's terminal or the
		// installer, the log may be shipped elsewhere.
		fmt.Fprintf(stdout, "\nNo admin password was set, so cafard made one.\n")
		fmt.Fprintf(stdout, "It is shown once and only its hash is stored:\n\n  %s\n\n", password)
		fmt.Fprintf(stdout, "Sign in with it at %s/signin, and change it there or with cafard passwd.\n", a.deps.Server.AdminURL())
		fmt.Fprintf(stdout, "Give agents their own tokens:\n")
		fmt.Fprintf(stdout, "  cafard token create --name claude-laptop --kind agent --scopes read,message\n\n")
	}
	if err := auth.ExposureGuard(ctx, a.deps.Admin.HasPassword, cfg.HTTPListen, cfg.AdminListen); err != nil {
		return err
	}

	// Every address is bound before anything serves, so a taken port stops
	// the start instead of leaving half the server up.
	specs := a.listeners(cfg)
	lns := make([]net.Listener, 0, len(specs))
	for _, l := range specs {
		ln, err := net.Listen("tcp", l.address)
		if err != nil {
			for _, open := range lns {
				open.Close()
			}
			return fmt.Errorf("%s listener: %w", l.name, err)
		}
		lns = append(lns, ln)
	}
	servers := make([]*http.Server, len(specs))
	for i, l := range specs {
		servers[i] = &http.Server{
			Handler: l.handler,
			// No ReadTimeout or WriteTimeout: SSE streams and wait_for calls
			// stay open for minutes, and phone uploads are slow. Handlers that
			// need a deadline set one through http.ResponseController.
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       2 * time.Minute,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
			BaseContext:       func(net.Listener) context.Context { return ctx },
		}
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
	for i, srv := range servers {
		g.Go(func() error {
			var err error
			if cfg.TLS() {
				err = srv.ServeTLS(lns[i], cfg.TLSCert, cfg.TLSKey)
			} else {
				err = srv.Serve(lns[i])
			}
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	}
	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var first error
		for _, srv := range servers {
			if err := srv.Shutdown(shutdownCtx); err != nil && first == nil {
				first = err
			}
		}
		return first
	})

	for i, l := range specs {
		log.Info("listening", "interface", l.name, "addr", lns[i].Addr().String(), "version", version, "tls", cfg.TLS(),
			"bundle", assets.BundleBuilt(), "data_dir", cfg.DataDir)
	}
	if ready != nil {
		var admin net.Addr
		if len(lns) > 1 {
			admin = lns[1].Addr()
		}
		ready(lns[0].Addr(), admin)
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
