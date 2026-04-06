// Package awscreds provides AWS credential chain setup and STS integration.
package awscreds

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// CallerIdentityAPI abstracts the STS GetCallerIdentity operation.
type CallerIdentityAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// ResolveCallerIdentity calls STS GetCallerIdentity and extracts the principal
// name from the ARN. For IAM users and assumed roles the name is the second
// path segment of the ARN resource (e.g. "johndoe" from "user/johndoe", or
// "MyRole" from "assumed-role/MyRole/session").
func ResolveCallerIdentity(ctx context.Context, api CallerIdentityAPI) (string, error) {
	out, err := api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("awscreds: GetCallerIdentity: %w", err)
	}
	if out.Arn == nil {
		return "", fmt.Errorf("awscreds: GetCallerIdentity returned nil ARN")
	}

	// ARN format: arn:partition:service:region:account:resource
	// resource is "type/name" or "type/name/qualifier"
	parts := strings.Split(*out.Arn, ":")
	if len(parts) < 6 {
		return "", fmt.Errorf("awscreds: unexpected ARN format: %s", *out.Arn)
	}
	resource := parts[5]
	segments := strings.Split(resource, "/")
	if len(segments) < 2 {
		return "", fmt.Errorf("awscreds: unexpected ARN resource format: %s", resource)
	}
	return segments[1], nil
}
