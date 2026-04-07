package imdsserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCreds() CredentialProvider {
	creds := aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "AQoDYXdzEJr//fake-session-token",
		Expires:         time.Now().UTC().Add(time.Hour),
	}
	return aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
		return creds, nil
	})
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newHandler("us-east-1", "TestRole", logger, testCreds())
}

// getToken obtains a valid IMDSv2 token from the handler.
func getToken(t *testing.T, h http.Handler, ttl int) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
	req.Header.Set(headerTokenTTL, fmt.Sprintf("%d", ttl))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "expected 200 getting token: %s", w.Body.String())
	return w.Body.String()
}

// --- PUT /latest/api/token ---

func TestPutToken_ValidTTL(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
	req.Header.Set(headerTokenTTL, "60")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotEmpty(t, w.Body.String())
	// Response must echo back the TTL header (required by Go SDK v2).
	assert.Equal(t, "60", w.Header().Get(headerTokenTTL))
}

func TestPutToken_TTLBoundaries(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct {
		ttl    string
		wantOK bool
	}{
		{"0", false},
		{"1", true},
		{"21600", true},
		{"21601", false},
		{"", false},
		{"notanumber", false},
	}

	for _, tc := range cases {
		t.Run("ttl="+tc.ttl, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
			if tc.ttl != "" {
				req.Header.Set(headerTokenTTL, tc.ttl)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if tc.wantOK {
				assert.Equal(t, http.StatusOK, w.Code)
			} else {
				assert.NotEqual(t, http.StatusOK, w.Code)
			}
		})
	}
}

// --- Auth middleware (token validation) ---

func TestAuthMiddleware_MissingToken(t *testing.T) {
	h := newTestHandler(t)
	for _, path := range []string{
		"/latest/meta-data/placement/region",
		"/latest/meta-data/placement/availability-zone/",
		"/latest/meta-data/iam/security-credentials/",
		"/latest/meta-data/iam/security-credentials/TestRole",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/region", nil)
	req.Header.Set(headerToken, "invalid.token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	h := newTestHandler(t)
	// Use a token from a different handler instance (different secret) to simulate rejection.
	otherLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	other := newHandler("us-west-2", "OtherRole", otherLogger, testCreds())

	req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
	req.Header.Set(headerTokenTTL, "60")
	w := httptest.NewRecorder()
	other.ServeHTTP(w, req)
	foreignToken := w.Body.String()

	req2 := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/region", nil)
	req2.Header.Set(headerToken, foreignToken)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)
}

// --- Availability zone endpoint ---

func TestAvailabilityZoneEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/availability-zone/", nil)
	req.Header.Set(headerToken, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "us-east-1a", w.Body.String())
}

// --- Region endpoint ---

func TestRegionEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/region", nil)
	req.Header.Set(headerToken, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "us-east-1", w.Body.String())
}

// --- Credential listing endpoint ---

func TestCredentialListingEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/", nil)
	req.Header.Set(headerToken, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "TestRole", w.Body.String())
}

// --- Credential detail endpoint ---

func TestCredentialDetailEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/TestRole", nil)
	req.Header.Set(headerToken, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp credentialResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Success", resp.Code)
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", resp.AccessKeyID)
	assert.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", resp.SecretAccessKey)
	assert.NotEmpty(t, resp.Token)
	assert.NotEmpty(t, resp.Expiration)
}

func TestCredentialDetailEndpoint_WrongRole(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/WrongRole", nil)
	req.Header.Set(headerToken, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
