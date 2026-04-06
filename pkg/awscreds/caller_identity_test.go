package awscreds_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
)

type stubCallerIdentity struct {
	output *sts.GetCallerIdentityOutput
	err    error
}

func (s *stubCallerIdentity) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return s.output, s.err
}

func ptr(s string) *string { return &s }

func TestResolveCallerIdentity_APIError(t *testing.T) {
	stub := &stubCallerIdentity{err: fmt.Errorf("access denied")}

	_, err := awscreds.ResolveCallerIdentity(context.Background(), stub)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestResolveCallerIdentity_AssumedRoleARN(t *testing.T) {
	stub := &stubCallerIdentity{
		output: &sts.GetCallerIdentityOutput{
			Arn: ptr("arn:aws:sts::123456789012:assumed-role/MyRole/session"),
		},
	}

	name, err := awscreds.ResolveCallerIdentity(context.Background(), stub)
	require.NoError(t, err)
	assert.Equal(t, "MyRole", name)
}

func TestResolveCallerIdentity_UserARN(t *testing.T) {
	stub := &stubCallerIdentity{
		output: &sts.GetCallerIdentityOutput{
			Arn: ptr("arn:aws:iam::123456789012:user/johndoe"),
		},
	}

	name, err := awscreds.ResolveCallerIdentity(context.Background(), stub)
	require.NoError(t, err)
	assert.Equal(t, "johndoe", name)
}
