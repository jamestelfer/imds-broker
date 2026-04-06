package awscreds

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// SessionTokenAPI abstracts the STS GetSessionToken operation.
type SessionTokenAPI interface {
	GetSessionToken(ctx context.Context, params *sts.GetSessionTokenInput, optFns ...func(*sts.Options)) (*sts.GetSessionTokenOutput, error)
}

// SessionTokenProvider is an aws.CredentialsProvider that upgrades credentials
// to temporary ones via STS GetSessionToken.
type SessionTokenProvider struct {
	api SessionTokenAPI
}

// NewSessionTokenProvider creates a SessionTokenProvider backed by api.
// Wrap the result with aws.NewCredentialsCache to add caching.
func NewSessionTokenProvider(api SessionTokenAPI) *SessionTokenProvider {
	return &SessionTokenProvider{api: api}
}

// Retrieve calls STS GetSessionToken and returns the resulting temporary credentials.
func (p *SessionTokenProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	out, err := p.api.GetSessionToken(ctx, &sts.GetSessionTokenInput{})
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("awscreds: GetSessionToken: %w", err)
	}
	if out.Credentials == nil {
		return aws.Credentials{}, fmt.Errorf("awscreds: GetSessionToken returned nil credentials")
	}

	c := out.Credentials
	expiry := time.Time{}
	if c.Expiration != nil {
		expiry = *c.Expiration
	}

	return aws.Credentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretAccessKey),
		SessionToken:    aws.ToString(c.SessionToken),
		Expires:         expiry,
		Source:          "STS",
	}, nil
}
