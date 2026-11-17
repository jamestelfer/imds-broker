package containercreds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/endpointcreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
)

var _ broker.Server = (*containercreds.Server)(nil)

const (
	testAccessKey    = "AKIAIOSFODNN7EXAMPLE"
	testSecretKey    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testSessionToken = "AQoDYXdzEJr//fake-session-token"
)

func staticCreds(expires time.Time) containercreds.CredentialProvider {
	creds := aws.Credentials{
		AccessKeyID:     testAccessKey,
		SecretAccessKey: testSecretKey,
		SessionToken:    testSessionToken,
		CanExpire:       true,
		Expires:         expires,
	}
	return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return creds, nil
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func startServer(t *testing.T, creds containercreds.CredentialProvider, logger *slog.Logger) *containercreds.Server {
	t.Helper()
	srv, err := containercreds.New(containercreds.Options{
		Profile:     "test",
		Region:      "us-east-1",
		BindAddr:    "127.0.0.1:0",
		Logger:      logger,
		Credentials: creds,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		srv.Stop()
		<-srv.Done()
	})
	return srv
}

func doRequest(t *testing.T, method, url, token string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, body
}

func TestSDKRoundTrip_EndpointCreds(t *testing.T) {
	expires := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	srv := startServer(t, staticCreds(expires), discardLogger())

	provider := endpointcreds.New(srv.CredentialsURL(), func(o *endpointcreds.Options) {
		o.AuthorizationToken = srv.Token()
	})

	got, err := provider.Retrieve(t.Context())
	require.NoError(t, err)

	assert.Equal(t, testAccessKey, got.AccessKeyID)
	assert.Equal(t, testSecretKey, got.SecretAccessKey)
	assert.Equal(t, testSessionToken, got.SessionToken)
	assert.True(t, got.CanExpire)
	assert.True(t, expires.Equal(got.Expires), "expires: want %v, got %v", expires, got.Expires)
}

func TestSDKRoundTrip_ErrorDecodes(t *testing.T) {
	failing := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New("boom")
	})
	srv := startServer(t, failing, discardLogger())

	provider := endpointcreds.New(srv.CredentialsURL(), func(o *endpointcreds.Options) {
		o.AuthorizationToken = srv.Token()
		o.Retryer = aws.NopRetryer{}
	})

	_, err := provider.Retrieve(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CredentialsUnavailable")
}

func TestProtocol(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	ok := startServer(t, staticCreds(expires), discardLogger())
	failing := startServer(t, aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New("resolution failed")
	}), discardLogger())
	noExpiry := startServer(t, staticCreds(time.Time{}), discardLogger())

	cases := []struct {
		name       string
		srv        *containercreds.Server
		method     string
		path       string
		token      string
		wantStatus int
		wantCode   string
	}{
		{name: "valid token", srv: ok, method: http.MethodGet, path: containercreds.Path, token: ok.Token(), wantStatus: http.StatusOK},
		{name: "absent token", srv: ok, method: http.MethodGet, path: containercreds.Path, wantStatus: http.StatusForbidden, wantCode: "AccessDenied"},
		{name: "wrong token", srv: ok, method: http.MethodGet, path: containercreds.Path, token: strings.Repeat("0", len(ok.Token())), wantStatus: http.StatusForbidden, wantCode: "AccessDenied"},
		{name: "bearer prefix rejected", srv: ok, method: http.MethodGet, path: containercreds.Path, token: "Bearer " + ok.Token(), wantStatus: http.StatusForbidden, wantCode: "AccessDenied"},
		{name: "unknown path unauthenticated", srv: ok, method: http.MethodGet, path: "/nope", wantStatus: http.StatusForbidden, wantCode: "AccessDenied"},
		{name: "unknown path", srv: ok, method: http.MethodGet, path: "/nope", token: ok.Token(), wantStatus: http.StatusNotFound, wantCode: "NotFound"},
		{name: "wrong method", srv: ok, method: http.MethodPost, path: containercreds.Path, token: ok.Token(), wantStatus: http.StatusMethodNotAllowed, wantCode: "MethodNotAllowed"},
		{name: "provider error", srv: failing, method: http.MethodGet, path: containercreds.Path, token: failing.Token(), wantStatus: http.StatusInternalServerError, wantCode: "CredentialsUnavailable"},
		{name: "zero expiry", srv: noExpiry, method: http.MethodGet, path: containercreds.Path, token: noExpiry.Token(), wantStatus: http.StatusInternalServerError, wantCode: "CredentialsNotTemporary"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, header, body := doRequest(t, tc.method, tc.srv.URLs()[0]+tc.path, tc.token)
			require.Equal(t, tc.wantStatus, status, string(body))
			assert.Equal(t, "application/json", header.Get("Content-Type"))

			if tc.wantStatus == http.StatusOK {
				var got map[string]string
				require.NoError(t, json.Unmarshal(body, &got))
				assert.Equal(t, testAccessKey, got["AccessKeyId"])
				assert.Equal(t, testSecretKey, got["SecretAccessKey"])
				assert.Equal(t, testSessionToken, got["Token"])
				require.Contains(t, got, "Expiration")
				exp, err := time.Parse(time.RFC3339, got["Expiration"])
				require.NoError(t, err)
				assert.True(t, strings.HasSuffix(got["Expiration"], "Z"), "expiration not UTC: %s", got["Expiration"])
				assert.WithinDuration(t, expires, exp, time.Second)
				return
			}

			var errBody struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(body, &errBody), string(body))
			assert.Equal(t, tc.wantCode, errBody.Code)
			assert.NotEmpty(t, errBody.Message)
		})
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLogging_NoSecrets(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ok := startServer(t, staticCreds(time.Now().Add(time.Hour)), logger)
	failing := startServer(t, aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New("resolution failed")
	}), logger)

	doRequest(t, http.MethodGet, ok.CredentialsURL(), ok.Token())
	doRequest(t, http.MethodGet, ok.CredentialsURL(), "")
	doRequest(t, http.MethodGet, ok.CredentialsURL(), "wrong")
	doRequest(t, http.MethodGet, ok.URLs()[0]+"/nope", ok.Token())
	doRequest(t, http.MethodGet, failing.CredentialsURL(), failing.Token())

	out := logs.String()
	require.Contains(t, out, "request_id")
	for _, secret := range []string{ok.Token(), failing.Token(), testSecretKey, testSessionToken} {
		assert.NotContains(t, out, secret)
	}
}

func TestServer_DistinctTokensPerInstance(t *testing.T) {
	a := startServer(t, staticCreds(time.Now().Add(time.Hour)), discardLogger())
	b := startServer(t, staticCreds(time.Now().Add(time.Hour)), discardLogger())
	assert.NotEqual(t, a.Token(), b.Token())
	assert.Regexp(t, `^[0-9a-f]{64}$`, a.Token())

	status, _, _ := doRequest(t, http.MethodGet, a.CredentialsURL(), b.Token())
	assert.Equal(t, http.StatusForbidden, status)
}

func TestServer_TokenFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	srv, err := containercreds.New(containercreds.Options{
		BindAddr:    "127.0.0.1:0",
		TokenFile:   path,
		Logger:      discardLogger(),
		Credentials: staticCreds(time.Now().Add(time.Hour)),
	})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, srv.Token(), string(written))

	status, _, _ := doRequest(t, http.MethodGet, srv.CredentialsURL(), string(written))
	assert.Equal(t, http.StatusOK, status)

	srv.Stop()
	<-srv.Done()
	assert.NoFileExists(t, path)
}

func TestServer_TokenFileWriteFailureStopsServer(t *testing.T) {
	_, err := containercreds.New(containercreds.Options{
		BindAddr:    "127.0.0.1:0",
		TokenFile:   filepath.Join(t.TempDir(), "absent", "token"),
		Logger:      discardLogger(),
		Credentials: staticCreds(time.Now().Add(time.Hour)),
	})
	require.Error(t, err)
}
