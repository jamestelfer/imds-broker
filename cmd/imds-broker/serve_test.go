package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
)

const (
	fakeAccessKey    = "AKIAFAKESERVE0000000"
	fakeSecretKey    = "fake/serve/secret"
	fakeSessionToken = "fake-serve-session-token"
)

// fakeResolver returns fixed temporary credentials and counts calls.
func fakeResolver(calls *atomic.Int32) profileResolver {
	return func(_ context.Context, _, region string) (awscreds.ResolvedProfile, error) {
		calls.Add(1)
		if region == "" {
			region = "us-east-1"
		}
		creds := aws.Credentials{
			AccessKeyID:     fakeAccessKey,
			SecretAccessKey: fakeSecretKey,
			SessionToken:    fakeSessionToken,
			CanExpire:       true,
			Expires:         time.Now().Add(time.Hour),
		}
		return awscreds.ResolvedProfile{
			Region:   region,
			Identity: awscreds.CallerIdentity{PrincipalName: "FakeRole", AccountID: "123456789012"},
			Credentials: aws.NewCredentialsCache(aws.CredentialsProviderFunc(
				func(context.Context) (aws.Credentials, error) { return creds, nil })),
		}, nil
	}
}

// isolateServeEnv points broker config, logs, and AWS config at empty temp
// locations so serve tests do not touch host state.
func isolateServeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	awsDir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(awsDir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(awsDir, "credentials"))
	for _, k := range []string{
		"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN",
		"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
	} {
		unsetEnv(t, k)
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func runServe(ctx context.Context, resolve profileResolver, args ...string) error {
	app := &cli.Command{
		Name:     "imds-broker",
		Flags:    []cli.Flag{&cli.StringFlag{Name: "log-level"}},
		Commands: []*cli.Command{serveCommand(resolve)},
	}
	return app.Run(ctx, append([]string{"imds-broker", "serve"}, args...))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func TestServeContainer_SDKDefaultChainRetrievesCredentials(t *testing.T) {
	isolateServeEnv(t)
	var calls atomic.Int32
	tokenFile := filepath.Join(t.TempDir(), "token")
	port := freePort(t)

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		errc <- runServe(ctx, fakeResolver(&calls),
			"--profile", "p", "--quiet",
			"--protocol", "container",
			"--token-file", tokenFile,
			"--bind", "127.0.0.1",
			"--port", fmt.Sprint(port))
	}()

	// A present, non-empty token file means the server is ready.
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(tokenFile)
		return err == nil && len(b) > 0
	}, 5*time.Second, 10*time.Millisecond)

	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI",
		fmt.Sprintf("http://127.0.0.1:%d%s", port, containercreds.Path))
	t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE", tokenFile)

	cfg, err := config.LoadDefaultConfig(t.Context(), config.WithRegion("us-east-1"))
	require.NoError(t, err)
	got, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)

	assert.Equal(t, fakeAccessKey, got.AccessKeyID)
	assert.Equal(t, fakeSecretKey, got.SecretAccessKey)
	assert.Equal(t, fakeSessionToken, got.SessionToken)
	assert.True(t, got.CanExpire)
	assert.Equal(t, int32(1), calls.Load())

	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after cancellation")
	}
	assert.NoFileExists(t, tokenFile, "token file should be removed on shutdown")
}

func TestServe_ConfigErrorsFailBeforeResolving(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "empty profile", args: []string{"--profile", ""}, wantErr: "profile name is required"},
		{name: "whitespace profile", args: []string{"--profile", " \t"}, wantErr: "profile name is required"},
		{name: "container without token file", args: []string{"--protocol", "container"}, wantErr: "--token-file"},
		{name: "unknown protocol", args: []string{"--protocol", "bogus"}, wantErr: `unknown --protocol "bogus"`},
		{name: "token file with imds", args: []string{"--token-file", "/tmp/x"}, wantErr: "--token-file requires --protocol container"},
		{name: "port out of range", args: []string{"--port", "70000"}, wantErr: "--port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateServeEnv(t)
			var calls atomic.Int32
			// Fail rather than serve, so a missing check cannot block the test.
			resolve := func(context.Context, string, string) (awscreds.ResolvedProfile, error) {
				calls.Add(1)
				return awscreds.ResolvedProfile{}, errors.New("resolver called")
			}
			args := append([]string{"--profile", "p", "--quiet"}, tc.args...)
			err := runServe(t.Context(), resolve, args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Zero(t, calls.Load(), "AWS config must not be resolved")
		})
	}
}

func TestParseServeOptions_BindAddr(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "defaults", args: nil, want: "0.0.0.0:0"},
		{name: "port", args: []string{"--port", "8080"}, want: "0.0.0.0:8080"},
		{name: "bind and port", args: []string{"--bind", "169.254.170.23", "--port", "80"}, want: "169.254.170.23:80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := serveCommand(nil).Flags
			args := append([]string{"imds-broker", "sub", "--profile", "p"}, tc.args...)
			runWith(t, flags, args, func(c *cli.Command) {
				opts, err := parseServeOptions(c)
				require.NoError(t, err)
				assert.Equal(t, tc.want, opts.bindAddr)
			})
		})
	}
}

func TestServeIMDS_DefaultProtocolStillServesIMDS(t *testing.T) {
	isolateServeEnv(t)
	var calls atomic.Int32
	port := freePort(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- runServe(ctx, fakeResolver(&calls),
			"--profile", "p", "--quiet", "--port", fmt.Sprint(port))
	}()

	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", fmt.Sprintf("http://127.0.0.1:%d", port))

	var got aws.Credentials
	require.Eventually(t, func() bool {
		cfg, err := config.LoadDefaultConfig(t.Context(), config.WithRegion("us-east-1"))
		if err != nil {
			return false
		}
		got, err = cfg.Credentials.Retrieve(t.Context())
		return err == nil
	}, 5*time.Second, 50*time.Millisecond)
	assert.Equal(t, fakeAccessKey, got.AccessKeyID)

	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after cancellation")
	}
}

// stubServer is a broker.Server whose Done channel the test controls.
type stubServer struct{ done chan struct{} }

func (s stubServer) URLs() []string        { return nil }
func (s stubServer) Stop()                 {}
func (s stubServer) Done() <-chan struct{} { return s.done }

func TestAwaitShutdown(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("signal returns nil", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.NoError(t, awaitShutdown(ctx, stubServer{done: make(chan struct{})}, logger))
	})

	// A non-zero exit lets supervisors with restart: on-failure restart the
	// broker.
	t.Run("unexpected server exit returns error", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		err := awaitShutdown(t.Context(), stubServer{done: done}, logger)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "server exited unexpectedly")
	})
}
