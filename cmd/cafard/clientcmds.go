package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/push"
)

const tokenUsage = `Usage:
  cafard token create --name NAME --kind agent|human|device --scopes read,message [--label TEXT]
  cafard token list [--name NAME]
  cafard token revoke ID

The token is printed once; only its hash is stored. Scopes: ingest, read,
message, serial_write. No token carries admin: the admin signs in with the
password (cafard passwd). The configuration flags (-data-dir, -config) are
accepted too.
`

func cmdToken(ctx context.Context, e env, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(e.stdout, tokenUsage)
		if len(args) == 0 {
			return fmt.Errorf("token needs create, list or revoke")
		}
		return nil
	}
	sub, args := args[0], args[1:]
	set := flag.NewFlagSet("cafard token "+sub, flag.ContinueOnError)
	loader := config.NewLoader(set)
	name := set.String("name", "", "principal name")
	kind := set.String("kind", "agent", "principal kind: agent, human or device")
	scopes := set.String("scopes", "", "comma separated scopes")
	label := set.String("label", "", "what the token is for")
	set.SetOutput(e.stderr)
	if err := set.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(e.stdout, tokenUsage)
			return nil
		}
		return err
	}
	cfg, _, err := loader.Load(e.getenv)
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	tokens := auth.NewTokens(st.DB(), clock.System{})

	switch sub {
	case "create":
		if set.NArg() > 0 {
			return fmt.Errorf("unexpected argument %q", set.Arg(0))
		}
		if *name == "" || *scopes == "" {
			return fmt.Errorf("create needs --name and --scopes")
		}
		set, err := auth.ParseScopes(*scopes)
		if err != nil {
			return err
		}
		if _, err := tokens.EnsurePrincipal(ctx, *name, *kind); err != nil {
			return err
		}
		tr, secret, err := tokens.CreateToken(ctx, *name, *label, set)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.stderr, "token %d for %s (%s), scopes %s. It is shown once:\n", tr.ID, tr.Principal, tr.Kind, set.String())
		fmt.Fprintln(e.stdout, secret)
		return nil
	case "list":
		rows, err := tokens.ListTokens(ctx, *name)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tPRINCIPAL\tKIND\tSCOPES\tLABEL\tCREATED\tLAST USED")
		for _, r := range rows {
			last := "never"
			if r.LastUsed != nil {
				last = r.LastUsed.Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Principal, r.Kind, strings.Join(r.Scopes.Strings(), ","), r.Label, r.CreatedAt.Format(time.DateTime), last)
		}
		return tw.Flush()
	case "revoke":
		if set.NArg() != 1 {
			return fmt.Errorf("revoke needs one token id (see cafard token list)")
		}
		id, err := strconv.ParseInt(set.Arg(0), 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not a token id", set.Arg(0))
		}
		if err := tokens.RevokeToken(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "token %d revoked\n", id)
		return nil
	}
	return fmt.Errorf("unknown token command %q (create, list, revoke)", sub)
}

const pushUsage = `Usage:
  cafard push --server URL --token T --stream NAME [source] [--echo] [--spool PATH|off]

Sources (one at most):
  (none)                          read stdin      journalctl -f | cafard push ...
  --serial COM5 [--baud 115200]   read a serial port; reopened if it goes away
  --file PATH [--follow]          read a file; --follow keeps reading as it
                                  grows (a PuTTY session log), through
                                  truncation and rotation. --from-start also
                                  sends what the file holds already.

  cafard.exe push --server http://10.0.50.2:8790 --stream bench --serial COM5 --baud 115200

--server and --token may come from CAFARD_SERVER and CAFARD_TOKEN. The
stream is push:NAME unless NAME already has a kind prefix. While the server
is unreachable, lines wait in a spool file (100 MiB at most, in the user
cache directory by default) and are sent in order when it is back, or by
the next push to the same server and stream.
`

func cmdPush(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard push", flag.ContinueOnError)
	rf := addRemoteFlags(set, e, "server URL")
	stream := set.String("stream", "", "stream name")
	echo := set.Bool("echo", false, "also print what is read")
	device := set.String("serial", "", "serial port to read (COM5, /dev/ttyUSB0)")
	baud := set.Int("baud", serial.DefaultBaud, "baud rate for --serial (8N1)")
	file := set.String("file", "", "file to read")
	follow := set.Bool("follow", false, "with --file: keep reading as the file grows")
	fromStart := set.Bool("from-start", false, "with --follow: also send what the file already holds")
	spoolPath := set.String("spool", "", `spool file for lines the server cannot take yet, or "off" (default in the user cache directory)`)
	set.Usage = func() { fmt.Fprint(e.stderr, pushUsage) }
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) { given[f.Name] = true })
	switch {
	case *device != "" && *file != "":
		return fmt.Errorf("push reads one source: --serial or --file, not both")
	case given["baud"] && *device == "":
		return fmt.Errorf("--baud goes with --serial")
	case *follow && *file == "":
		return fmt.Errorf("--follow goes with --file")
	case *fromStart && !*follow:
		return fmt.Errorf("--from-start goes with --follow")
	}
	log := newLogger(e, "info")
	opts := push.Options{Server: rf.server, Token: rf.bearer(), Stream: *stream, Log: log, Version: version}
	if *echo {
		opts.Echo = e.stdout
	}
	p, closeSpool, err := newPusher(e, opts, *spoolPath)
	if err != nil {
		return err
	}
	defer closeSpool()

	var src io.Reader = e.stdin
	switch {
	case *device != "":
		r, err := push.SerialReader(ctx, push.SerialOptions{Port: serial.Port{Device: *device, Baud: *baud}, Log: log})
		if err != nil {
			return fmt.Errorf("--serial %s: %w", *device, err)
		}
		defer r.Close()
		src = r
	case *file != "" && *follow:
		r := push.Follow(ctx, *file, push.FollowOptions{FromStart: *fromStart, Log: log})
		defer r.Close()
		src = r
	case *file != "":
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	err = p.Reader(ctx, src)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil && (*device != "" || *follow) {
		// Ctrl-C is how a serial or follow push ends.
		return nil
	}
	return err
}

// remoteFlags are --server and --token, shared by the commands that talk
// to a server.
type remoteFlags struct {
	server, token string
	getenv        func(string) string
}

func addRemoteFlags(set *flag.FlagSet, e env, serverUsage string) *remoteFlags {
	rf := &remoteFlags{getenv: e.getenv}
	set.StringVar(&rf.server, "server", e.getenv("CAFARD_SERVER"), serverUsage)
	// Not a flag default, so -help never prints the token.
	set.StringVar(&rf.token, "token", "", "bearer token (default $CAFARD_TOKEN)")
	return rf
}

// bearer is --token, else CAFARD_TOKEN.
func (rf *remoteFlags) bearer() string {
	if rf.token != "" {
		return rf.token
	}
	return rf.getenv("CAFARD_TOKEN")
}

// newPusher builds the pusher of cafard push and cafard term, with its
// spool. The returned func closes the spool.
func newPusher(e env, opts push.Options, spoolPath string) (*push.Pusher, func(), error) {
	if _, err := push.New(opts); err != nil {
		return nil, nil, err // the flags are wrong: say so before touching the spool
	}
	sp, err := openSpool(e, opts.Log, spoolPath, opts.Server, opts.Stream)
	if err != nil {
		return nil, nil, err
	}
	closeSpool := func() {}
	if sp != nil {
		opts.Spool = sp
		closeSpool = func() { sp.Close() }
	}
	p, err := push.New(opts)
	if err != nil {
		closeSpool()
		return nil, nil, err
	}
	return p, closeSpool, nil
}

// openSpool opens the spool of a push: path, "off" (nil), or "" for the
// default place in the user cache directory.
func openSpool(e env, log *slog.Logger, path, server, stream string) (*push.Spool, error) {
	if path == "off" {
		return nil, nil
	}
	if path == "" {
		p, err := push.DefaultSpoolPath(e.getenv, server, stream)
		if err != nil {
			log.Warn("running without a spool: lines wait in memory while the server is unreachable", "err", err)
			return nil, nil
		}
		path = p
	}
	sp, err := push.OpenSpool(path, push.DefaultSpoolMax)
	if err != nil {
		return nil, fmt.Errorf("spool: %w (pass --spool PATH or --spool off)", err)
	}
	return sp, nil
}
