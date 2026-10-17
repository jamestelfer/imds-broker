package awscreds

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const retrieveTimeout = 30 * time.Second

// Retrieve fetches credentials for an HTTP request without inheriting its
// cancellation. SSO refreshes need a network round-trip, and a client whose
// read deadline fires first would otherwise abort the refresh for every
// subsequent caller.
func Retrieve(ctx context.Context, p aws.CredentialsProvider) (aws.Credentials, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retrieveTimeout)
	defer cancel()
	return p.Retrieve(ctx)
}
