package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
)

// configCommand defines the host-side configuration command. It reads and
// writes the broker configuration file directly; it makes no AWS calls and
// starts no servers. The configuration file is host-controlled, not an
// agent-reachable interface: see the project README sandbox model.
func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Inspect and modify the host-side broker configuration file",
		Commands: []*cli.Command{
			configPathCommand(),
			configListCommand(),
			configSetCommand(),
		},
	}
}

// commandWriter returns the root command writer, defaulting to stdout.
func commandWriter(cmd *cli.Command) io.Writer {
	if w := cmd.Root().Writer; w != nil {
		return w
	}
	return os.Stdout
}

func configPathCommand() *cli.Command {
	return &cli.Command{
		Name:  "path",
		Usage: "Print the configuration file location",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			path, err := brokerconfig.ResolvePath(ctx)
			if err != nil {
				return fmt.Errorf("config path: %w", err)
			}
			_, err = io.WriteString(commandWriter(cmd), path+"\n")
			return err
		},
	}
}

func configListCommand() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"show"},
		Usage:   "List the current configuration values",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := brokerconfig.Load(ctx)
			if err != nil {
				return fmt.Errorf("config list: %w", err)
			}

			fileState := "not found (using built-in defaults)"
			if cfg.Found {
				fileState = "found"
			}
			var b strings.Builder
			fmt.Fprintf(&b, "path: %s\n", cfg.Path)
			fmt.Fprintf(&b, "file: %s\n", fileState)
			fmt.Fprintf(&b, "%s: %s\n", brokerconfig.KeyProfileFilter, valueOrUnset(cfg.ProfileFilter))
			fmt.Fprintf(&b, "%s: %s\n", brokerconfig.KeyRegion, valueOrUnset(cfg.Region))
			fmt.Fprintf(&b, "%s: %s\n", brokerconfig.KeyLogLevel, valueOrUnset(cfg.LogLevel))
			_, err = io.WriteString(commandWriter(cmd), b.String())
			return err
		},
	}
}

func configSetCommand() *cli.Command {
	return &cli.Command{
		Name:      "set",
		Usage:     "Set a configuration value (creates the file if absent)",
		ArgsUsage: "<key> <value>",
		Description: "Valid keys: " + brokerconfig.KeyProfileFilter + ", " +
			brokerconfig.KeyRegion + ", " + brokerconfig.KeyLogLevel +
			". An empty value clears the key.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() != 2 {
				return fmt.Errorf("config set: expected <key> <value>, got %d argument(s)", args.Len())
			}
			key, value := args.Get(0), args.Get(1)

			cfg, err := brokerconfig.Set(ctx, key, value)
			if err != nil {
				return fmt.Errorf("config set: %w", err)
			}

			msg := fmt.Sprintf("set %s = %s in %s\n", key, value, cfg.Path)
			if value == "" {
				msg = fmt.Sprintf("cleared %s in %s\n", key, cfg.Path)
			}
			_, err = io.WriteString(commandWriter(cmd), msg)
			return err
		},
	}
}

// valueOrUnset renders an absent configuration value distinctly from an empty
// string set on disk.
func valueOrUnset(v string) string {
	if v == "" {
		return "(unset; built-in default applies)"
	}
	return v
}
