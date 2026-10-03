package broker_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
)

// containerServers builds real container credentials servers for a broker and
// indexes them by URL, so tests can authorise requests and stop them directly.
type containerServers struct {
	t       *testing.T
	created map[string]*containercreds.Server
}

// factory returns a broker.ServerFactory that starts container credentials
// servers.
//
// The broker dedups on profile:region only, so a repeat CreateServer returns
// the existing server even if the caller wanted a different token file. That
// is acceptable while the container protocol is reachable only through serve.
func (c *containerServers) factory() broker.ServerFactory {
	return func(_ context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (broker.Server, error) {
		creds := aws.Credentials{
			AccessKeyID:     "AKID-" + profile,
			SecretAccessKey: "secret",
			SessionToken:    "session",
			CanExpire:       true,
			Expires:         time.Now().Add(time.Hour),
		}
		srv, err := containercreds.New(containercreds.Options{
			Profile:   profile,
			Region:    region,
			BindAddr:  bindAddrs[0],
			TokenFile: filepath.Join(c.t.TempDir(), "token"),
			Logger:    logger,
			Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				return creds, nil
			}),
		})
		if err != nil {
			return nil, err
		}
		c.created[srv.URLs()[0]] = srv
		c.t.Cleanup(func() {
			srv.Stop()
			<-srv.Done()
		})
		return srv, nil
	}
}

// get performs an authorised credentials request against the server at url.
func (c *containerServers) get(url string) (int, error) {
	srv := c.created[url]
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodGet, srv.CredentialsURL(), nil)
	require.NoError(c.t, err)
	req.Header.Set("Authorization", srv.Token())
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func newContainerBroker(t *testing.T) (*broker.Broker, *containerServers) {
	t.Helper()
	c := &containerServers{t: t, created: make(map[string]*containercreds.Server)}
	b, err := broker.New(t.Context(), broker.Options{Logger: discardLogger(), ServerFactory: c.factory()})
	require.NoError(t, err)
	t.Cleanup(b.StopAll)
	return b, c
}

func TestContainerServer_Deduplication(t *testing.T) {
	b, c := newContainerBroker(t)

	r1, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	r2, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)

	assert.Equal(t, r1.LocalURL, r2.LocalURL)
	assert.Len(t, c.created, 1, "only one listener should be bound")

	status, err := c.get(r1.LocalURL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

// TestContainerServer_CrashIsReplaced stops the server behind the broker's
// back, which is how the broker sees a crash: Done() closes without a
// broker-requested stop.
func TestContainerServer_CrashIsReplaced(t *testing.T) {
	b, c := newContainerBroker(t)

	r1, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	crashed := c.created[r1.LocalURL]
	crashed.Stop()
	<-crashed.Done()

	r2, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	assert.NotEqual(t, r1.LocalURL, r2.LocalURL)

	status, err := c.get(r2.LocalURL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestContainerServer_StopServerFreesPort(t *testing.T) {
	b, c := newContainerBroker(t)

	r, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	srv := c.created[r.LocalURL]

	require.NoError(t, b.StopServer(t.Context(), r.LocalURL))
	<-srv.Done()

	_, err = c.get(r.LocalURL)
	assert.Error(t, err)
}
