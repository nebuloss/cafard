package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/mcpproxy"
	"github.com/nebuloss/cafard/internal/push"
	"github.com/nebuloss/cafard/internal/term"
)

const mcpUsage = `Usage:
  cafard mcp --server URL --token T

A stdio MCP server that forwards every request to the cafard at URL over
streamable HTTP: tools, resources, subscriptions and progress. For MCP
clients that only start local commands:

  {"mcpServers": {"cafard": {"command": "cafard",
    "args": ["mcp", "--server", "http://10.0.50.2:8790"],
    "env": {"CAFARD_TOKEN": "cfd_..."}}}}

--server and --token may come from CAFARD_SERVER and CAFARD_TOKEN. It
never opens a database; stdout carries the protocol, logs go to stderr.
`

func cmdMCP(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard mcp", flag.ContinueOnError)
	rf := addRemoteFlags(set, e, "server URL")
	logLevel := set.String("log-level", "warn", "stderr log level: debug, info, warn, error")
	set.Usage = func() { fmt.Fprint(e.stderr, mcpUsage) }
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	p, err := mcpproxy.New(ctx, mcpproxy.Options{Server: rf.server, Token: rf.bearer(), Version: version, Log: newLogger(e, *logLevel)})
	if err != nil {
		return err
	}
	err = p.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(e.stdin), Writer: nopWriteCloser{e.stdout}})
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, mcp.ErrConnectionClosed) {
		return nil // the client went away
	}
	return err
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

const termUsage = `Usage:
  cafard term PORT [--baud 115200] [--server URL --token T [--stream NAME] [--spool PATH|off]]

A serial terminal: it shows what PORT sends, sends your keys to it, and
pushes everything it reads to the server (when --server is given), so
agents see the console while you type in it. Ctrl-] quits.

  cafard.exe term COM5 --baud 115200 --server http://10.0.50.2:8790 --stream bench

The stream is push:NAME (default the port's name, push:com5). --server and
--token may come from CAFARD_SERVER and CAFARD_TOKEN. Lines the server
cannot take yet wait in a spool, as with cafard push.
`

func cmdTerm(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard term", flag.ContinueOnError)
	rf := addRemoteFlags(set, e, "server URL; without one nothing is pushed")
	stream := set.String("stream", "", "stream name (default the port's name)")
	baud := set.Int("baud", serial.DefaultBaud, "baud rate (8N1)")
	spoolPath := set.String("spool", "", `spool file for lines the server cannot take yet, or "off"`)
	set.Usage = func() { fmt.Fprint(e.stderr, termUsage) }
	// The port comes first, flags after it, as in the examples.
	var device string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		device, args = args[0], args[1:]
	}
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	if device == "" {
		fmt.Fprint(e.stderr, termUsage)
		return errors.New("term needs a serial port, e.g. COM5 or /dev/ttyUSB0")
	}
	if *stream == "" {
		*stream = portStream(device)
	}
	// Raw mode turns off the terminal's own \n to \r\n, so the log
	// lines between the port's output carry their \r.
	log := newLogger(env{stderr: crlfWriter{e.stderr}}, "info")
	opts := term.Options{Port: serial.Port{Device: device, Baud: *baud}, Log: log}
	if f, ok := e.stdin.(*os.File); ok {
		opts.Console = term.StdConsole{In: f, Out: e.stdout}
	} else {
		opts.Console = plainConsole{e.stdin, e.stdout}
	}
	if rf.server != "" {
		p, closeSpool, err := newPusher(e, push.Options{Server: rf.server, Token: rf.bearer(), Stream: *stream, Log: log, Version: version}, *spoolPath)
		if err != nil {
			return err
		}
		defer closeSpool()
		opts.Push = p.Reader
	}
	if err := term.Run(ctx, opts); err != nil {
		return fmt.Errorf("term %s: %w", device, err)
	}
	return nil
}

var notStreamChar = regexp.MustCompile(`[^a-z0-9._-]+`)

// portStream is the default stream name of a port: COM5 -> com5,
// /dev/ttyUSB0 -> ttyusb0.
func portStream(device string) string {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(device, `\`, "/")))
	name = strings.Trim(notStreamChar.ReplaceAllString(name, "-"), "-")
	if name == "" {
		name = "term"
	}
	return name
}

// plainConsole is a console that is not a terminal (tests, pipes).
type plainConsole struct {
	io.Reader
	io.Writer
}

func (plainConsole) MakeRaw() (func() error, error) { return func() error { return nil }, nil }

// crlfWriter ends lines with \r\n, for logs printed in raw mode.
type crlfWriter struct{ w io.Writer }

func (c crlfWriter) Write(p []byte) (int, error) {
	if _, err := c.w.Write(bytes.ReplaceAll(p, []byte("\n"), []byte("\r\n"))); err != nil {
		return 0, err
	}
	return len(p), nil
}
