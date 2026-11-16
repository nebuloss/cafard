// Command cafard collects logs and images from devices and people, serves
// them to AI agents over MCP, and passes messages between agents and humans.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = `cafard - logs, photos and messages for agents and the humans they work with

Usage:
  cafard <command> [flags]

Commands:
  serve     run the server
  push      send stdin, a serial port or a file to a server
  term      a serial terminal that also pushes what it reads (Windows COM ports)
  mcp       a stdio MCP server that forwards to a remote cafard
  token     create and list tokens
  config    validate and print the effective configuration
  version   print the version

Run "cafard <command> -h" for the flags of one command. Every configuration
key is also a flag (-http-listen) and an environment variable
(CAFARD_HTTP_LISTEN); the file is ` + "/etc/cafard.yaml" + ` unless -config or
CAFARD_CONFIG says otherwise.
`

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cafard: "+err.Error())
		os.Exit(1)
	}
}

// env is the process environment, injected so tests do not touch os.Getenv.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
}

type command func(ctx context.Context, e env, args []string) error

var commands = map[string]command{
	"serve":   cmdServe,
	"config":  cmdConfig,
	"version": cmdVersion,
	"push":    notYet("M1"),
	"token":   notYet("M1"),
	"term":    notYet("M6"),
	"mcp":     notYet("M6"),
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	e := env{stdout: stdout, stderr: stderr, getenv: getenv}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("no command given")
	}
	name := args[0]
	switch name {
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return nil
	case "-version", "--version":
		name = "version"
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", name)
	}
	return cmd(ctx, e, args[1:])
}

func cmdVersion(_ context.Context, e env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("version takes no arguments")
	}
	fmt.Fprintln(e.stdout, "cafard "+version)
	return nil
}

func notYet(milestone string) command {
	return func(context.Context, env, []string) error {
		return fmt.Errorf("not implemented yet (milestone %s)", milestone)
	}
}

// parseFlags parses a subcommand's flags. -h prints the flags and is not an
// error; stray arguments are.
func parseFlags(set *flag.FlagSet, e env, args []string) (help bool, err error) {
	set.SetOutput(e.stderr)
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, err
	}
	if set.NArg() > 0 {
		return false, fmt.Errorf("%s: unexpected argument %q", set.Name(), set.Arg(0))
	}
	return false, nil
}
