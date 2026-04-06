package awscreds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

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
				AccessKeyId:     ptr("ASIATEMP1234"),
				SecretAccessKey: ptr("tempSecret"),
				SessionToken:    ptr("tempSession"),
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
}
