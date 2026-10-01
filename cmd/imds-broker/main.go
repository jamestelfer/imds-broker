// Package main is the entry point for the imds-broker CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/urfave/cli/v3"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
	"github.com/jamestelfer/imds-broker/pkg/broker"
	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
	"github.com/jamestelfer/imds-broker/pkg/mcpserver"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

func main() {
	app := &cli.Command{
		Name:  "imds-broker",
		Usage: "Serve AWS credentials via the EC2 IMDSv2 or container credentials protocol",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "log-level",
				Usage: "log level: debug, info, warn, error (default: info, or config log-level)",
			},
		},
		Commands: []*cli.Command{
			serveCommand(resolveProfile),
			profilesCommand(),
			mcpCommand(),
			configCommand(),
			doctorCommand(),
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
// for the named command. If extra is non-nil, log records are also written as
// text to that writer. The caller must close the returned io.Closer when done.
func newCommandLogger(cmdName, levelStr string, extra io.Writer) (*slog.Logger, io.Closer, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		return nil, nil, fmt.Errorf("invalid log level %q: %w", levelStr, err)
	}
	lw, err := openLogFile(cmdName)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	opts := &slog.HandlerOptions{Level: level}
	handlers := []slog.Handler{slog.NewJSONHandler(lw, opts)}
	if extra != nil {
		handlers = append(handlers, slog.NewTextHandler(extra, opts))
	}
	return slog.New(slog.NewMultiHandler(handlers...)), lw, nil
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

// effectiveFilter resolves the profile filter using runtime override
// precedence: an explicit --profile-filter flag or IMDS_BROKER_PROFILE_FILTER
// env value wins, otherwise the configured default applies. An empty result
// lets the downstream layer apply the built-in default.
func effectiveFilter(cmd *cli.Command, cfg *brokerconfig.Config) string {
	if cmd.IsSet("profile-filter") {
		return cmd.String("profile-filter")
	}
	return cfg.ProfileFilter
}

// effectiveRegion resolves a region default: an explicit --region flag wins,
// otherwise the configured region applies. An empty result lets the downstream
// layer apply profile-configured behaviour.
func effectiveRegion(cmd *cli.Command, cfg *brokerconfig.Config) string {
	if cmd.IsSet("region") {
		return cmd.String("region")
	}
	return cfg.Region
}

// effectiveLogLevel resolves the root log level: an explicit --log-level wins,
// otherwise the configured log-level applies, otherwise the built-in default.
func effectiveLogLevel(cmd *cli.Command, cfg *brokerconfig.Config) string {
	if cmd.Root().IsSet("log-level") {
		return cmd.Root().String("log-level")
	}
	if cfg.LogLevel != "" {
		return cfg.LogLevel
	}
	return "info"
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
			cfg, err := brokerconfig.Load(ctx)
			if err != nil {
				return fmt.Errorf("profiles: load config: %w", err)
			}

			names, err := profiles.List(ctx, effectiveFilter(cmd, cfg))
			if err != nil {
				return err
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(names)
		},
	}
}

// credentialProvider returns a provider that vends the credentials for cfg.
// If the credentials are already temporary (session token present), they are
// used as-is. Long-term credentials are upgraded via STS GetSessionToken.
func credentialProvider(ctx context.Context, cfg aws.Config, stsClient *sts.Client) (aws.CredentialsProvider, error) {
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieve credentials: %w", err)
	}
	if creds.SessionToken != "" {
		return cfg.Credentials, nil
	}
	return aws.NewCredentialsCache(awscreds.NewSessionTokenProvider(stsClient)), nil
}

// profileCredentials is the AWS state resolved for a single profile.
type profileCredentials struct {
	// Region is the effective region after applying profile configuration.
	Region      string
	Identity    awscreds.CallerIdentity
	Credentials aws.CredentialsProvider
}

// profileResolver resolves credentials for a profile. Production code uses
// resolveProfile; tests inject fakes.
type profileResolver func(ctx context.Context, profile, region string) (profileCredentials, error)

// resolveProfile loads AWS config for profile, validates the credentials via
// STS GetCallerIdentity, and builds the credential provider shared by every
// serving protocol. An empty region defers to the profile configuration.
func resolveProfile(ctx context.Context, profile, region string) (profileCredentials, error) {
	loadOpts := []func(*config.LoadOptions) error{
		config.WithSharedConfigProfile(profile),
	}
	if region != "" {
		loadOpts = append(loadOpts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return profileCredentials{}, fmt.Errorf("load AWS config for profile %q: %w", profile, err)
	}

	stsClient := sts.NewFromConfig(cfg)

	identity, err := awscreds.ResolveCallerIdentity(ctx, stsClient)
	if err != nil {
		return profileCredentials{}, fmt.Errorf("resolve caller identity for profile %q: %w", profile, err)
	}

	creds, err := credentialProvider(ctx, cfg, stsClient)
	if err != nil {
		return profileCredentials{}, fmt.Errorf("build credential provider for profile %q: %w", profile, err)
	}

	return profileCredentials{Region: cfg.Region, Identity: identity, Credentials: creds}, nil
}

// imdsFactory is the broker.ServerFactory used in production. It resolves
// AWS credentials for the given profile and starts an IMDS server.
func imdsFactory(ctx context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (broker.Server, error) {
	pc, err := resolveProfile(ctx, profile, region)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}

	return imdsserver.New(imdsserver.Options{
		Profile:       profile,
		Region:        pc.Region,
		PrincipalName: pc.Identity.PrincipalName,
		AccountID:     pc.Identity.AccountID,
		BindAddrs:     bindAddrs,
		Logger:        logger,
		Credentials:   pc.Credentials,
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
			cfg, err := brokerconfig.Load(ctx)
			if err != nil {
				return fmt.Errorf("mcp: load config: %w", err)
			}

			logger, lw, err := newCommandLogger("mcp", effectiveLogLevel(cmd, cfg), nil)
			if err != nil {
				return err
			}
			defer func() { _ = lw.Close() }()

			pf, err := mcpserver.NewProfileFilter(effectiveFilter(cmd, cfg))
			if err != nil {
				return fmt.Errorf("mcp: invalid profile filter: %w", err)
			}

			b, err := broker.New(ctx, broker.Options{
				Logger:        logger,
				ServerFactory: imdsFactory,
			})
			if err != nil {
				return fmt.Errorf("mcp: create broker: %w", err)
			}

			s := mcpserver.New(mcpserver.Options{
				Broker:        b,
				ListProfiles:  profiles.ListAll, // ProfileFilter is the gate
				Filter:        pf,
				Logger:        logger,
				DefaultRegion: cfg.Region,
			})

			if err := s.ServeStdio(); err != nil {
				logger.Error("MCP server error", "error", err)
			}

			b.StopAll()
			return nil
		},
	}
}
