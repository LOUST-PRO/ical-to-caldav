package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level configuration.
type Config struct {
	// Listen is the address (:8080) the CalDAV HTTP server binds to.
	Listen string `yaml:"listen"`
	// DataDir is where Badger stores its database.
	DataDir string `yaml:"data_dir"`
	// RefreshInterval is how often we re-fetch each iCal source.
	RefreshInterval string `yaml:"refresh_interval"`
	// Sources is the list of iCal subscription feeds.
	Sources []Source `yaml:"sources"`
}

// Source describes a single iCal feed.
type Source struct {
	// ID is a short identifier used in URLs and the store.
	ID string `yaml:"id"`
	// Name is a human-readable label shown in status.
	Name string `yaml:"name"`
	// URL is the public .ics URL to poll.
	URL string `yaml:"url"`
	// Color is an optional hex color for UI display (e.g. "#1a73e8").
	Color string `yaml:"color,omitempty"`
	// RefreshInterval overrides the global RefreshInterval for this source.
	RefreshInterval string `yaml:"refresh_interval,omitempty"`
}

// Interval returns the effective polling interval as time.Duration.
func (s *Source) Interval(global string) time.Duration {
	if s.RefreshInterval != "" {
		d, err := time.ParseDuration(s.RefreshInterval)
		if err == nil {
			return d
		}
	}
	d, err := time.ParseDuration(global)
	if err == nil {
		return d
	}
	return 15 * time.Minute
}

// Load reads and parses a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "~/.local/share/ical-to-caldav"
	}
	if cfg.RefreshInterval == "" {
		cfg.RefreshInterval = "15m"
	}
	return &cfg, nil
}
