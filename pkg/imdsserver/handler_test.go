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
)

// fixedCredentials is a mock CredentialProvider for tests.
type fixedCredentials struct {
	creds aws.Credentials
	err   error
}

func (f *fixedCredentials) Retrieve(_ context.Context) (aws.Credentials, error) {
	return f.creds, f.err
}

func testCreds() *fixedCredentials {
	return &fixedCredentials{
		creds: aws.Credentials{
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			SessionToken:    "AQoDYXdzEJr//fake-session-token",
			Expires:         time.Now().UTC().Add(time.Hour),
		},
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := newHandler("test-profile", "us-east-1", "TestRole", logger, testCreds())
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return h
}

// getToken obtains a valid IMDSv2 token from the handler.
func getToken(t *testing.T, h http.Handler, ttl int) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", fmt.Sprintf("%d", ttl))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 getting token, got %d: %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// --- PUT /latest/api/token ---

func TestPutToken_ValidTTL(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", "60")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected non-empty token in response body")
	}
	// Response must echo back the TTL header (required by Go SDK v2)
	if got := w.Header().Get("X-Aws-Ec2-Metadata-Token-Ttl-Seconds"); got != "60" {
		t.Errorf("expected TTL header echo '60', got %q", got)
	}
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
				req.Header.Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", tc.ttl)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if tc.wantOK && w.Code != http.StatusOK {
				t.Errorf("ttl=%s: expected 200, got %d", tc.ttl, w.Code)
			}
			if !tc.wantOK && w.Code == http.StatusOK {
				t.Errorf("ttl=%s: expected non-200, got 200", tc.ttl)
			}
		})
	}
}

// --- Auth middleware (token validation) ---

func TestAuthMiddleware_MissingToken(t *testing.T) {
	h := newTestHandler(t)
	for _, path := range []string{
		"/latest/meta-data/placement/region",
		"/latest/meta-data/iam/security-credentials/",
		"/latest/meta-data/iam/security-credentials/TestRole",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", w.Code)
			}
		})
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/region", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token", "invalid.token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	h := newTestHandler(t)
	// We can't easily create an expired token from the outside since the token
	// secret is internal. Use a token from a different handler instance (wrong secret).
	otherLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	other, err := newHandler("other", "us-west-2", "OtherRole", otherLogger, testCreds())
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/latest/api/token", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", "60")
	w := httptest.NewRecorder()
	other.ServeHTTP(w, req)
	foreignToken := w.Body.String()

	req2 := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/region", nil)
	req2.Header.Set("X-Aws-Ec2-Metadata-Token", foreignToken)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for foreign token, got %d", w2.Code)
	}
}

// --- Region endpoint ---

func TestRegionEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/placement/region", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "us-east-1" {
		t.Errorf("expected region 'us-east-1', got %q", got)
	}
}

// --- Credential listing endpoint ---

func TestCredentialListingEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "TestRole" {
		t.Errorf("expected principal name 'TestRole', got %q", got)
	}
}

// --- Credential detail endpoint ---

func TestCredentialDetailEndpoint(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/TestRole", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp credentialResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Code != "Success" {
		t.Errorf("expected Code='Success', got %q", resp.Code)
	}
	if resp.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("unexpected AccessKeyId: %q", resp.AccessKeyID)
	}
	if resp.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
		t.Errorf("unexpected SecretAccessKey")
	}
	if resp.Token == "" {
		t.Error("expected non-empty Token")
	}
	if resp.Expiration == "" {
		t.Error("expected non-empty Expiration")
	}
}

func TestCredentialDetailEndpoint_WrongRole(t *testing.T) {
	h := newTestHandler(t)
	tok := getToken(t, h, 60)

	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/WrongRole", nil)
	req.Header.Set("X-Aws-Ec2-Metadata-Token", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
