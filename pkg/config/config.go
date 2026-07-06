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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// RelPath is the configuration path relative to the XDG config base directory.
const RelPath = "imds-broker/config.yaml"

// Configuration key names accepted by Set and reported by List.
const (
	KeyProfileFilter = "profile-filter"
	KeyRegion        = "region"
	KeyLogLevel      = "log-level"
)

// Keys lists the settable configuration keys in file order.
var Keys = []string{KeyProfileFilter, KeyRegion, KeyLogLevel}

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
// other key. omitempty keeps cleared keys out of the written file.
type fileSchema struct {
	ProfileFilter string `yaml:"profile-filter,omitempty"`
	Region        string `yaml:"region,omitempty"`
	LogLevel      string `yaml:"log-level,omitempty"`
}

// validate checks a decoded schema against the value rules Load and Set share.
// Errors are path-free; callers add file context where the value originates
// from a file (Load) but not where it originates from the caller (Set).
func validate(schema fileSchema) error {
	if schema.ProfileFilter != "" {
		if _, err := regexp.Compile(schema.ProfileFilter); err != nil {
			return fmt.Errorf("invalid profile-filter regex %q: %w", schema.ProfileFilter, err)
		}
	}
	if schema.LogLevel != "" {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(schema.LogLevel)); err != nil {
			return fmt.Errorf("invalid log-level %q: %w", schema.LogLevel, err)
		}
	}
	return nil
}

// decodeFile reads and strictly decodes the configuration file at path. A
// missing file yields a zero schema with found=false. A present but malformed,
// unknown-key, or multi-document file fails.
func decodeFile(path string) (schema fileSchema, found bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is host-controlled, not agent-controlled
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fileSchema{}, false, nil
		}
		return fileSchema{}, false, fmt.Errorf("read config %q: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty file decodes to io.EOF; treat it as built-in defaults.
	if err := dec.Decode(&schema); err != nil && !errors.Is(err, io.EOF) {
		return fileSchema{}, false, fmt.Errorf("parse config %q: %w", path, err)
	}
	// Reject multi-document files. A single Decode reads only the first
	// document, so additional documents would be silently ignored, including
	// any unknown keys. Fail closed: a multi-document config is an operator
	// mistake.
	if err := dec.Decode(new(fileSchema)); !errors.Is(err, io.EOF) {
		if err != nil {
			return fileSchema{}, false, fmt.Errorf("parse config %q: %w", path, err)
		}
		return fileSchema{}, false, fmt.Errorf("parse config %q: multiple YAML documents are not supported", path)
	}
	return schema, true, nil
}

// ResolvePath returns the configuration file path. It uses XDG_CONFIG_HOME when
// set, otherwise $HOME/.config. There is no broker-specific path override.
//
// ctx is accepted for call-chain consistency; path resolution itself does not
// block on it.
func ResolvePath(_ context.Context) (string, error) {
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
func Load(ctx context.Context) (*Config, error) {
	path, err := ResolvePath(ctx)
	if err != nil {
		return nil, err
	}

	cfg := &Config{Path: path}

	schema, found, err := decodeFile(path)
	if err != nil {
		return nil, err
	}
	if !found {
		return cfg, nil
	}
	cfg.Found = true

	if err := validate(schema); err != nil {
		return nil, fmt.Errorf("config %q: %w", path, err)
	}

	cfg.ProfileFilter = schema.ProfileFilter
	cfg.Region = schema.Region
	cfg.LogLevel = schema.LogLevel
	return cfg, nil
}

// Set updates a single configuration key on disk and returns the reloaded
// configuration. It creates the file and its parent directory if absent and
// preserves other keys. An empty value clears the key. Setting an existing but
// malformed file fails rather than overwriting operator content.
//
// This command is host-side and writes a host-controlled path. It is not an
// agent-reachable interface: see the package doc and README sandbox model.
func Set(ctx context.Context, key, value string) (*Config, error) {
	path, err := ResolvePath(ctx)
	if err != nil {
		return nil, err
	}

	schema, _, err := decodeFile(path)
	if err != nil {
		return nil, err
	}

	switch key {
	case KeyProfileFilter:
		schema.ProfileFilter = value
	case KeyRegion:
		schema.Region = value
	case KeyLogLevel:
		schema.LogLevel = value
	default:
		return nil, fmt.Errorf("unknown configuration key %q; valid keys: %s", key, strings.Join(Keys, ", "))
	}

	if err := validate(schema); err != nil {
		return nil, err
	}

	data, err := yaml.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("write config %q: %w", path, err)
	}

	return Load(ctx)
}
