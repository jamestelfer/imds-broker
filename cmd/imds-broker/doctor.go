package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"

	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
	"github.com/jamestelfer/imds-broker/pkg/doctor"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// doctorCommand defines the read-only, non-networked diagnostic command. It
// never constructs AWS service clients or calls AWS APIs. All report-building
// and rendering logic lives in pkg/doctor; this action is CLI wiring only.
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

			cfg, err := brokerconfig.Load(ctx)
			if err != nil {
				// Invalid config: fail before reporting. Exits non-zero.
				return fmt.Errorf("doctor: configuration error: %w", err)
			}

			// Discover local profiles once, without AWS API calls. Filtering and
			// counting happen in-memory inside pkg/doctor.
			discovered, discoveryErr := profiles.ListAll(ctx)

			report, err := doctor.Build(doctorInputs(cmd, cfg), discovered, discoveryErr)
			if err != nil {
				// Effective filter (flag/env) failed to compile: configuration
				// error, distinct from profile discovery failure.
				return fmt.Errorf("doctor: configuration error: %w", err)
			}

			if _, err := io.WriteString(w, doctor.Render(report)); err != nil {
				return fmt.Errorf("doctor: write report: %w", err)
			}
			return nil
		},
	}
}

// doctorInputs reads CLI flag, environment, and config state into the plain
// doctor.Inputs consumed by pkg/doctor.
func doctorInputs(cmd *cli.Command, cfg *brokerconfig.Config) doctor.Inputs {
	filterSet := cmd.IsSet("profile-filter")
	filterValue := cmd.String("profile-filter")

	// A set value originates from the environment when IMDS_BROKER_PROFILE_FILTER
	// is present and equal to the resolved flag value (cli surfaces env values
	// through the flag).
	filterFromEnv := false
	if filterSet {
		if env, ok := os.LookupEnv("IMDS_BROKER_PROFILE_FILTER"); ok && env == filterValue {
			filterFromEnv = true
		}
	}

	return doctor.Inputs{
		ConfigPath:     cfg.Path,
		ConfigFound:    cfg.Found,
		FilterSet:      filterSet,
		FilterValue:    filterValue,
		FilterFromEnv:  filterFromEnv,
		ConfigFilter:   cfg.ProfileFilter,
		ConfigRegion:   cfg.Region,
		LogLevelSet:    cmd.Root().IsSet("log-level"),
		LogLevelValue:  cmd.Root().String("log-level"),
		ConfigLogLevel: cfg.LogLevel,
	}
}
