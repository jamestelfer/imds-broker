package awscreds

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// ResolvedProfile is the AWS state every serving protocol needs for one
// profile.
type ResolvedProfile struct {
	// Region is the effective region after applying profile configuration.
	Region      string
	Identity    CallerIdentity
	Credentials aws.CredentialsProvider
}

// ResolveProfile loads AWS config for profile, validates the credentials via
// STS GetCallerIdentity, and returns a provider that always vends temporary
// credentials. An empty region defers to the profile configuration.
func ResolveProfile(ctx context.Context, profile, region string) (ResolvedProfile, error) {
	// The SDK treats an empty profile as unset and falls back to host
	// credentials (environment, AWS_PROFILE, default profile).
	if err := profiles.ValidateName(profile); err != nil {
		return ResolvedProfile{}, err
	}

	loadOpts := []func(*config.LoadOptions) error{
		config.WithSharedConfigProfile(profile),
	}
	if region != "" {
		loadOpts = append(loadOpts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return ResolvedProfile{}, fmt.Errorf("load AWS config for profile %q: %w", profile, err)
	}

	stsClient := sts.NewFromConfig(cfg)

	identity, err := ResolveCallerIdentity(ctx, stsClient)
	if err != nil {
		return ResolvedProfile{}, fmt.Errorf("resolve caller identity for profile %q: %w", profile, err)
	}

	creds, err := temporaryCredentials(ctx, cfg, stsClient)
	if err != nil {
		return ResolvedProfile{}, fmt.Errorf("build credential provider for profile %q: %w", profile, err)
	}

	return ResolvedProfile{Region: cfg.Region, Identity: identity, Credentials: creds}, nil
}

// temporaryCredentials returns cfg's provider if it already vends a session
// token, and otherwise upgrades long-term keys via STS GetSessionToken.
func temporaryCredentials(ctx context.Context, cfg aws.Config, stsClient *sts.Client) (aws.CredentialsProvider, error) {
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieve credentials: %w", err)
	}
	if creds.SessionToken != "" {
		return cfg.Credentials, nil
	}
	return aws.NewCredentialsCache(NewSessionTokenProvider(stsClient)), nil
}
