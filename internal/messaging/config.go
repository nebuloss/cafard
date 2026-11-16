package messaging

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Config is the `messaging:` config section.
type Config struct {
	RatePerMinute        int           `yaml:"rate_per_minute"`
	Burst                int           `yaml:"burst"`
	ChannelCreatePerHour int           `yaml:"channel_create_per_hour"`
	ArchiveAfter         time.Duration `yaml:"archive_after"`
}

// SetDefaults implements config.Defaulter.
func (c *Config) SetDefaults() {
	c.RatePerMinute = 30
	c.Burst = 10
	c.ChannelCreatePerHour = 10
	c.ArchiveAfter = 14 * 24 * time.Hour
}

// Validate implements config.Validator.
func (c *Config) Validate() error {
	if c.RatePerMinute < 1 || c.Burst < 1 || c.ChannelCreatePerHour < 1 {
		return errors.New("rate_per_minute, burst and channel_create_per_hour must be at least 1")
	}
	if c.ArchiveAfter < 0 {
		return errors.New("archive_after must not be negative (0 never archives)")
	}
	return nil
}

// ChannelConfig is one entry of the `channels:` list.
type ChannelConfig struct {
	Name    string      `yaml:"name"`
	Title   string      `yaml:"title,omitempty"`
	Topic   string      `yaml:"topic,omitempty"`
	Streams []string    `yaml:"streams,omitempty"`
	Surface dto.Surface `yaml:"surface,omitempty"`
}

// Channels is the `channels:` list. It is a struct around the list so the
// config loader calls its Validate, which it only does for structs.
type Channels struct {
	List []ChannelConfig `yaml:"-"`
}

// UnmarshalYAML reads the list, refusing unknown keys like the rest of
// the file.
func (c *Channels) UnmarshalYAML(n *yaml.Node) error {
	raw, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var list []ChannelConfig
	if err := dec.Decode(&list); err != nil {
		return err
	}
	c.List = nil
	if len(list) > 0 {
		c.List = list
	}
	return nil
}

// MarshalYAML writes the list.
func (c Channels) MarshalYAML() (any, error) {
	if c.List == nil {
		return []ChannelConfig{}, nil
	}
	return c.List, nil
}

// Validate implements config.Validator.
func (c *Channels) Validate() error {
	seen := map[string]bool{}
	for i, ch := range c.List {
		if ch.Name != General {
			if err := ValidateChannelName(ch.Name); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		} else if len(ch.Streams) > 0 || ch.Surface != "" {
			return fmt.Errorf("[%d]: general may only set title and topic", i)
		}
		if seen[ch.Name] {
			return fmt.Errorf("[%d]: channel %s is listed twice", i, ch.Name)
		}
		seen[ch.Name] = true
		for _, p := range ch.Streams {
			if err := validateBinding(p); err != nil {
				return fmt.Errorf("[%d] %s: %w", i, ch.Name, err)
			}
		}
		if ch.Surface != "" && !validSurface(ch.Surface) {
			return fmt.Errorf("[%d] %s: surface %q is not none, markers or errors", i, ch.Name, ch.Surface)
		}
	}
	return nil
}

func validSurface(s dto.Surface) bool {
	return s == dto.SurfaceNone || s == dto.SurfaceMarkers || s == dto.SurfaceErrors
}

func validateBinding(p string) error {
	if err := store.ValidatePattern(p); err != nil {
		return err
	}
	if store.IsMessageStream(p) {
		return fmt.Errorf("%q is a message stream; channels bind log streams only", p)
	}
	return nil
}
