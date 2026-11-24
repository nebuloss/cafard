package client

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

// The commands of mouchard: say, send, log and run, for a machine that
// holds an agent's or a device's token. Each is a curl line of
// docs/design.md, made robust and given an ending a script can act on. They
// print in English, as command lines do here, and work the same on Linux,
// macOS and Windows. cmd/mouchard only calls Command.

// Usage is mouchard's help.
const Usage = `mouchard: send to cafard from this machine, with its agent's or
device's token (CAFARD_URL and CAFARD_TOKEN, or ~/.config/cafard):

  mouchard say  PROJECT/CONVERSATION TEXT...     a message (TEXT, or stdin)
  mouchard send PROJECT/CONVERSATION FILE... [-m TEXT]
                                                      files, in one message
  mouchard log  PROJECT/CONVERSATION NAME [-tee] stdin as a live file:
                                                      tail -f x | mouchard log …
  mouchard run  PROJECT/CONVERSATION [-name F] -- COMMAND [ARGS...]
                                                      run COMMAND, its output as a live
                                                      file, then how it ended; exits as it did
  mouchard version                               print the build

The conversation is created by the first thing sent to it.
`

// IsCommand reports whether name is one of the commands Command runs.
func IsCommand(name string) bool {
	return name == "say" || name == "send" || name == "log" || name == "run"
}

// partSize is how much one upload carries before a log goes on in a new
// file: the server stops a request at 256 MB (api.MaxUpload), and a serial
// console or a long build passes that.
const partSize = 240 << 20

// Command runs one of the commands, with its arguments, and returns the
// process's exit code.
func Command(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c, err := FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 1
	}
	c.Agent = "mouchard/" + name
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch name {
	case "say":
		return sayCommand(ctx, c, args, stdin, stdout, stderr)
	case "send":
		return send(ctx, c, args, stdout, stderr)
	case "log":
		return logCommand(ctx, c, args, stdin, stdout, stderr)
	default:
		return runCommand(c, args, stdout, stderr)
	}
}

// flags parses a helper's flags wherever they are among its arguments, so
// `mouchard send be98/x a.log -m "boot"` works as well as the flag first.
// Everything after "--" is left alone.
//
// The arguments are split at "--" before parsing: the flag package swallows
// the "--" itself, and a command's own flags after it were then read as
// cafard's.
func flags(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i:]
			break
		}
	}
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	return append(pos, tail...), nil
}

func sayCommand(ctx context.Context, c *Client, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	to, err := ParseAddress(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 2
	}
	text := strings.Join(args[1:], " ")
	if text == "" {
		b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			fmt.Fprintln(stderr, "cafard:", err)
			return 1
		}
		text = strings.TrimRight(string(b), "\n")
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(stderr, "cafard: nothing to say")
		return 2
	}
	sent, err := c.Say(ctx, to, text)
	return report(sent, err, stdout, stderr)
}

func send(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	text := fs.String("m", "", "the message's text, sent with the files")
	pos, err := flags(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) < 2 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	to, err := ParseAddress(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 2
	}
	// Every file is opened first: a typo in the third name sends nothing,
	// rather than two files and an error.
	var files []*os.File
	for _, p := range pos[1:] {
		f, err := os.Open(p)
		if err != nil {
			fmt.Fprintln(stderr, "cafard:", err)
			return 1
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || st.IsDir() {
			fmt.Fprintf(stderr, "cafard: %s is not a file\n", p)
			return 1
		}
		files = append(files, f)
	}
	var first Sent
	for i, f := range files {
		st, _ := f.Stat()
		file := File{Name: filepath.Base(f.Name()), Body: f, Size: st.Size()}
		if i == 0 {
			file.Text = *text
		} else {
			file.Message = first.Message.ID
		}
		sent, err := c.Send(ctx, to, file)
		if err != nil {
			fmt.Fprintf(stderr, "cafard: %s: %v\n", file.Name, err)
			return 1
		}
		if i == 0 {
			first = sent
		}
		if sent.Stopped != "" {
			fmt.Fprintf(stderr, "cafard: %s: %s\n", file.Name, sent.Stopped)
			return 1
		}
	}
	return report(first, nil, stdout, stderr)
}

func logCommand(ctx context.Context, c *Client, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tee := fs.Bool("tee", false, "also write what is sent to stdout")
	pos, err := flags(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	to, err := ParseAddress(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 2
	}
	in := stdin
	if *tee {
		in = io.TeeReader(stdin, stdout)
	}
	first, err := stream(ctx, c, to, pos[1], "", in, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 1
	}
	if !*tee {
		return report(first, nil, stdout, stderr)
	}
	return 0
}

// stream sends r as a live file named name, in parts of partSize: name,
// then name.2, name.3 before its extension (build.log, build.2.log). text
// goes with the first part. It returns the first part's message.
func stream(ctx context.Context, c *Client, to Address, name, text string, r io.Reader, stderr io.Writer) (Sent, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var first Sent
	for part := 1; ; part++ {
		if part > 1 {
			// Only go on if there is more: a log that ends on a part's
			// boundary leaves no empty file behind it.
			if _, err := br.Peek(1); err != nil {
				return first, nil
			}
		}
		n := &counter{r: io.LimitReader(br, partSize)}
		f := File{Name: partName(name, part), Body: n, Size: -1}
		if part == 1 {
			f.Text = text
		}
		sent, err := c.Send(ctx, to, f)
		if err != nil {
			return first, fmt.Errorf("%s: %w", f.Name, err)
		}
		if part == 1 {
			first = sent
		}
		if sent.Stopped != "" {
			return first, fmt.Errorf("%s: %s", f.Name, sent.Stopped)
		}
		if n.n < partSize {
			return first, nil
		}
		fmt.Fprintf(stderr, "cafard: %s reached %d MB, going on in %s\n", f.Name, partSize>>20, partName(name, part+1))
	}
}

func partName(name string, part int) string {
	if part == 1 {
		return name
	}
	ext := filepath.Ext(name)
	return fmt.Sprintf("%s.%d%s", strings.TrimSuffix(name, ext), part, ext)
}

type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// runCommand runs a command with its output, stdout and stderr together,
// shown here and sent as a live file; then says how it ended, and exits
// with its code. The command is never held up by cafard: if sending fails,
// its output still reaches the terminal and the command runs to its end.
func runCommand(c *Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "run.log", "the live file's name")
	pos, err := flags(fs, args)
	if err != nil {
		return 2
	}
	dash := -1
	for i, a := range pos {
		if a == "--" {
			dash = i
			break
		}
	}
	if dash != 1 || len(pos) < 3 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	to, err := ParseAddress(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 2
	}
	argv := pos[2:]
	shown := commandLine(argv)

	// Interrupting the run interrupts the command, which shares the
	// terminal; cafard waits for it to end and says so. Caught, not
	// ignored: an ignored signal is inherited across exec on Unix, and the
	// command would no longer stop on Ctrl-C.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	ctx := context.Background()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	start := time.Now()
	if err := cmd.Start(); err != nil {
		_, _ = c.Say(ctx, to, fmt.Sprintf("`%s` could not start: %v", shown, err))
		fmt.Fprintln(stderr, "cafard:", err)
		return 127
	}
	done := make(chan error, 1)
	go func() {
		// What the command writes goes to the terminal and to cafard; once
		// sending fails, to the terminal alone, so the command never blocks
		// on a full pipe.
		_, err := stream(ctx, c, to, *name, "$ "+shown, io.TeeReader(pr, stdout), stderr)
		if err != nil {
			_, _ = io.Copy(stdout, pr)
		}
		done <- err
	}()
	waitErr := cmd.Wait()
	pw.Close()
	sendErr := <-done
	took := Duration(time.Since(start))

	code := 0
	var verdict string
	var exit *exec.ExitError
	switch {
	case waitErr == nil:
		verdict = fmt.Sprintf("`%s` finished after %s", shown, took)
	case errors.As(waitErr, &exit) && exit.ExitCode() >= 0:
		code = exit.ExitCode()
		verdict = fmt.Sprintf("`%s` exited %d after %s", shown, code, took)
	default:
		code = 1
		verdict = fmt.Sprintf("`%s` was stopped after %s: %v", shown, took, waitErr)
	}
	if sendErr != nil {
		fmt.Fprintln(stderr, "cafard: the output was not all sent:", sendErr)
		verdict += " (its output was not all sent: " + sendErr.Error() + ")"
	}
	if _, err := c.Say(ctx, to, verdict); err != nil {
		fmt.Fprintln(stderr, "cafard: could not say how it ended:", err)
	}
	return code
}

// commandLine is argv as a person reads it, quoting what has spaces.
func commandLine(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t'\"") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// report prints where something was sent, or why it was not.
func report(sent Sent, err error, stdout, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 1
	}
	if sent.Created {
		fmt.Fprintf(stdout, "sent to %s (new conversation)\n", sent.Address)
	} else {
		fmt.Fprintf(stdout, "sent to %s\n", sent.Address)
	}
	return 0
}
