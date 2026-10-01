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

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
	"github.com/jamestelfer/imds-broker/pkg/broker"
	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// serverStarter starts the server for the selected protocol and logs where it
// listens.
type serverStarter func(bindAddr, profile string, rp awscreds.ResolvedProfile, logger *slog.Logger) (broker.Server, error)

// serveOptions holds the validated serve flags.
type serveOptions struct {
	bindAddr string
	start    serverStarter
}

// parseServeOptions validates the protocol and listener flags before any AWS
// configuration is loaded or any listener is bound.
func parseServeOptions(cmd *cli.Command) (serveOptions, error) {
	port := cmd.Int("port")
	if port < 0 || port > 65535 {
		return serveOptions{}, fmt.Errorf("--port must be between 0 and 65535, got %d", port)
	}
	opts := serveOptions{bindAddr: net.JoinHostPort(cmd.String("bind"), strconv.Itoa(port))}

	switch protocol := cmd.String("protocol"); protocol {
	case "imds":
		if cmd.IsSet("token-file") {
			return serveOptions{}, errors.New("--token-file requires --protocol container")
		}
		opts.start = startIMDS
	case "container":
		tokenFile := cmd.String("token-file")
		if tokenFile == "" {
			return serveOptions{}, errors.New("--protocol container requires --token-file")
		}
		opts.start = containerStarter(tokenFile)
	default:
		return serveOptions{}, fmt.Errorf(`unknown --protocol %q: must be "imds" or "container"`, protocol)
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
				Value: "imds",
			},
			&cli.StringFlag{
				Name: "token-file",
				Usage: "container protocol only (required): path the authorisation token is written to, mode 0600. " +
					"Clients reading it via AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE must run as the broker's UID or root",
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

			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			rp, err := resolve(ctx, profile, effectiveRegion(cmd, bcfg))
			if err != nil {
				return fmt.Errorf("serve: %w", err)
			}

			srv, err := opts.start(opts.bindAddr, profile, rp, logger)
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

func startIMDS(bindAddr, profile string, rp awscreds.ResolvedProfile, logger *slog.Logger) (broker.Server, error) {
	srv, err := imdsserver.New(imdsserver.Options{
		Profile:       profile,
		Region:        rp.Region,
		PrincipalName: rp.Identity.PrincipalName,
		AccountID:     rp.Identity.AccountID,
		BindAddrs:     []string{bindAddr},
		Logger:        logger,
		Credentials:   rp.Credentials,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("IMDS server listening", "url", srv.URLs()[0], "profile", profile)
	return srv, nil
}

func containerStarter(tokenFile string) serverStarter {
	return func(bindAddr, profile string, rp awscreds.ResolvedProfile, logger *slog.Logger) (broker.Server, error) {
		srv, err := containercreds.New(containercreds.Options{
			Profile:     profile,
			Region:      rp.Region,
			BindAddr:    bindAddr,
			TokenFile:   tokenFile,
			Logger:      logger,
			Credentials: rp.Credentials,
		})
		if err != nil {
			return nil, err
		}
		logger.Info("container credentials server listening",
			"url", srv.CredentialsURL(),
			"addr", srv.Addr(),
			"token_file", tokenFile,
			"profile", profile)
		return srv, nil
	}
}
