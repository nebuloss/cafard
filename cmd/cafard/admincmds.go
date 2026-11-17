package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/config"
)

const passwdUsage = `Usage:
  cafard passwd [--if-unset] [-config FILE]

Sets the admin password without asking for the old one: a shell on the
server is the higher privilege. On a terminal it asks twice, without echo;
otherwise it reads one line from stdin:

  echo "$password" | cafard passwd -config /etc/cafard.yaml

--if-unset leaves a password that is set already alone (the installer uses
it). Admin sessions already open end when the server restarts. The
configuration flags (-data-dir, -config) are accepted too.
`

func cmdPasswd(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard passwd", flag.ContinueOnError)
	loader := config.NewLoader(set)
	ifUnset := set.Bool("if-unset", false, "do nothing when a password is set already")
	set.Usage = func() { fmt.Fprint(e.stderr, passwdUsage) }
	if help, err := parseFlags(set, e, args); help || err != nil {
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
	admin := auth.NewAdmin(st.DB(), clock.System{})
	if *ifUnset {
		if set, err := admin.HasPassword(ctx); err != nil || set {
			if set {
				fmt.Fprintln(e.stdout, "An admin password is set already; it was left alone.")
			}
			return err
		}
	}
	password, err := readPassword(e)
	if err != nil {
		return err
	}
	if err := admin.SetPassword(ctx, password); err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, "Admin password set. Admin sessions already open end when the server restarts.")
	return nil
}

// readPassword asks twice on a terminal, or reads one line of a pipe.
func readPassword(e env) (string, error) {
	if f, ok := e.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		ask := func(prompt string) (string, error) {
			fmt.Fprint(e.stderr, prompt)
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(e.stderr)
			return string(b), err
		}
		first, err := ask("New admin password: ")
		if err != nil {
			return "", err
		}
		again, err := ask("Again: ")
		if err != nil {
			return "", err
		}
		if first != again {
			return "", errors.New("the two passwords differ")
		}
		return first, nil
	}
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", errors.New("no password on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

const pairUsage = `Usage:
  cafard pair --name NAME [--scopes read,message,ingest] [--ttl 5m] [-config FILE]

Prints a one-time sign-in link for a person, the same as a QR code on the
Pair page. Opened in a browser, it signs that browser in as NAME (a human,
created when new) for 30 days, renewed while used. It works once, within
--ttl (24h at most). Scopes: ingest, read, message, serial_write; never
admin. The link starts with public_url.
`

func cmdPair(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard pair", flag.ContinueOnError)
	loader := config.NewLoader(set)
	name := set.String("name", "", "the person's principal name")
	scopes := set.String("scopes", "", "comma separated scopes (default read,message,ingest)")
	ttl := set.Duration("ttl", auth.PairingTTL, "how long the link works, 24h at most")
	set.Usage = func() { fmt.Fprint(e.stderr, pairUsage) }
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	if *name == "" {
		return errors.New("pair needs --name")
	}
	sc, err := auth.ParseScopes(*scopes)
	if err != nil {
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
	clk := clock.System{}
	pairings := auth.NewPairings(st.DB(), clk, auth.NewSessions(st.DB(), clk))
	row, code, err := pairings.Create(ctx, "", *name, sc, auth.AdminName, *ttl)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stderr, "Sign-in link for %s, scopes %s. It works once, until %s:\n", row.Name(), row.Scopes.String(),
		row.ExpiresAt.Local().Format(time.DateTime+" MST"))
	fmt.Fprintln(e.stdout, serverSettings(cfg).BaseURL()+"/p/"+code)
	return nil
}
