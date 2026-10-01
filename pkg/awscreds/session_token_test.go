package awscreds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	stypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
)

type stubSessionToken struct {
	output *sts.GetSessionTokenOutput
	err    error
}

func (s *stubSessionToken) GetSessionToken(ctx context.Context, params *sts.GetSessionTokenInput, optFns ...func(*sts.Options)) (*sts.GetSessionTokenOutput, error) {
	return s.output, s.err
}

func TestSessionTokenProvider_PropagatesError(t *testing.T) {
	stub := &stubSessionToken{err: fmt.Errorf("no permission")}

	provider := awscreds.NewSessionTokenProvider(stub)
	_, err := provider.Retrieve(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no permission")
}

func TestSessionTokenProvider_ReturnsTemporaryCredentials(t *testing.T) {
	expiry := time.Now().UTC().Add(time.Hour)
	stub := &stubSessionToken{
		output: &sts.GetSessionTokenOutput{
			Credentials: &stypes.Credentials{
				AccessKeyId:     new("ASIATEMP1234"),
				SecretAccessKey: new("tempSecret"),
				SessionToken:    new("tempSession"),
				Expiration:      &expiry,
			},
		},
	}

	provider := awscreds.NewSessionTokenProvider(stub)
	creds, err := provider.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ASIATEMP1234", creds.AccessKeyID)
	assert.Equal(t, "tempSecret", creds.SecretAccessKey)
	assert.Equal(t, "tempSession", creds.SessionToken)
	assert.Equal(t, expiry, creds.Expires)
	assert.True(t, creds.CanExpire)
}

// rotatingSessionToken issues already-expired credentials with a new access
// key on every call.
type rotatingSessionToken struct {
	calls int
}

func (s *rotatingSessionToken) GetSessionToken(context.Context, *sts.GetSessionTokenInput, ...func(*sts.Options)) (*sts.GetSessionTokenOutput, error) {
	s.calls++
	expiry := time.Now().Add(-time.Minute)
	return &sts.GetSessionTokenOutput{
		Credentials: &stypes.Credentials{
			AccessKeyId:     new(fmt.Sprintf("ASIATEMP%d", s.calls)),
			SecretAccessKey: new("tempSecret"),
			SessionToken:    new("tempSession"),
			Expiration:      &expiry,
		},
	}, nil
}

func TestSessionTokenProvider_CacheRefreshesAfterExpiry(t *testing.T) {
	stub := &rotatingSessionToken{}
	cache := aws.NewCredentialsCache(awscreds.NewSessionTokenProvider(stub))

	first, err := cache.Retrieve(t.Context())
	require.NoError(t, err)
	second, err := cache.Retrieve(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 2, stub.calls, "expired credentials must be re-fetched")
	assert.NotEqual(t, first.AccessKeyID, second.AccessKeyID)
}

func TestSessionTokenProvider_NoExpirationCannotExpire(t *testing.T) {
	stub := &stubSessionToken{
		output: &sts.GetSessionTokenOutput{
			Credentials: &stypes.Credentials{
				AccessKeyId:     new("ASIATEMP1234"),
				SecretAccessKey: new("tempSecret"),
				SessionToken:    new("tempSession"),
			},
		},
	}

	creds, err := awscreds.NewSessionTokenProvider(stub).Retrieve(t.Context())
	require.NoError(t, err)
	assert.False(t, creds.CanExpire)
	assert.True(t, creds.Expires.IsZero())
}
