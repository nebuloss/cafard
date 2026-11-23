package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"cafard/internal/config"
	"cafard/internal/httpapi/join"
	"cafard/internal/install"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

const adminUsage = `cafard admin: manage people, agents, devices and projects from the box.

Usage:
  cafard admin [-config FILE] COMMAND

Commands:
  create person NAME [-name "Display Name"]       prints their join link
  create agent  NAME [-description TEXT] [-project P]...   prints its install link
  create device NAME [-description TEXT] [-project P]...   prints its token
  create project NAME [-retention DAYS] [-quota SIZE] [-member NAME]...
  add    PROJECT NAME...         put members in a project
  remove PROJECT NAME...         take them out
  link   NAME [-hours N] [-clients claude-code,codex,crush,opencode]
                                 a new one-time link: a person's sign-in link, an
                                 agent's install link; the old one stops working
  token  NAME                    a new token for a device; the old one stops working
  list                           everyone and every project

Every command acts as the admin, the box's operator, whose password opens
the admin UI (cafard passwd). Everything is in the audit log, under "admin",
from "cli".

It works on the database directly, beside a running server. Run it as the
service's user, or as root: it gives the database back to the owner of the
state directory when it is done.
`

func adminCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, adminUsage) }
	path := fs.String("config", "", "configuration file (default "+defaultConfig+" if it exists)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	if *path == "" {
		if _, err := os.Stat(defaultConfig); err == nil {
			*path = defaultConfig
		}
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(stderr, "cafard:", err)
		return 1
	}
	if err := runAdmin(context.Background(), cfg, rest, stdout); err != nil {
		var ref *access.Refusal
		var usage usageError
		switch {
		case errors.As(err, &usage):
			fmt.Fprintf(stderr, "cafard admin: %s\n\n%s", usage, adminUsage)
			return 2
		case errors.As(err, &ref):
			fmt.Fprintln(stderr, "cafard admin:", ref.Message)
		default:
			fmt.Fprintln(stderr, "cafard admin:", err)
		}
		return 1
	}
	return 0
}

type usageError string

func (u usageError) Error() string { return string(u) }

// openState opens the service's database, and returns what to run when done
// with it.
func openState(ctx context.Context, cfg config.Config) (*store.Store, func(), error) {
	// The state directory is the service's, created by it on its first
	// start with its owner and mode. Creating it here, as whoever runs this,
	// would leave the service unable to open its own database.
	if _, err := os.Stat(cfg.StateDir); err != nil {
		return nil, nil, fmt.Errorf("no state directory at %s: start cafard once first (rc-service cafard start), or check -config", cfg.StateDir)
	}
	st, err := store.Open(ctx, filepath.Join(cfg.StateDir, "cafard.db"))
	if err != nil {
		return nil, nil, err
	}
	return st, func() { st.Close(); giveBack(cfg.StateDir) }, nil
}

func runAdmin(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	st, done, err := openState(ctx, cfg)
	if err != nil {
		return err
	}
	defer done()
	a := &adminCmd{st: st, acc: access.New(st), cfg: cfg, out: out, who: access.Admin("cli")}
	switch args[0] {
	case "create":
		if len(args) < 3 {
			return usageError("create what? person, agent, device or project, then a name")
		}
		switch args[1] {
		case "person":
			return a.createPerson(ctx, args[2], args[3:])
		case "agent", "device":
			return a.createMachine(ctx, store.Kind(args[1]), args[2], args[3:])
		case "project":
			return a.createProject(ctx, args[2], args[3:])
		}
		return usageError(fmt.Sprintf("cannot create %q: person, agent, device or project", args[1]))
	case "add", "remove":
		if len(args) < 3 {
			return usageError(args[0] + " needs a project and at least one name")
		}
		return a.membership(ctx, args[0] == "add", args[1], args[2:])
	case "token":
		if len(args) != 2 {
			return usageError("token needs one name")
		}
		return a.token(ctx, args[1])
	case "link":
		if len(args) < 2 {
			return usageError("link needs a name")
		}
		return a.link(ctx, args[1], args[2:])
	case "list":
		return a.list(ctx)
	}
	return usageError(fmt.Sprintf("unknown command %q", args[0]))
}

type adminCmd struct {
	st  *store.Store
	acc *access.Service
	cfg config.Config
	out io.Writer
	who access.Who
}

// printLink makes a one-time link for i and prints it, with a QR code to
// scan from a phone and what to do with it.
//
// clients are the MCP clients an agent's commands set up, by id; none is
// every client its machine has.
func (a *adminCmd) printLink(ctx context.Context, i store.Identity, life time.Duration, clients ...string) error {
	code, l, err := a.acc.IssueLink(ctx, a.who, i, life)
	if err != nil {
		return err
	}
	url := strings.TrimSuffix(a.cfg.URL(), "/") + "/join/" + code
	until := l.ExpiresAt.Local().Format("2 Jan 15:04")
	switch l.Kind {
	case store.AgentLink:
		fmt.Fprintf(a.out, "Run this once on %s's machine, as the user its agents run as (works once, until %s):\n\n", i.Name, until)
		fmt.Fprintf(a.out, "  Linux / macOS:         %s\n", install.Command(url, false, clients...))
		fmt.Fprintf(a.out, "  Windows (PowerShell):  %s\n", install.Command(url, true, clients...))
		return nil
	default:
		fmt.Fprintf(a.out, "%s opens this to sign in, on the device they will use (works once, until %s):\n\n", i.Name, until)
	}
	fmt.Fprintf(a.out, "  %s\n\n", url)
	if q := terminalQR(url); q != "" {
		fmt.Fprintln(a.out, q)
	}
	fmt.Fprintln(a.out, "Never paste it in a conversation: every member, agents included, would see it.")
	return nil
}

func (a *adminCmd) link(ctx context.Context, name string, args []string) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	hours := fs.Int("hours", 24, "how long the link waits, 168 at most")
	only := fs.String("clients", "", "an agent's MCP clients to set up, by id; every one it has by default")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	clients, err := join.ParseClients(*only)
	if err != nil {
		return usageError(err.Error())
	}
	i, err := a.st.IdentityByName(ctx, name)
	if err != nil {
		return fmt.Errorf("no member %q", name)
	}
	return a.printLink(ctx, i, time.Duration(*hours)*time.Hour, clients...)
}

func (a *adminCmd) createPerson(ctx context.Context, name string, args []string) error {
	fs := flag.NewFlagSet("create person", flag.ContinueOnError)
	display := fs.String("name", "", "display name")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	i, err := a.acc.CreateIdentity(ctx, a.who, access.NewIdentity{Kind: store.Person, Name: name, DisplayName: *display})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "created person %s\n\n", i.Name)
	return a.printLink(ctx, i, 0)
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

func (a *adminCmd) projects(ctx context.Context, names []string) ([]store.Project, error) {
	var ps []store.Project
	for _, n := range names {
		p, err := a.st.ProjectByName(ctx, n)
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("no project %q: create it first, with cafard admin create project %s", n, n)
		}
		if err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, nil
}

func (a *adminCmd) createMachine(ctx context.Context, kind store.Kind, name string, args []string) error {
	fs := flag.NewFlagSet("create "+string(kind), flag.ContinueOnError)
	desc := fs.String("description", "", "what the machine can do")
	var projects listFlag
	fs.Var(&projects, "project", "a project to put it in (repeat, or comma-separated)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	ps, err := a.projects(ctx, projects)
	if err != nil {
		return err
	}
	i, err := a.acc.CreateIdentity(ctx, a.who, access.NewIdentity{Kind: kind, Name: name, Description: *desc})
	if err != nil {
		return err
	}
	for _, p := range ps {
		if err := a.acc.AddMember(ctx, a.who, p, i); err != nil {
			return err
		}
	}
	where := "in no project yet"
	if len(projects) > 0 {
		where = "in " + strings.Join(projects, ", ")
	}
	fmt.Fprintf(a.out, "created %s %s, %s\n\n", kind, i.Name, where)
	if kind == store.Agent {
		return a.printLink(ctx, i, 0)
	}
	tok, err := a.acc.IssueToken(ctx, a.who, i)
	if err != nil {
		return err
	}
	a.showToken(kind, tok, projects)
	return nil
}

func (a *adminCmd) showToken(kind store.Kind, tok string, projects []string) {
	url := strings.TrimSuffix(a.cfg.URL(), "/")
	project := "<project>"
	if len(projects) > 0 {
		project = projects[0]
	}
	fmt.Fprintf(a.out, "token, shown once, kept nowhere: %s\n\n", tok)
	fmt.Fprintln(a.out, "Never paste it in a conversation: every member, agents included, would see it.")
	fmt.Fprintln(a.out)
	if kind == store.Device {
		fmt.Fprintf(a.out, "Send a file:\n  curl -T boot.log -H \"Authorization: Bearer %s\" \\\n    %s/api/v1/send/%s/<conversation>/\n\n", tok, url, project)
		fmt.Fprintf(a.out, "Stream a live log:\n  logread -f | curl -T - -H \"Authorization: Bearer %s\" \\\n    %s/api/v1/send/%s/<conversation>/console.log\n", tok, url, project)
		return
	}
	fmt.Fprintf(a.out, "Check it works:\n  curl -H \"Authorization: Bearer %s\" %s/api/v1/me\n\n", tok, url)
	fmt.Fprintln(a.out, "The MCP server comes in a later step; this token is the one its config will carry.")
}

func (a *adminCmd) createProject(ctx context.Context, name string, args []string) error {
	fs := flag.NewFlagSet("create project", flag.ContinueOnError)
	retention := fs.Int("retention", 30, "days files are kept")
	quota := fs.String("quota", "5G", "storage for its files: 500M, 5G, …")
	var members listFlag
	fs.Var(&members, "member", "a member to add (repeat, or comma-separated)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	bytes, err := parseSize(*quota)
	if err != nil {
		return usageError(err.Error())
	}
	var ids []store.Identity
	for _, m := range members {
		i, err := a.st.IdentityByName(ctx, m)
		if err != nil {
			return fmt.Errorf("no member %q", m)
		}
		ids = append(ids, i)
	}
	p, err := a.acc.CreateProject(ctx, a.who, name, *retention, bytes)
	if err != nil {
		return err
	}
	for _, i := range ids {
		if err := a.acc.AddMember(ctx, a.who, p, i); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.out, "created project %s (%d-day retention, %s quota)", p.Name, p.RetentionDays, *quota)
	if len(members) > 0 {
		fmt.Fprintf(a.out, " with %s", strings.Join(members, ", "))
	}
	fmt.Fprintln(a.out)
	return nil
}

// parseSize reads 500M, 5G, 5GB, 1T or a plain number of bytes.
func parseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(strings.ToUpper(s)), "B"))
	mult := int64(1)
	switch {
	case strings.HasSuffix(t, "K"):
		mult, t = 1<<10, strings.TrimSuffix(t, "K")
	case strings.HasSuffix(t, "M"):
		mult, t = 1<<20, strings.TrimSuffix(t, "M")
	case strings.HasSuffix(t, "G"):
		mult, t = 1<<30, strings.TrimSuffix(t, "G")
	case strings.HasSuffix(t, "T"):
		mult, t = 1<<40, strings.TrimSuffix(t, "T")
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a size: use 500M, 5G, …", s)
	}
	return n * mult, nil
}

func (a *adminCmd) membership(ctx context.Context, add bool, project string, names []string) error {
	ps, err := a.projects(ctx, []string{project})
	if err != nil {
		return err
	}
	for _, n := range names {
		i, err := a.st.IdentityByName(ctx, n)
		if err != nil {
			return fmt.Errorf("no member %q", n)
		}
		if add {
			err = a.acc.AddMember(ctx, a.who, ps[0], i)
		} else {
			err = a.acc.RemoveMember(ctx, a.who, ps[0], i)
		}
		if err != nil {
			return err
		}
		verb := "added %s to %s\n"
		if !add {
			verb = "removed %s from %s\n"
		}
		fmt.Fprintf(a.out, verb, n, project)
	}
	return nil
}

func (a *adminCmd) token(ctx context.Context, name string) error {
	i, err := a.st.IdentityByName(ctx, name)
	if err != nil {
		return fmt.Errorf("no member %q", name)
	}
	if i.Kind == store.Agent {
		return errors.New(name + " is an agent: its token is never shown. Make it a new install link: cafard admin link " + name)
	}
	tok, err := a.acc.IssueToken(ctx, a.who, i)
	if err != nil {
		return err
	}
	ps, _ := a.st.ProjectsOf(ctx, i.ID)
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	fmt.Fprintf(a.out, "new token for %s; the old one no longer works\n\n", i.Name)
	a.showToken(i.Kind, tok, names)
	return nil
}

func (a *adminCmd) list(ctx context.Context) error {
	ids, err := a.st.Identities(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tSTATE\tPROJECTS\tLAST USED")
	for _, i := range ids {
		state := "active"
		switch {
		case i.Deleted():
			state = "removed"
		case !i.Enabled:
			state = "paused"
		}
		ps, _ := a.st.ProjectsOf(ctx, i.ID)
		var names []string
		for _, p := range ps {
			names = append(names, p.Name)
		}
		last := "-"
		if u, err := a.st.TokenUse(ctx, i.ID); err == nil && u.LastUsed != nil {
			last = u.LastUsed.Local().Format(time.DateTime) + " from " + u.From
		} else if err == nil {
			last = "never"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", i.Name, i.Kind, state, dash(strings.Join(names, ", ")), last)
	}
	tw.Flush()

	projects, err := a.st.Projects(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out)
	tw = tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tMEMBERS\tRETENTION\tQUOTA\tSTATE")
	for _, p := range projects {
		ms, _ := a.st.Members(ctx, p.ID)
		var names []string
		for _, m := range ms {
			names = append(names, m.Name)
		}
		state := "open"
		if p.Archived() {
			state = "archived"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d days\t%d MB\t%s\n", p.Name, dash(strings.Join(names, ", ")), p.RetentionDays, p.QuotaBytes>>20, state)
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
