package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

const (
	protocolIMDS      = "imds"
	protocolContainer = "container"
)

// serveOptions holds the validated serve flags that select and bind a
// protocol server.
type serveOptions struct {
	protocol  string
	bindAddr  string
	tokenFile string
	path      string
}

// parseServeOptions validates protocol-related flags. It runs before any AWS
// configuration is loaded or any listener is bound, so configuration errors
// fail fast.
func parseServeOptions(cmd *cli.Command) (serveOptions, error) {
	opts := serveOptions{
		protocol:  cmd.String("protocol"),
		tokenFile: cmd.String("token-file"),
		path:      cmd.String("path"),
	}

	port := cmd.Int("port")
	if port < 0 || port > 65535 {
		return serveOptions{}, fmt.Errorf("--port must be between 0 and 65535, got %d", port)
	}
	opts.bindAddr = net.JoinHostPort(cmd.String("bind"), strconv.Itoa(port))

	switch opts.protocol {
	case protocolIMDS:
		if cmd.IsSet("token-file") {
			return serveOptions{}, errors.New("--token-file requires --protocol container")
		}
		if cmd.IsSet("path") {
			return serveOptions{}, errors.New("--path requires --protocol container")
		}
	case protocolContainer:
		if opts.tokenFile == "" {
			return serveOptions{}, errors.New("--protocol container requires --token-file")
		}
	default:
		return serveOptions{}, fmt.Errorf("unknown --protocol %q: must be %q or %q", opts.protocol, protocolIMDS, protocolContainer)
	}

	return opts, nil
}

func serveCommand(resolve profileResolver) *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Serve credentials for a single AWS profile over the IMDS or container credentials protocol",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:      "profile",
				Usage:     "AWS profile name",
				Required:  true,
				Validator: profiles.ValidateName,
			},
			&cli.StringFlag{
				Name:  "region",
				Usage: "AWS region (defaults to the profile-configured region)",
			},
			&cli.StringFlag{
				Name:  "protocol",
				Usage: "credential protocol: imds (EC2 IMDSv2) or container (AWS_CONTAINER_CREDENTIALS_FULL_URI)",
				Value: protocolIMDS,
			},
			&cli.StringFlag{
				Name: "token-file",
				Usage: "container protocol only (required): path the authorisation token is written to, mode 0600. " +
					"Clients reading it via AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE must run as the broker's UID or root",
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "container protocol only: credentials path",
				Value: containercreds.DefaultPath,
			},
			&cli.StringFlag{
				Name:  "bind",
				Usage: "address to listen on",
				Value: "0.0.0.0",
			},
			&cli.IntFlag{
				Name:  "port",
				Usage: "TCP port to listen on (0 selects an ephemeral port)",
			},
			&cli.BoolFlag{
				Name:  "quiet",
				Usage: "suppress log output to stderr",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			opts, err := parseServeOptions(cmd)
			if err != nil {
				return fmt.Errorf("serve: %w", err)
			}

			bcfg, err := brokerconfig.Load(ctx)
			if err != nil {
				return fmt.Errorf("serve: load config: %w", err)
			}

			var stderrWriter io.Writer
			if !cmd.Bool("quiet") {
				stderrWriter = os.Stderr
			}
			logger, lw, err := newCommandLogger("serve", effectiveLogLevel(cmd, bcfg), stderrWriter)
			if err != nil {
				return err
			}
			defer func() { _ = lw.Close() }()

			profile := cmd.String("profile")

			// Cancel on SIGINT/SIGTERM.
			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			pc, err := resolve(ctx, profile, effectiveRegion(cmd, bcfg))
			if err != nil {
				return fmt.Errorf("serve: %w", err)
			}

			srv, err := startServer(opts, profile, pc, logger)
			if err != nil {
				return fmt.Errorf("serve: start server: %w", err)
			}
			defer func() {
				srv.Stop()
				<-srv.Done()
			}()

			select {
			case <-ctx.Done():
				logger.Info("shutting down")
			case <-srv.Done():
				logger.Error("server exited unexpectedly")
			}
			return nil
		},
	}
}

// startServer starts the protocol server selected by opts and logs where it
// listens. The token value is never logged.
func startServer(opts serveOptions, profile string, pc profileCredentials, logger *slog.Logger) (broker.Server, error) {
	switch opts.protocol {
	case protocolContainer:
		srv, err := containercreds.New(containercreds.Options{
			Profile:     profile,
			Region:      pc.Region,
			BindAddr:    opts.bindAddr,
			Path:        opts.path,
			TokenFile:   opts.tokenFile,
			Logger:      logger,
			Credentials: pc.Credentials,
		})
		if err != nil {
			return nil, err
		}
		logger.Info("container credentials server listening",
			"url", srv.CredentialsURL(),
			"addr", srv.Addr(),
			"path", srv.Path(),
			"token_file", opts.tokenFile,
			"profile", profile)
		return srv, nil
	default:
		srv, err := imdsserver.New(imdsserver.Options{
			Profile:       profile,
			Region:        pc.Region,
			PrincipalName: pc.Identity.PrincipalName,
			AccountID:     pc.Identity.AccountID,
			BindAddrs:     []string{opts.bindAddr},
			Logger:        logger,
			Credentials:   pc.Credentials,
		})
		if err != nil {
			return nil, err
		}
		for _, u := range srv.URLs() {
			logger.Info("IMDS server listening", "url", u, "profile", profile)
		}
		return srv, nil
	}
}
