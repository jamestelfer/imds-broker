package awscreds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
)

func TestRetrieve_IgnoresCallerCancellationButHasDeadline(t *testing.T) {
	var sawDeadline bool
	p := aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		_, sawDeadline = ctx.Deadline()
		return aws.Credentials{AccessKeyID: "AKID"}, ctx.Err()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := awscreds.Retrieve(ctx, p)

	require.NoError(t, err)
	assert.Equal(t, "AKID", got.AccessKeyID)
	assert.True(t, sawDeadline)
}
