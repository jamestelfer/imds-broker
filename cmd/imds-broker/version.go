package main

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/urfave/cli/v3"
)

var version = "dev"

func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print version information",
		Action: func(_ context.Context, _ *cli.Command) error {
			fmt.Printf("imds-broker %s\n", version)

			info, ok := debug.ReadBuildInfo()
			if !ok {
				return nil
			}

			settings := make(map[string]string, len(info.Settings))
			for _, s := range info.Settings {
				settings[s.Key] = s.Value
			}

			if commit, ok := settings["vcs.revision"]; ok {
				suffix := ""
				if settings["vcs.modified"] == "true" {
					suffix = " (modified)"
				}
				fmt.Printf("  commit: %s%s\n", commit, suffix)
			}

			if t, ok := settings["vcs.time"]; ok {
				fmt.Printf("  built:  %s\n", t)
			}

			return nil
		},
	}
}
