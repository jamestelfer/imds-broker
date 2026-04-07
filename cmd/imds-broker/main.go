// Package main is the entry point for the imds-broker CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/urfave/cli/v3"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
	"github.com/jamestelfer/imds-broker/pkg/mcpserver"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

func main() {
	app := &cli.Command{
		Name:  "imds-broker",
		Usage: "Serve AWS credentials via the EC2 IMDSv2 protocol",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "log-level",
				Value: "info",
				Usage: "log level: debug, info, warn, error",
			},
		},
		Commands: []*cli.Command{
			serveCommand(),
			profilesCommand(),
			mcpCommand(),
			versionCommand(),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// resolveLogDir returns the log directory path using XDG_STATE_HOME if set,
// falling back to $HOME/.local/state.
func resolveLogDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "sandy", "logs", "imds-broker"), nil
}

// newCommandLogger constructs a JSON slog.Logger writing to a rotating log file
// for the named command. The caller must close the returned io.Closer when done.
func newCommandLogger(cmdName, levelStr string) (*slog.Logger, io.Closer, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		return nil, nil, fmt.Errorf("invalid log level %q: %w", levelStr, err)
	}
	lw, err := openLogFile(cmdName)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	return slog.New(slog.NewJSONHandler(lw, &slog.HandlerOptions{Level: level})), lw, nil
}

// openLogFile creates a rotating log file writer for the named command.
// The file is placed in the directory returned by resolveLogDir(), named
// "<cmdName>-<pid>.log". The directory is created if absent.
func openLogFile(cmdName string) (io.WriteCloser, error) {
	dir, err := resolveLogDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create log dir %q: %w", dir, err)
	}
	filename := filepath.Join(dir, fmt.Sprintf("%s-%d.log", cmdName, os.Getpid()))
	return &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    10, // MB
		MaxBackups: 3,
		MaxAge:     7, // days
	}, nil
}

func profileFilterFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    "profile-filter",
		Usage:   "regex to filter AWS profile names",
		Sources: cli.EnvVars("IMDS_BROKER_PROFILE_FILTER"),
	}
}

func profilesCommand() *cli.Command {
	return &cli.Command{
		Name:  "profiles",
		Usage: "List AWS profiles matching the filter",
		Flags: []cli.Flag{
			profileFilterFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			filter := cmd.String("profile-filter")

			names, err := profiles.List(ctx, filter)
			if err != nil {
				return err
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(names)
		},
	}
}

// imdsFactory is the broker.ServerFactory used in production. It loads AWS
// credentials for the given profile, validates them via STS, and starts an
// IMDS server.
func imdsFactory(ctx context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (broker.Server, error) {
	loadOpts := []func(*config.LoadOptions) error{
		config.WithSharedConfigProfile(profile),
	}
	if region != "" {
		loadOpts = append(loadOpts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("mcp: load AWS config for profile %q: %w", profile, err)
	}

	stsClient := sts.NewFromConfig(cfg)

	principalName, err := awscreds.ResolveCallerIdentity(ctx, stsClient)
	if err != nil {
		return nil, fmt.Errorf("mcp: resolve caller identity for profile %q: %w", profile, err)
	}

	credProvider := aws.NewCredentialsCache(awscreds.NewSessionTokenProvider(stsClient))

	return imdsserver.New(imdsserver.Options{
		Profile:       profile,
		Region:        cfg.Region,
		PrincipalName: principalName,
		BindAddrs:     bindAddrs,
		Logger:        logger,
		Credentials:   credProvider,
	})
}

func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "Start an MCP server for managing IMDS servers over stdio",
		Flags: []cli.Flag{
			profileFilterFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger, lw, err := newCommandLogger("mcp", cmd.Root().String("log-level"))
			if err != nil {
				return err
			}
			defer func() { _ = lw.Close() }()

			filter := cmd.String("profile-filter")

			b, err := broker.New(ctx, broker.Options{
				Logger:        logger,
				ServerFactory: imdsFactory,
			})
			if err != nil {
				return fmt.Errorf("mcp: create broker: %w", err)
			}

			s := mcpserver.New(mcpserver.Options{
				Broker:        b,
				ListProfiles:  profiles.List,
				ProfileFilter: filter,
				Logger:        logger,
			})

			if err := s.ServeStdio(); err != nil {
				logger.Error("MCP server error", "error", err)
			}

			b.StopAll()
			return nil
		},
	}
}

func serveCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Start an IMDS server for a single AWS profile",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "profile",
				Usage:    "AWS profile name",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "region",
				Usage: "AWS region (defaults to the profile-configured region)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger, lw, err := newCommandLogger("serve", cmd.Root().String("log-level"))
			if err != nil {
				return err
			}
			defer func() { _ = lw.Close() }()

			profile := cmd.String("profile")
			region := cmd.String("region")

			// Cancel on SIGINT/SIGTERM.
			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Load AWS config for the specified profile.
			loadOpts := []func(*config.LoadOptions) error{
				config.WithSharedConfigProfile(profile),
			}
			if region != "" {
				loadOpts = append(loadOpts, config.WithRegion(region))
			}

			cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
			if err != nil {
				return fmt.Errorf("serve: load AWS config for profile %q: %w", profile, err)
			}

			stsClient := sts.NewFromConfig(cfg)

			// Resolve the principal name and validate credentials at startup.
			principalName, err := awscreds.ResolveCallerIdentity(ctx, stsClient)
			if err != nil {
				return fmt.Errorf("serve: resolve caller identity: %w", err)
			}

			// Upgrade static credentials to temporary via STS GetSessionToken.
			// CredentialsCache handles refresh near expiry.
			credProvider := aws.NewCredentialsCache(awscreds.NewSessionTokenProvider(stsClient))

			srv, err := imdsserver.New(imdsserver.Options{
				Profile:       profile,
				Region:        cfg.Region,
				PrincipalName: principalName,
				BindAddrs:     []string{"127.0.0.1:0"},
				Logger:        logger,
				Credentials:   credProvider,
			})
			if err != nil {
				return fmt.Errorf("serve: start server: %w", err)
			}
			defer srv.Stop()

			for _, u := range srv.URLs() {
				logger.Info("IMDS server listening", "url", u, "profile", profile)
			}

			select {
			case <-ctx.Done():
				logger.Info("shutting down")
			case <-srv.Done():
				logger.Error("IMDS server exited unexpectedly")
			}
			return nil
		},
	}
}
