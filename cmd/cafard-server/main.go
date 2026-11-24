// Command cafard-server is the server, and the commands that look after it
// on its box. Sending from other machines is cafard-client's.
//
//	cafard-server serve [-config FILE]    run both listeners
//	cafard-server config [-config FILE]   check the configuration, print it in full
//	cafard-server admin COMMAND           people, agents, devices and projects
//	cafard-server version                 print the build
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
	"cafard/internal/httpapi/admin"
	"cafard/internal/httpapi/api"
	"cafard/internal/httpapi/httpx"
	"cafard/internal/live"
	"cafard/internal/logging"
	"cafard/internal/service/access"
	"cafard/internal/service/attention"
	"cafard/internal/service/chat"
	"cafard/internal/store"
	"cafard/internal/version"

	"gopkg.in/yaml.v3"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `cafard-server: a message hub for people and agents on different machines.

Usage:
  cafard-server serve [-config FILE]    run the public and the admin listener
  cafard-server config [-config FILE]   check the configuration, print it in full
  cafard-server admin  COMMAND          people, agents, devices and projects (cafard-server admin -h)
  cafard-server passwd [-config FILE]   set the admin password, which opens the admin UI
  cafard-server version                 print the build

The configuration is described in configs/config.example.yaml.
To send to cafard from another machine, use cafard-client.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stderr)
	case "config":
		return showConfig(args[1:], stdout, stderr)
	case "admin":
		return adminCommand(args[1:], stdout, stderr)
	case "passwd":
		return passwdCommand(args[1:], os.Stdin, stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "cafard-server", version.String())
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

// loadConfig parses -config for a subcommand and loads the file it names.
// code is the exit code to return when ok is false.
func loadConfig(name string, args []string, stderr io.Writer) (cfg config.Config, code int, ok bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "configuration file (default "+defaultConfig+" if it exists)")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, 2, false
	}
	if *path == "" {
		if _, err := os.Stat(defaultConfig); err == nil {
			*path = defaultConfig
		}
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return config.Config{}, 1, false
	}
	return cfg, 0, true
}

// showConfig prints the configuration in effect, defaults filled in, or why
// it cannot be used. The init scripts run it before starting the service, so
// a mistake is reported where the person starting it looks, rather than as a
// service that died.
func showConfig(args []string, stdout, stderr io.Writer) int {
	cfg, code, ok := loadConfig("config", args, stderr)
	if !ok {
		return code
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 1
	}
	stdout.Write(out)
	return 0
}

func serve(args []string, stderr io.Writer) int {
	cfg, code, ok := loadConfig("serve", args, stderr)
	if !ok {
		return code
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
	// Every write tells the hub what it changed; open pages hear it through
	// /api/v1/events. Closed before the listeners shut down, so a stream left
	// open does not hold the shutdown until it times out.
	hub := live.New()
	defer hub.Close()
	st.Announce(hub)
	acc := access.New(st)
	ch := chat.New(st, bl, acc)
	if cut, err := ch.Start(ctx); err != nil {
		return err
	} else if cut > 0 {
		log.Warn("uploads cut off by the last stop", "files", cut)
	}
	// The public URL is the file's until the admin sets another in the
	// admin UI; that one is in the database and outlives a restart.
	publicURL := httpx.NewPublicURL(cfg.URL())
	if set, err := st.Setting(ctx, store.SettingPublicURL); err != nil {
		return err
	} else {
		publicURL.Set(set)
	}
	proxies, err := cfg.Proxies()
	if err != nil {
		return err
	}
	srv := httpapi.New(httpapi.Deps{Log: log, Store: st, Access: acc, Chat: ch, Attention: attention.New(st, acc), Hub: hub, Admin: admin.Config{
		Listen: cfg.Listen, AdminListen: cfg.AdminListen, PublicURL: publicURL, StateDir: cfg.StateDir, MaxUpload: api.MaxUpload,
		PasswordHash: cfg.AdminPasswordHash,
	}, TrustedProxies: proxies})

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
	hub.Close()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		s.Shutdown(shutdownCtx)
	}
	log.Info("cafard stopped")
	return serveErr
}
