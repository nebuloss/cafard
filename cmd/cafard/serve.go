package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/web/assets"
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
	return serve(ctx, cfg, log, nil)
}

func newLogger(e env, level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: l}))
}

// newHandler builds the mux once, with every feature registered on it.
func newHandler(_ *config.Config) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintln(w, "ok "+version)
	})
	if err := assets.Register(mux); err != nil {
		return nil, err
	}
	return mux, nil
}

// serve runs the HTTP server until ctx is cancelled, then shuts it down.
// ready, when set, is called with the bound address once it accepts.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, ready func(net.Addr)) error {
	handler, err := newHandler(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: handler,
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
	log.Info("listening", "addr", ln.Addr().String(), "version", version, "tls", cfg.TLS(), "bundle", assets.BundleBuilt())
	if ready != nil {
		ready(ln.Addr())
	}

	errs := make(chan error, 1)
	go func() {
		if cfg.TLS() {
			errs <- srv.ServeTLS(ln, cfg.TLSCert, cfg.TLSKey)
		} else {
			errs <- srv.Serve(ln)
		}
	}()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}
