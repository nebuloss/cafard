// Command cafard is the server, and the commands that look after it.
//
//	cafard serve [-config FILE]   run both listeners
//	cafard version                print the build
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
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cafard/internal/blobs"
	"cafard/internal/config"
	"cafard/internal/httpapi"
	"cafard/internal/logging"
	"cafard/internal/service/access"
	"cafard/internal/store"
	"cafard/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `cafard: a message hub for people and agents on different machines.

Usage:
  cafard serve [-config FILE]   run the public and the admin listener
  cafard version                print the build

The configuration is described in configs/config.example.yaml.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "cafard", version.String())
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "cafard: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// defaultConfig is read when -config is not given and the file exists, so a
// service started by the installer needs no flag.
const defaultConfig = "/etc/cafard/config.yaml"

func serve(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "configuration file (default "+defaultConfig+" if it exists)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		if _, err := os.Stat(defaultConfig); err == nil {
			*path = defaultConfig
		}
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 1
	}
	log := logging.New(cfg.Log.Level, cfg.Log.Format)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServer(ctx, cfg, log, nil); err != nil {
		log.Error("cafard stopped", "err", err)
		return 1
	}
	return 0
}

// runServer opens the state and serves both listeners until ctx ends. ready,
// if given, receives the two bound addresses once both listen; tests use it
// with ":0" ports.
func runServer(ctx context.Context, cfg config.Config, log *slog.Logger, ready func(public, admin net.Addr)) error {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("state directory: %w", err)
	}
	st, err := store.Open(ctx, filepath.Join(cfg.StateDir, "cafard.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	bl, err := blobs.Open(filepath.Join(cfg.StateDir, "files"))
	if err != nil {
		return err
	}
	srv := httpapi.New(httpapi.Deps{Log: log, Store: st, Blobs: bl, Access: access.New(st)})

	pubLn, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	admLn, err := net.Listen("tcp", cfg.AdminListen)
	if err != nil {
		pubLn.Close()
		return fmt.Errorf("admin-listen %s: %w", cfg.AdminListen, err)
	}
	servers := []*http.Server{
		{Handler: srv.Public(), ReadHeaderTimeout: 10 * time.Second},
		{Handler: srv.Admin(), ReadHeaderTimeout: 10 * time.Second},
	}
	errs := make(chan error, 2)
	for i, ln := range []net.Listener{pubLn, admLn} {
		go func() {
			if err := servers[i].Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}
	log.Info("cafard serving", "version", version.Version, "listen", pubLn.Addr().String(),
		"admin_listen", admLn.Addr().String(), "state_dir", cfg.StateDir)
	if ready != nil {
		ready(pubLn.Addr(), admLn.Addr())
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errs:
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		s.Shutdown(shutdownCtx)
	}
	log.Info("cafard stopped")
	return serveErr
}
