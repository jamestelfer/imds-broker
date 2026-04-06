package imdsserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
)

type staticCreds struct {
	creds aws.Credentials
}

func (s *staticCreds) Retrieve(_ context.Context) (aws.Credentials, error) {
	return s.creds, nil
}

func newServerCreds() *staticCreds {
	return &staticCreds{
		creds: aws.Credentials{
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			SessionToken:    "AQoDYXdzEJr//fake-session-token",
			Expires:         time.Now().UTC().Add(time.Hour),
		},
	}
}

func newTestServer(t *testing.T, bindAddrs ...string) *imdsserver.Server {
	t.Helper()
	if len(bindAddrs) == 0 {
		bindAddrs = []string{"127.0.0.1:0"}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := imdsserver.New(imdsserver.Options{
		Profile:       "test",
		Region:        "us-east-1",
		PrincipalName: "TestRole",
		BindAddrs:     bindAddrs,
		Logger:        logger,
		Credentials:   newServerCreds(),
	})
	require.NoError(t, err)
	return srv
}

func TestServer_StartsAndResponds(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Stop()

	urls := srv.URLs()
	require.Len(t, urls, 1)

	// The server should respond to HTTP requests.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, urls[0]+"/latest/meta-data/placement/region", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	// 401 because we didn't include a token — but the server is alive.
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_DoneClosesOnStop(t *testing.T) {
	srv := newTestServer(t)
	srv.Stop()

	select {
	case <-srv.Done():
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("Done() channel not closed after Stop()")
	}
}

func TestServer_MultipleBindAddrs(t *testing.T) {
	srv := newTestServer(t, "127.0.0.1:0", "127.0.0.1:0")
	defer srv.Stop()

	urls := srv.URLs()
	require.Len(t, urls, 2)
	assert.NotEqual(t, urls[0], urls[1])
}

func TestServer_StopIsIdempotent(t *testing.T) {
	srv := newTestServer(t)
	srv.Stop()
	srv.Stop() // must not panic

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case <-srv.Done():
	case <-ctx.Done():
		t.Fatal("Done() not closed within timeout")
	}
}
