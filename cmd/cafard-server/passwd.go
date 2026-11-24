package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/term"

	"cafard/internal/config"
	"cafard/internal/service/access"
)

const passwdUsage = `cafard-server passwd: set the admin password, which opens the admin UI.

Usage:
  cafard-server passwd [-config FILE]

It reads the password from the terminal, twice, or from the first line of
stdin when that is not a terminal (echo 'secret' | cafard-server passwd), and
writes its argon2id hash to the configuration file as admin-password-hash.
Restart cafard for it to take effect; the restart signs every admin out.

It does not ask for the old password: whoever can edit the configuration
can already change everything it protects, and this is how a lost password
is replaced.
`

// passwdCommand is claudication's passwd, with the hash kept in the
// configuration file.
func passwdCommand(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("passwd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, passwdUsage) }
	path := fs.String("config", defaultConfig, "configuration file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := runPasswd(*path, stdin, stdout, stderr); err != nil {
		var ref *access.Refusal
		if errors.As(err, &ref) {
			err = errors.New(ref.Message)
		}
		fmt.Fprintln(stderr, "cafard-server passwd:", err)
		return 1
	}
	return 0
}

func runPasswd(path string, stdin *os.File, stdout, stderr io.Writer) error {
	// The file must already be a configuration cafard accepts: the hash is
	// added to it, never the start of one.
	if _, err := config.Load(path); err != nil {
		return err
	}
	password, err := readPassword(stdin, stderr, "New admin password: ")
	if err != nil {
		return err
	}
	// A pipe has no typo to catch and no second line to read.
	if term.IsTerminal(int(stdin.Fd())) {
		again, err := readPassword(stdin, stderr, "Again: ")
		if err != nil {
			return err
		}
		if again != password {
			return errors.New("the two passwords differ")
		}
	}
	hash, err := access.HashAdminPassword(password)
	if err != nil {
		return err
	}
	if err := writeHash(path, hash); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Admin password set in %s. Restart cafard to use it: rc-service cafard restart.\n", path)
	return nil
}

var hashLine = regexp.MustCompile(`(?m)^#?\s*admin-password-hash:.*$`)

// writeHash puts admin-password-hash in the file, in place of the line that
// was there (the example's comment included), or at the end; the rest of the
// file, comments and all, is left as it is. The new file replaces the old in
// one rename, with its mode, owner and group.
func writeHash(path, hash string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("admin-password-hash: %q", hash)
	text := string(raw)
	if loc := hashLine.FindStringIndex(text); loc != nil {
		text = text[:loc[0]] + line + text[loc[1]:]
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n# The admin password's hash, written by cafard-server passwd.\n" + line + "\n"
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := sameOwner(tmp.Name(), path); err != nil {
		return err
	}
	if _, err := config.Load(tmp.Name()); err != nil {
		return fmt.Errorf("the file would not load with the hash in it: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}

// readPassword reads without echo from a terminal, and the first line of
// anything else, so a provisioning script can pipe it in. The prompt goes
// to stderr.
func readPassword(stdin *os.File, stderr io.Writer, prompt string) (string, error) {
	fd := int(stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(stderr, prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(raw), nil
}
