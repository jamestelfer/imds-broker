// Package config loads host-side broker configuration from a fixed XDG path.
//
// Configuration supplies defaults for existing broker behaviour. Explicit
// runtime inputs override these defaults. The configuration file is not a
// secret and is not a security boundary by itself: its protection depends on
// the agent sandbox keeping the path out of the agent's reach. See the project
// README for the sandbox deployment model.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// RelPath is the configuration path relative to the XDG config base directory.
const RelPath = "imds-broker/config.yaml"

// Config holds the effective host-side broker configuration. Empty string
// values for ProfileFilter, Region, and LogLevel mean the key was absent and
// the relevant built-in default applies.
type Config struct {
	// Path is the resolved configuration file path.
	Path string
	// Found reports whether a configuration file existed at Path.
	Found bool
	// ProfileFilter is the configured profile-filter regex, or "" if absent.
	ProfileFilter string
	// Region is the configured default region, or "" if absent.
	Region string
	// LogLevel is the configured default log level, or "" if absent.
	LogLevel string
}

// fileSchema mirrors the supported YAML keys. Strict decoding rejects any
// other key.
type fileSchema struct {
	ProfileFilter string `yaml:"profile-filter"`
	Region        string `yaml:"region"`
	LogLevel      string `yaml:"log-level"`
}

// ResolvePath returns the configuration file path. It uses XDG_CONFIG_HOME when
// set, otherwise $HOME/.config. There is no broker-specific path override.
func ResolvePath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, RelPath), nil
}

// Load reads and validates the configuration once. A missing file yields
// built-in defaults without error. A present but unreadable, malformed,
// unknown-key, invalid-regex, or invalid-log-level file fails.
func Load() (*Config, error) {
	path, err := ResolvePath()
	if err != nil {
		return nil, err
	}

	cfg := &Config{Path: path}

	data, err := os.ReadFile(path) //nolint:gosec // path is host-controlled, not agent-controlled
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	cfg.Found = true

	var schema fileSchema
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty file decodes to io.EOF; treat it as built-in defaults.
	if err := dec.Decode(&schema); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	// Reject multi-document files. A single Decode reads only the first
	// document, so additional documents would be silently ignored, including
	// any unknown keys. Fail closed: a multi-document config is an operator
	// mistake.
	if err := dec.Decode(new(fileSchema)); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("parse config %q: %w", path, err)
		}
		return nil, fmt.Errorf("parse config %q: multiple YAML documents are not supported", path)
	}

	if schema.ProfileFilter != "" {
		if _, err := regexp.Compile(schema.ProfileFilter); err != nil {
			return nil, fmt.Errorf("invalid profile-filter regex %q in %q: %w", schema.ProfileFilter, path, err)
		}
	}
	if schema.LogLevel != "" {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(schema.LogLevel)); err != nil {
			return nil, fmt.Errorf("invalid log-level %q in %q: %w", schema.LogLevel, path, err)
		}
	}

	cfg.ProfileFilter = schema.ProfileFilter
	cfg.Region = schema.Region
	cfg.LogLevel = schema.LogLevel
	return cfg, nil
}
