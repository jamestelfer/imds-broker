package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"

	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// sandboxWarning is the operator reminder that the profile filter depends on
// sandbox isolation. It is printed by doctor.
const sandboxWarning = `The profile filter is advisory only unless the agent sandbox keeps host
inputs out of reach. The filter may be ineffective if the sandboxed agent can:
  - read host AWS credentials, config, or SSO caches directly
  - read or edit the broker configuration file
  - alter MCP client configuration
  - set broker command-line flags or environment variables
Verify your container, VM, or sandbox-runtime policy. doctor cannot prove it.`

// doctorReport holds the resolved diagnostic state for rendering.
type doctorReport struct {
	configPath      string
	configFound     bool
	filter          string
	filterSource    string
	region          string
	regionSource    string
	logLevel        string
	logLevelSource  string
	totalProfiles   int
	matchedProfiles int
	discoveryErr    error
}

// doctorCommand defines the read-only, non-networked diagnostic command. It
// never constructs AWS service clients or calls AWS APIs.
func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "Inspect host-side broker configuration and profile discovery",
		Flags: []cli.Flag{
			profileFilterFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			w := cmd.Root().Writer
			if w == nil {
				w = os.Stdout
			}

			cfg, err := brokerconfig.Load()
			if err != nil {
				// Invalid config: fail before reporting. Exits non-zero.
				return fmt.Errorf("doctor: configuration error: %w", err)
			}

			report, err := buildDoctorReport(ctx, cmd, cfg)
			if err != nil {
				// Effective filter (flag/env) failed to compile: configuration
				// error, distinct from profile discovery failure.
				return fmt.Errorf("doctor: configuration error: %w", err)
			}

			if _, err := io.WriteString(w, renderDoctorReport(report)); err != nil {
				return fmt.Errorf("doctor: write report: %w", err)
			}
			return nil
		},
	}
}

// buildDoctorReport resolves effective configuration and discovers local
// profiles without calling AWS APIs. It returns an error only for configuration
// faults (such as an invalid effective filter regex); profile discovery
// failures are recorded on the report and distinguished from config faults.
func buildDoctorReport(ctx context.Context, cmd *cli.Command, cfg *brokerconfig.Config) (doctorReport, error) {
	r := doctorReport{
		configPath:  cfg.Path,
		configFound: cfg.Found,
	}

	r.filter, r.filterSource = resolveFilterSource(cmd, cfg)
	r.region, r.regionSource = resolveRegionSource(cfg)
	r.logLevel, r.logLevelSource = resolveLogLevelSource(cmd, cfg)

	if _, err := regexp.Compile(r.filter); err != nil {
		return r, fmt.Errorf("invalid effective profile filter %q (%s): %w", r.filter, r.filterSource, err)
	}

	all, err := profiles.List(ctx, ".*")
	if err != nil {
		// Discovery failure is recorded on the report and kept distinct from a
		// configuration error; the command still exits zero.
		r.discoveryErr = err
		return r, nil //nolint:nilerr // discovery failure is reported, not a config fault
	}
	r.totalProfiles = len(all)

	matched, err := profiles.List(ctx, r.filter)
	if err != nil {
		r.discoveryErr = err
		return r, nil //nolint:nilerr // discovery failure is reported, not a config fault
	}
	r.matchedProfiles = len(matched)

	return r, nil
}

// resolveFilterSource reports the effective profile filter and where it came
// from, matching the runtime override precedence used by mcp and profiles.
func resolveFilterSource(cmd *cli.Command, cfg *brokerconfig.Config) (string, string) {
	if cmd.IsSet("profile-filter") {
		v := cmd.String("profile-filter")
		if env, ok := os.LookupEnv("IMDS_BROKER_PROFILE_FILTER"); ok && env == v {
			return v, "environment (IMDS_BROKER_PROFILE_FILTER)"
		}
		return v, "command-line flag"
	}
	if cfg.ProfileFilter != "" {
		return cfg.ProfileFilter, "config file"
	}
	return profiles.DefaultFilter, "built-in default"
}

// resolveRegionSource reports the effective default region. doctor takes no
// region flag, so the default is the configured region or profile-configured
// fallback.
func resolveRegionSource(cfg *brokerconfig.Config) (string, string) {
	if cfg.Region != "" {
		return cfg.Region, "config file"
	}
	return "(none; falls back to the profile-configured region)", "built-in default"
}

// resolveLogLevelSource reports the effective log-level default and its source.
func resolveLogLevelSource(cmd *cli.Command, cfg *brokerconfig.Config) (string, string) {
	if cmd.Root().IsSet("log-level") {
		return cmd.Root().String("log-level"), "command-line flag"
	}
	if cfg.LogLevel != "" {
		return cfg.LogLevel, "config file"
	}
	return "info", "built-in default"
}

// renderDoctorReport renders the report as human-readable text. It is not a
// machine interface and never lists matched profile names.
func renderDoctorReport(r doctorReport) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	line("imds-broker doctor")
	line("==================")
	line("")

	line("Configuration")
	line(fmt.Sprintf("  path:  %s", r.configPath))
	if r.configFound {
		line("  file:  found")
	} else {
		line("  file:  not found (using built-in defaults)")
	}
	line("")

	line("Effective defaults")
	line(fmt.Sprintf("  profile filter: %s  (source: %s)", r.filter, r.filterSource))
	line(fmt.Sprintf("  region:         %s  (source: %s)", r.region, r.regionSource))
	line(fmt.Sprintf("  log level:      %s  (source: %s)", r.logLevel, r.logLevelSource))
	line("")

	line("Profile discovery")
	if r.discoveryErr != nil {
		line(fmt.Sprintf("  failed to discover local profiles: %v", r.discoveryErr))
		line("  (this is a profile discovery failure, not a configuration error)")
	} else {
		line(fmt.Sprintf("  discoverable profiles: %d", r.totalProfiles))
		line(fmt.Sprintf("  matched by filter:     %d", r.matchedProfiles))
	}
	line("  Profile names are not listed here. Run 'imds-broker profiles' for JSON output.")
	line("")

	line("Sandbox assumptions")
	line(sandboxWarning)
	return b.String()
}
