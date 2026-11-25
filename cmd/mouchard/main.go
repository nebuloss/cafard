// Command mouchard sends to a cafard from any machine with an agent's
// or a device's token: a message, files, a live log, a command's output and
// how it ended. Linux, macOS and Windows. The server is cafard.
//
//	mouchard say|send|log|run PROJECT/CONVERSATION …
//	mouchard version
//
// Everything it does is in internal/client; this is the entry point alone,
// so the client carries none of the server.
package main

import (
	"fmt"
	"io"
	"os"

	"cafard/internal/client"
	"cafard/internal/version"
)

func main() {
	client.Version = version.Version
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, client.Usage)
		return 2
	}
	switch name := args[0]; {
	case client.IsCommand(name):
		if len(args) > 1 && (args[1] == "-h" || args[1] == "-help" || args[1] == "--help") {
			fmt.Fprint(stdout, client.Usage)
			return 0
		}
		return client.Command(name, args[1:], stdin, stdout, stderr)
	case name == "version" || name == "-version" || name == "--version":
		fmt.Fprintln(stdout, "mouchard", version.String())
		return 0
	case name == "help" || name == "-h" || name == "-help" || name == "--help":
		fmt.Fprint(stdout, client.Usage)
		return 0
	default:
		fmt.Fprintf(stderr, "mouchard: unknown command %q\n\n%s", name, client.Usage)
		return 2
	}
}
