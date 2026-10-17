package main

import (
	"context"
	"log/slog"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
)

// profileResolver resolves a profile's credentials. Production code uses
// awscreds.ResolveProfile; tests inject fakes.
type profileResolver func(ctx context.Context, profile, region string) (awscreds.ResolvedProfile, error)

// imdsFactory starts IMDSv2 servers. Both mcp and serve --protocol imds use it.
func imdsFactory(resolve profileResolver) broker.ServerFactory {
	return func(ctx context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (broker.Server, []slog.Attr, error) {
		rp, err := resolve(ctx, profile, region)
		if err != nil {
			return nil, nil, err
		}
		srv, err := imdsserver.New(imdsserver.Options{
			Profile:       profile,
			Region:        rp.Region,
			PrincipalName: rp.Identity.PrincipalName,
			AccountID:     rp.Identity.AccountID,
			BindAddrs:     bindAddrs,
			Logger:        logger,
			Credentials:   rp.Credentials,
		})
		if err != nil {
			return nil, nil, err
		}
		return srv, []slog.Attr{
			slog.String("protocol", "imds"),
			slog.String("profile", profile),
			slog.String("region", rp.Region),
			slog.String("url", srv.URLs()[0]),
		}, nil
	}
}

// containerFactory starts container credentials servers that write their
// authorisation token to tokenFile.
func containerFactory(resolve profileResolver, tokenFile string) broker.ServerFactory {
	return func(ctx context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (broker.Server, []slog.Attr, error) {
		rp, err := resolve(ctx, profile, region)
		if err != nil {
			return nil, nil, err
		}
		srv, err := containercreds.New(ctx, containercreds.Options{
			Profile:     profile,
			Region:      rp.Region,
			BindAddrs:   bindAddrs,
			TokenFile:   tokenFile,
			Logger:      logger,
			Credentials: rp.Credentials,
		})
		if err != nil {
			return nil, nil, err
		}
		return srv, []slog.Attr{
			slog.String("protocol", "container"),
			slog.String("profile", profile),
			slog.String("region", rp.Region),
			slog.String("url", srv.CredentialsURL()),
			slog.String("token_file", tokenFile),
		}, nil
	}
}
