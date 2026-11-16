package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/nebuloss/cafard/internal/config"
)

// cmdConfig loads and validates the configuration exactly as serve would,
// and prints it with secrets redacted. The OpenRC script and the installer
// run it before starting the service.
func cmdConfig(_ context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard config", flag.ContinueOnError)
	loader := config.NewLoader(set)
	if help, err := parseFlags(set, e, args); help || err != nil {
		return err
	}
	cfg, path, err := loader.Load(e.getenv)
	if err != nil {
		return err
	}
	out, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if path == "" {
		fmt.Fprintf(e.stdout, "# no file at %s: defaults, environment and flags\n", config.DefaultPath)
	} else {
		fmt.Fprintf(e.stdout, "# loaded from %s\n", path)
	}
	_, err = e.stdout.Write(out)
	return err
}
