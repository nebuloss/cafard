package projects_test

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/store"
)

var defaults = store.Limits{LinesMaxAge: 14 * 24 * time.Hour, LinesMaxBytes: 2e9, MessagesMaxAge: 90 * 24 * time.Hour, BlobsMaxBytes: 5e9}

func open(t *testing.T) (*projects.Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := projects.New(context.Background(), st.DB(), clock.NewFake(time.Unix(1e9, 0)), defaults, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func principal(t *testing.T, st *store.Store, name, kind string, scopes ...auth.Scope) auth.Principal {
	t.Helper()
	tokens := auth.NewTokens(st.DB(), clock.NewFake(time.Unix(1e9, 0)))
	if _, err := tokens.EnsurePrincipal(context.Background(), name, kind); err != nil {
		t.Fatal(err)
	}
	return auth.Principal{Name: name, Kind: kind, Scopes: auth.NewScopeSet(scopes...)}
}

func TestCreateTakesTheConfigRetention(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	var made []string
	s.OnCreate(func(_ context.Context, name string) error { made = append(made, name); return nil })
	p, err := s.Create(ctx, "lab", "The lab")
	must(t, err)
	if p.Retention != defaults || !slices.Equal(made, []string{"lab"}) {
		t.Errorf("project = %+v, hooks ran for %v", p, made)
	}
	if _, err := s.Create(ctx, "lab", ""); err == nil {
		t.Error("a second lab was made")
	}
	if _, err := s.Create(ctx, "Lab!", ""); err == nil {
		t.Error("a bad name was taken")
	}
	must(t, s.SetRetention(ctx, "lab", store.Limits{LinesMaxAge: time.Hour}))
	lim, err := s.Limits(ctx)
	must(t, err)
	if lim["lab"] != (store.Limits{LinesMaxAge: time.Hour}) {
		t.Errorf("limits = %+v", lim)
	}
}

func TestNullRetentionIsFilledFromTheConfig(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	must(t, err)
	defer st.Close()
	_, err = st.DB().Exec(`INSERT INTO projects(name, created_at) VALUES ('imported', 1)`)
	must(t, err)
	s, err := projects.New(context.Background(), st.DB(), clock.NewFake(time.Unix(1e9, 0)), defaults, slog.New(slog.NewTextHandler(io.Discard, nil)))
	must(t, err)
	p, err := s.Project(context.Background(), "imported")
	must(t, err)
	if p.Retention != defaults {
		t.Errorf("retention = %+v", p.Retention)
	}
}

func TestParseGrant(t *testing.T) {
	g, err := projects.ParseGrant("lab:dut1=read,message")
	must(t, err)
	if g.Project != "lab" || g.Channel != "dut1" || g.Scopes.String() != "read message" || g.Spec() != "lab:dut1=read,message" {
		t.Errorf("grant = %+v", g)
	}
	for _, bad := range []string{"lab", "lab=", "lab=admin", "lab:dut1=ingest", "Lab=read", "lab:Bad=read"} {
		if _, err := projects.ParseGrant(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// grants sets up lab with #dut1 bound to serial:lab/dut1, and other.
func grants(t *testing.T) (*projects.Service, *store.Store) {
	t.Helper()
	s, st := open(t)
	ctx := context.Background()
	for _, name := range []string{"lab", "other"} {
		_, err := s.Create(ctx, name, "")
		must(t, err)
	}
	_, err := st.DB().Exec(`INSERT INTO channels(name, created_by, created_at, last_activity) VALUES ('lab/dut1', 'x', 1, 1), ('lab/general', 'x', 1, 1)`)
	must(t, err)
	_, err = st.DB().Exec(`INSERT INTO channel_streams(channel, pattern) VALUES ('lab/dut1', 'serial:lab/dut1')`)
	must(t, err)
	return s, st
}

func TestNoGrantSeesNothing(t *testing.T) {
	s, st := grants(t)
	p := principal(t, st, "codex", auth.KindAgent, auth.ScopeRead, auth.ScopeMessage)
	a := s.Access(context.Background(), p)
	for _, stream := range []string{"chan:lab/general", "serial:lab/dut1", "chan:other/general", "serial:dut1", "dm:a~b"} {
		if a.Can(stream, auth.ScopeRead) {
			t.Errorf("%s readable without a grant", stream)
		}
	}
	if len(a.LogPatterns()) != 0 || len(a.ChannelPatterns()) != 0 || len(a.Projects()) != 0 {
		t.Errorf("patterns %v %v, projects %v", a.LogPatterns(), a.ChannelPatterns(), a.Projects())
	}
	if !a.Can("dm:codex~x", auth.ScopeRead) {
		t.Error("a party cannot read its own direct conversation")
	}
}

func TestChannelGrantSeesThatChannelAndItsBindings(t *testing.T) {
	s, st := grants(t)
	ctx := context.Background()
	p := principal(t, st, "codex", auth.KindAgent, auth.ScopeRead, auth.ScopeMessage, auth.ScopeSerialWrite)
	must(t, s.SetGrant(ctx, projects.Grant{Principal: "codex", Project: "lab", Channel: "dut1", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeSerialWrite)}))
	a := s.Access(ctx, p)
	if !a.Can("chan:lab/dut1", auth.ScopeRead) || a.Can("chan:lab/dut1", auth.ScopeMessage) {
		t.Error("channel scopes are wrong")
	}
	if !a.Can("serial:lab/dut1", auth.ScopeRead) || !a.Can("serial:lab/dut1", auth.ScopeSerialWrite) {
		t.Error("the bound stream is not covered")
	}
	if a.Can("chan:lab/general", auth.ScopeRead) || a.Can("syslog:lab/dut1", auth.ScopeRead) || a.Can("serial:other/dut1", auth.ScopeRead) {
		t.Error("a channel grant reaches beyond its channel")
	}
	if a.ProjectScopes("lab") != nil {
		t.Error("a channel grant gives project scopes")
	}
	if got := strings.Join(a.LogPatterns(), " "); got != "serial:lab/dut1" {
		t.Errorf("log patterns = %q", got)
	}
}

func TestProjectGrantIsCappedByTheCredential(t *testing.T) {
	s, st := grants(t)
	ctx := context.Background()
	readOnly := principal(t, st, "codex", auth.KindAgent, auth.ScopeRead)
	must(t, s.SetGrant(ctx, projects.Grant{Principal: "codex", Project: "lab", Scopes: projects.GrantScopes}))
	a := s.Access(ctx, readOnly)
	if !a.Can("chan:lab/general", auth.ScopeRead) || !a.Can("syslog:lab/x", auth.ScopeRead) || a.Can("chan:lab/general", auth.ScopeMessage) {
		t.Error("a read-only token is not capped")
	}
	if a.Can("chan:other/general", auth.ScopeRead) {
		t.Error("lab's grant reaches other")
	}
	if got := strings.Join(a.LogPatterns(), " "); got != "*:lab/*" {
		t.Errorf("log patterns = %q", got)
	}
	admin := s.Access(ctx, auth.Principal{Name: auth.AdminName, Scopes: auth.AdminScopes})
	if !admin.All() || admin.LogPatterns() != nil || !admin.Can("chan:other/general", auth.ScopeRead) {
		t.Error("the admin does not see everything")
	}
	sink := s.Access(ctx, auth.Principal{Name: "codex", Scopes: auth.NewScopeSet(auth.ScopeIngest, auth.ScopeRead), Sink: "lab/x"})
	if sink.Can("chan:lab/general", auth.ScopeRead) {
		t.Error("a sink token reads")
	}
}

func TestGrantsAreSeenAtOnce(t *testing.T) {
	s, st := grants(t)
	ctx := context.Background()
	p := principal(t, st, "codex", auth.KindAgent, auth.ScopeRead)
	if s.Access(ctx, p).Can("chan:lab/general", auth.ScopeRead) {
		t.Fatal("readable before the grant")
	}
	must(t, s.AddGrants(ctx, projects.Grant{Principal: "codex", Project: "lab", Scopes: auth.NewScopeSet(auth.ScopeRead)}))
	if !s.Access(ctx, p).Can("chan:lab/general", auth.ScopeRead) {
		t.Error("the cached access kept the old grants")
	}
	gs, err := s.Grants(ctx, "lab", "")
	must(t, err)
	must(t, s.DeleteGrant(ctx, "lab", gs[0].ID))
	if s.Access(ctx, p).Can("chan:lab/general", auth.ScopeRead) {
		t.Error("readable after the grant was removed")
	}
	if err := s.AddGrants(ctx, projects.Grant{Principal: "codex", Project: "nope", Scopes: auth.NewScopeSet(auth.ScopeRead)}); err == nil {
		t.Error("a grant on a missing project was stored")
	}
}

func TestSinkForAddrPrefersTheMostSpecificPrefix(t *testing.T) {
	s, _ := grants(t)
	ctx := context.Background()
	wide, _ := projects.ParseSources("10.0.50.0/24")
	narrow, _ := projects.ParseSources("10.0.50.42")
	_, err := s.SaveSink(ctx, projects.Sink{Project: "lab", Name: "rack", Sources: wide})
	must(t, err)
	_, err = s.SaveSink(ctx, projects.Sink{Project: "other", Name: "dut1", Sources: narrow})
	must(t, err)
	for ip, want := range map[string]string{"10.0.50.42": "other/dut1", "10.0.50.7": "lab/rack", "::ffff:10.0.50.7": "lab/rack"} {
		k, ok := s.SinkForAddr(netip.MustParseAddr(ip))
		if !ok || k.Ref() != want {
			t.Errorf("%s lands in %v %v, want %s", ip, k.Ref(), ok, want)
		}
	}
	if _, ok := s.SinkForAddr(netip.MustParseAddr("192.168.1.1")); ok {
		t.Error("an unknown sender has a sink")
	}
	must(t, s.DeleteSink(ctx, "other", "dut1"))
	if k, _ := s.SinkForAddr(netip.MustParseAddr("10.0.50.42")); k.Ref() != "lab/rack" {
		t.Errorf("after delete: %s", k.Ref())
	}
}

func TestSinkForIngest(t *testing.T) {
	s, st := grants(t)
	ctx := context.Background()
	for _, ref := range []string{"lab/dut1", "other/dut1", "lab/solo"} {
		project, name, _ := projects.SplitRef(ref)
		_, err := s.SaveSink(ctx, projects.Sink{Project: project, Name: name})
		must(t, err)
	}
	p := principal(t, st, "pusher", auth.KindDevice, auth.ScopeIngest)
	if _, ok := s.SinkForIngest(ctx, p, "dut1"); ok {
		t.Error("ingest without a grant")
	}
	must(t, s.SetGrant(ctx, projects.Grant{Principal: "pusher", Project: "lab", Scopes: auth.NewScopeSet(auth.ScopeIngest)}))
	for name, want := range map[string]string{"dut1": "lab/dut1", "push:solo": "lab/solo", "lab/dut1": "lab/dut1"} {
		if k, ok := s.SinkForIngest(ctx, p, name); !ok || k.Ref() != want {
			t.Errorf("%s goes to %s %v, want %s", name, k.Ref(), ok, want)
		}
	}
	for _, name := range []string{"other/dut1", "missing"} {
		if k, ok := s.SinkForIngest(ctx, p, name); ok {
			t.Errorf("%s goes to %s", name, k.Ref())
		}
	}
	must(t, s.SetGrant(ctx, projects.Grant{Principal: "pusher", Project: "other", Scopes: auth.NewScopeSet(auth.ScopeIngest)}))
	if _, ok := s.SinkForIngest(ctx, p, "dut1"); ok {
		t.Error("an ambiguous bare name was routed")
	}
	bound := auth.Principal{Name: "pusher", Scopes: auth.NewScopeSet(auth.ScopeIngest), Sink: "other/dut1"}
	if k, ok := s.SinkForIngest(ctx, bound, "lab/dut1"); !ok || k.Ref() != "other/dut1" {
		t.Errorf("a sink token went to %s", k.Ref())
	}
}

func TestSinkSerialPortIsValidated(t *testing.T) {
	s, _ := grants(t)
	ctx := context.Background()
	if _, err := s.SaveSink(ctx, projects.Sink{Project: "lab", Name: "dut1", Serial: &projects.SerialPort{TCP: "nohost"}}); err == nil {
		t.Error("a bad port was saved")
	}
	k, err := s.SaveSink(ctx, projects.Sink{Project: "lab", Name: "dut1", Serial: &projects.SerialPort{Device: "/dev/ttyUSB0"}})
	must(t, err)
	if k.Serial == nil || k.Serial.Name != "lab/dut1" || k.Serial.Baud != projects.DefaultBaud || k.Serial.Stream() != "serial:lab/dut1" {
		t.Errorf("port = %+v", k.Serial)
	}
	ports := s.SerialPorts()
	if len(ports) != 1 || ports[0].Device != "/dev/ttyUSB0" {
		t.Errorf("ports = %+v", ports)
	}
}

func TestImportConfigOnce(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	ports := []projects.SerialPort{{Name: "dut1", Device: "/dev/ttyUSB0", Baud: 115200, DataBits: 8, Parity: "none", StopBits: 1}}
	names := map[string]string{"10.0.50.42": "dut1", "10.0.50.43": "dut2"}
	warn, err := s.ImportConfig(ctx, ports, names)
	must(t, err)
	if len(warn) != 2 {
		t.Errorf("warnings = %v", warn)
	}
	ks, err := s.Sinks(ctx, projects.Imported)
	must(t, err)
	if len(ks) != 2 || ks[0].Serial == nil || ks[0].Sources[0].String() != "10.0.50.42/32" || ks[1].Name != "dut2" {
		t.Fatalf("sinks = %+v", ks)
	}
	must(t, s.DeleteSink(ctx, projects.Imported, "dut2"))
	_, err = s.ImportConfig(ctx, ports, names)
	must(t, err)
	ks, _ = s.Sinks(ctx, projects.Imported)
	if len(ks) != 1 {
		t.Errorf("a deleted sink came back: %+v", ks)
	}
}

func TestAPairingLinkGivesItsGrants(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	_, err := s.Create(ctx, "lab", "")
	must(t, err)
	_, err = st.DB().Exec(`INSERT INTO channels(name, created_by, created_at, last_activity) VALUES ('lab/dut1', 'admin', 1, 1)`)
	must(t, err)
	clk := clock.NewFake(time.Unix(1e9, 0))
	pairings := auth.NewPairings(st.DB(), clk, auth.NewSessions(st.DB(), clk), s)
	for _, bad := range []string{"nowhere=read", "lab:nope=read", "lab=admin"} {
		if _, _, err := pairings.Create(ctx, "", "phone", nil, bad, "admin", 0); err == nil {
			t.Errorf("a link with grant %q was made", bad)
		}
	}
	_, code, err := pairings.Create(ctx, "", "phone", nil, "lab:dut1=read,message", "admin", 0)
	must(t, err)
	p := auth.Principal{Name: "phone", Kind: auth.KindHuman, Scopes: auth.PairingScopes}
	if s.Access(ctx, p).CanChannel("lab/dut1", auth.ScopeRead) {
		t.Fatal("the grant is there before the link is opened")
	}
	if _, _, err := pairings.Redeem(ctx, code, ""); err != nil {
		t.Fatal(err)
	}
	a := s.Access(ctx, p)
	if !a.CanChannel("lab/dut1", auth.ScopeMessage) || a.CanChannel("lab/general", auth.ScopeRead) {
		t.Errorf("after redeeming: dut1 %v, general %v", a.Scopes("chan:lab/dut1"), a.Scopes("chan:lab/general"))
	}
}
