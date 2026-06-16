// Package brokerconfig loads the protected configuration file from a fixed,
// well-known path outside the broker's command line.
//
// Trust derives solely from the path: the file lives in the host's XDG config
// directory, where a sandboxed agent's filesystem view does not expose it. The
// loader performs no ownership, permission, or signature checks.
package brokerconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// Config holds the recognised settings from the protected configuration file.
// Unknown keys in the file are ignored.
type Config struct {
	// ProfileFilter is the authoritative allow-list regex for AWS profile
	// names. A supplied flag or environment filter may only narrow it.
	ProfileFilter string `yaml:"profile-filter"`
	// Region is a convenience default; an explicit flag overrides it.
	Region string `yaml:"region"`
	// LogLevel is a convenience default; an explicit flag or environment
	// variable overrides it.
	LogLevel string `yaml:"log-level"`
}

// Filter is the effective profile allow-set. A profile is permitted only when
// it matches both the protected filter (from the configuration file) and any
// supplied flag or environment filter. The two predicates are evaluated as a
// logical AND of compiled regexes; a supplied filter can only narrow, never
// widen, the protected filter.
type Filter struct {
	protected *regexp.Regexp
	supplied  *regexp.Regexp
}

// NewFilter composes the protected filter with a supplied flag/env filter.
//
// Where the configuration omits the protected filter and no filter is supplied,
// the supplied predicate falls back to profiles.DefaultFilter. An empty filter
// otherwise imposes no constraint on its side of the AND, so a present protected
// filter alone restricts the set and a supplied filter alone restricts the set.
// Returns an error if either value is not a valid regular expression.
func NewFilter(protected, supplied string) (*Filter, error) {
	if protected == "" && supplied == "" {
		supplied = profiles.DefaultFilter
	}
	pr, err := regexp.Compile(protected)
	if err != nil {
		return nil, fmt.Errorf("invalid protected profile filter %q: %w", protected, err)
	}
	sr, err := regexp.Compile(supplied)
	if err != nil {
		return nil, fmt.Errorf("invalid profile filter %q: %w", supplied, err)
	}
	return &Filter{protected: pr, supplied: sr}, nil
}

// Allowed reports whether name is permitted by both the protected and supplied
// predicates.
func (f *Filter) Allowed(name string) bool {
	return f.protected.MatchString(name) && f.supplied.MatchString(name)
}

// ResolvePath returns the fixed configuration path
// `${XDG_CONFIG_HOME:-$HOME/.config}/imds-broker/config.yaml`. It mirrors the
// XDG resolution used for the log directory and is not overridable by any flag
// or environment variable other than XDG_CONFIG_HOME itself.
func ResolvePath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "imds-broker", "config.yaml"), nil
}

// Load reads and parses the configuration file at path. A missing file yields a
// zero-value Config and no error. A present-but-unreadable or unparseable file
// returns an error so the caller can fail closed.
func Load(path string) (Config, error) {
	// The path is the fixed, well-known configuration location; reading it by
	// design is the basis of the security model, not user-controlled inclusion.
	data, err := os.ReadFile(path) //nolint:gosec // fixed config path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}
