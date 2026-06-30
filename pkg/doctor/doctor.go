// Package doctor builds and renders the host-side diagnostic report for the
// `imds-broker doctor` command. It contains no CLI or AWS dependencies: callers
// supply resolved inputs and the discovered profile list, keeping this logic
// importable and independently testable.
package doctor

import (
	"fmt"
	"strings"

	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// sandboxWarning reminds operators that the profile filter depends on sandbox
// isolation.
const sandboxWarning = `The profile filter is advisory only unless the agent sandbox keeps host
inputs out of reach. The filter may be ineffective if the sandboxed agent can:
  - read host AWS credentials, config, or SSO caches directly
  - read or edit the broker configuration file
  - alter MCP client configuration
  - set broker command-line flags or environment variables
Verify your container, VM, or sandbox-runtime policy. doctor cannot prove it.`

// Inputs carries the resolved precedence inputs from the CLI layer. The CLI
// layer reads flags, environment, and configuration; this package resolves the
// effective values and their sources.
type Inputs struct {
	ConfigPath  string
	ConfigFound bool

	// FilterSet reports whether --profile-filter was set (via flag or env).
	FilterSet bool
	// FilterValue is the raw flag/env value when FilterSet is true.
	FilterValue string
	// FilterFromEnv reports whether the set value originated from
	// IMDS_BROKER_PROFILE_FILTER rather than the command-line flag.
	FilterFromEnv bool
	// ConfigFilter is the configured profile-filter, or "" if absent.
	ConfigFilter string

	// ConfigRegion is the configured region default, or "" if absent.
	ConfigRegion string

	// LogLevelSet reports whether --log-level was set.
	LogLevelSet bool
	// LogLevelValue is the --log-level value when LogLevelSet is true.
	LogLevelValue string
	// ConfigLogLevel is the configured log level, or "" if absent.
	ConfigLogLevel string
}

// Report is the resolved diagnostic state ready for rendering.
type Report struct {
	ConfigPath  string
	ConfigFound bool

	Filter       string
	FilterSource string

	Region       string
	RegionSource string

	LogLevel       string
	LogLevelSource string

	TotalProfiles   int
	MatchedProfiles int

	DiscoveryErr error
}

// Build resolves effective configuration and computes profile counts from the
// already-discovered profile list. It returns an error only for a configuration
// fault (an invalid effective filter regex). A non-nil discoveryErr is recorded
// on the report and kept distinct from a configuration fault.
func Build(in Inputs, discovered []profiles.Profile, discoveryErr error) (Report, error) {
	r := Report{
		ConfigPath:  in.ConfigPath,
		ConfigFound: in.ConfigFound,
	}

	r.Filter, r.FilterSource = resolveFilter(in)
	r.Region, r.RegionSource = resolveRegion(in)
	r.LogLevel, r.LogLevelSource = resolveLogLevel(in)

	// Filter validates the effective regex (an invalid filter is a config fault)
	// and reuses the single discovery pass rather than re-implementing matching.
	matched, err := profiles.Filter(discovered, r.Filter)
	if err != nil {
		return r, fmt.Errorf("invalid effective profile filter %q (%s): %w", r.Filter, r.FilterSource, err)
	}

	if discoveryErr != nil {
		// Discovery failure is recorded on the report and kept distinct from a
		// configuration fault; doctor still exits zero.
		r.DiscoveryErr = discoveryErr
		return r, nil //nolint:nilerr // discovery failure is reported, not a config fault
	}

	r.TotalProfiles = len(discovered)
	r.MatchedProfiles = len(matched)

	return r, nil
}

// resolveFilter reports the effective profile filter and its source, mirroring
// the runtime override precedence used by mcp and profiles. An explicitly empty
// filter is normalised to profiles.DefaultFilter so the reported value matches
// what discovery actually uses.
func resolveFilter(in Inputs) (string, string) {
	if in.FilterSet {
		v := in.FilterValue
		if v == "" {
			v = profiles.DefaultFilter
		}
		if in.FilterFromEnv {
			return v, "environment (IMDS_BROKER_PROFILE_FILTER)"
		}
		return v, "command-line flag"
	}
	if in.ConfigFilter != "" {
		return in.ConfigFilter, "config file"
	}
	return profiles.DefaultFilter, "built-in default"
}

// resolveRegion reports the effective region default. doctor takes no region
// flag, so the default is the configured region or the profile-configured
// fallback.
func resolveRegion(in Inputs) (string, string) {
	if in.ConfigRegion != "" {
		return in.ConfigRegion, "config file"
	}
	return "(none; falls back to the profile-configured region)", "built-in default"
}

// resolveLogLevel reports the effective log-level default and its source.
func resolveLogLevel(in Inputs) (string, string) {
	if in.LogLevelSet {
		return in.LogLevelValue, "command-line flag"
	}
	if in.ConfigLogLevel != "" {
		return in.ConfigLogLevel, "config file"
	}
	return "info", "built-in default"
}

// Render renders the report as human-readable text. It is not a machine
// interface and never lists matched profile names.
func Render(r Report) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	line("imds-broker doctor")
	line("==================")
	line("")

	line("Configuration")
	line(fmt.Sprintf("  path:  %s", r.ConfigPath))
	if r.ConfigFound {
		line("  file:  found")
	} else {
		line("  file:  not found (using built-in defaults)")
	}
	line("")

	line("Effective defaults")
	line(fmt.Sprintf("  profile filter: %s  (source: %s)", r.Filter, r.FilterSource))
	line(fmt.Sprintf("  region:         %s  (source: %s)", r.Region, r.RegionSource))
	line(fmt.Sprintf("  log level:      %s  (source: %s)", r.LogLevel, r.LogLevelSource))
	line("")

	line("Profile discovery")
	if r.DiscoveryErr != nil {
		line(fmt.Sprintf("  failed to discover local profiles: %v", r.DiscoveryErr))
		line("  (this is a profile discovery failure, not a configuration error)")
	} else {
		line(fmt.Sprintf("  discoverable profiles: %d", r.TotalProfiles))
		line(fmt.Sprintf("  matched by filter:     %d", r.MatchedProfiles))
	}
	line("  Profile names are not listed here. Run 'imds-broker profiles' for JSON output.")
	line("")

	line("Sandbox assumptions")
	line(sandboxWarning)
	return b.String()
}
