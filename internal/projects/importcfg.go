package projects

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"

	"github.com/nebuloss/cafard/internal/auth"
)

// ImportConfig turns the old config keys into sinks of the project
// imported: each serial port becomes the port of the sink of its name,
// each netconsole name a source address of the sink of that name. Each
// item is imported once, so a sink the admin deleted stays deleted. It
// returns a sentence per key still in the file, for a warning.
func (s *Service) ImportConfig(ctx context.Context, ports []SerialPort, names map[string]string) ([]string, error) {
	for _, p := range ports {
		port := p
		err := s.importItem(ctx, "serial:"+p.Name, p.Name, func(k *Sink) {
			if k.Serial == nil {
				k.Serial = &port
			}
		})
		if err != nil {
			return nil, err
		}
	}
	addrs := make([]string, 0, len(names))
	for addr := range names {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		ip, err := netip.ParseAddr(addr)
		if err != nil {
			continue
		}
		ip = ip.Unmap().WithZone("")
		err = s.importItem(ctx, "netconsole:"+ip.String(), names[addr], func(k *Sink) {
			k.Sources = append(k.Sources, netip.PrefixFrom(ip, ip.BitLen()))
		})
		if err != nil {
			return nil, err
		}
	}
	var warn []string
	if len(ports) > 0 {
		warn = append(warn, fmt.Sprintf("the serial list of the config is imported into sinks of the project %s; remove it from the file and manage the ports on the project page", Imported))
	}
	if len(names) > 0 {
		warn = append(warn, fmt.Sprintf("netconsole.names in the config is imported into sinks of the project %s; remove it from the file and manage the source addresses on the project page", Imported))
	}
	return warn, nil
}

func (s *Service) importItem(ctx context.Context, key, sink string, apply func(*Sink)) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM config_imports WHERE key = ?`, key).Scan(&n); err != nil || n > 0 {
		return err
	}
	if auth.ValidateName(sink) != nil {
		s.log.Warn("not imported: the name cannot be a sink name (1 to 40 of a-z, 0-9, '.', '_', '-')", "item", key, "name", sink)
		return nil
	}
	if _, err := s.Ensure(ctx, Imported, "Imported"); err != nil {
		return err
	}
	k, err := s.Sink(ctx, Imported, sink)
	if errors.Is(err, ErrNotFound) {
		k, err = Sink{Project: Imported, Name: sink}, nil
	}
	if err != nil {
		return err
	}
	apply(&k)
	if _, err := s.SaveSink(ctx, k); err != nil {
		return fmt.Errorf("importing %s: %w", key, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO config_imports(key, at) VALUES (?, ?) ON CONFLICT DO NOTHING`, key, s.clock.Now().UnixNano())
	if err == nil {
		s.log.Info("imported from the config", "item", key, "sink", k.Ref())
	}
	return err
}
