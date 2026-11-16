package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/push"
)

const tokenUsage = `Usage:
  cafard token create --name NAME --kind agent|human|device --scopes read,message [--label TEXT]
  cafard token list [--name NAME]
  cafard token revoke ID

The token is printed once; only its hash is stored. Scopes: ingest, read,
message, serial_write, admin. The configuration flags (-data-dir, -config)
are accepted too.
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
  cafard push --server URL --token T --stream NAME [--echo]   reads stdin

  journalctl -f | cafard push --server http://10.0.50.2:8790 --token cfd_... --stream laptop

--server and --token may come from CAFARD_SERVER and CAFARD_TOKEN. The
stream is push:NAME unless NAME already has a kind prefix.
`

func cmdPush(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard push", flag.ContinueOnError)
	server := set.String("server", e.getenv("CAFARD_SERVER"), "server URL")
	token := set.String("token", "", "bearer token (default $CAFARD_TOKEN)")
	stream := set.String("stream", "", "stream name")
	echo := set.Bool("echo", false, "also print what is read")
	set.Usage = func() { fmt.Fprint(e.stderr, pushUsage) }
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	if *token == "" {
		*token = e.getenv("CAFARD_TOKEN")
	}
	opts := push.Options{Server: *server, Token: *token, Stream: *stream, Log: newLogger(e, "info"), Version: version}
	if *echo {
		opts.Echo = e.stdout
	}
	p, err := push.New(opts)
	if err != nil {
		return err
	}
	return p.Reader(ctx, e.stdin)
}
