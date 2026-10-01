package containercreds

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/justinas/alice"

	"github.com/jamestelfer/imds-broker/pkg/httplog"
)

// retrieveTimeout bounds credential retrieval. SSO refreshes need a network
// round-trip, so retrieval is detached from the client's request context and
// given its own deadline instead.
const retrieveTimeout = 30 * time.Second

// CredentialProvider abstracts AWS credential retrieval. Production wiring
// supplies a provider wrapped in aws.CredentialsCache; the server adds no
// caching of its own.
type CredentialProvider interface {
	Retrieve(ctx context.Context) (aws.Credentials, error)
}

// credentialsResponse is the container credentials protocol success shape.
// Expiration is always present, so SDKs treat the credentials as refreshable.
type credentialsResponse struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Token           string `json:"Token"`
	Expiration      string `json:"Expiration"`
	AccountID       string `json:"AccountId,omitempty"`
}

// errorResponse is the container credentials protocol error shape, matching
// the AWS SDK endpointcreds decoder (lowercase keys, top level).
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type handler struct {
	path  string
	token []byte
	creds CredentialProvider
}

// newHandler builds the request pipeline: request logging, then
// authorisation, then routing. Authorisation precedes routing so that an
// unauthenticated caller cannot discover which paths exist.
func newHandler(path string, token []byte, logger *slog.Logger, creds CredentialProvider) http.Handler {
	h := &handler{path: path, token: token, creds: creds}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, h.handleCredentials)
	mux.HandleFunc(path, h.handleMethodNotAllowed)
	mux.HandleFunc("/", h.handleNotFound)

	return alice.New(httplog.Middleware(logger), h.authorise).Then(mux)
}

// authorise rejects requests whose Authorization header does not exactly
// match the server token. The SDK sends the token verbatim, with no scheme
// prefix.
//
// The token is defence-in-depth against stray containers on a shared network.
// It does not protect credentials from the intended client, which holds the
// token by design. Keeping the token away from other workloads depends on the
// sandbox boundary around the token file.
func (h *handler) authorise(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got == "" {
			writeError(w, http.StatusForbidden, "AccessDenied", "Authorization header is missing")
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), h.token) != 1 {
			writeError(w, http.StatusForbidden, "AccessDenied", "Authorization token is invalid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) handleCredentials(w http.ResponseWriter, r *http.Request) {
	logger := httplog.FromContext(r.Context())

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), retrieveTimeout)
	defer cancel()

	creds, err := h.creds.Retrieve(ctx)
	if err != nil {
		logger.Error("failed to retrieve credentials", "error", err)
		writeError(w, http.StatusInternalServerError, "CredentialsUnavailable", "Failed to retrieve credentials")
		return
	}

	if err := checkTemporary(creds); err != nil {
		logger.Error("refusing to serve credentials", "error", err)
		writeError(w, http.StatusInternalServerError, "CredentialsNotTemporary", err.Error())
		return
	}

	expiry, err := creds.Expires.UTC().MarshalText()
	if err != nil {
		logger.Error("failed to format credential expiry", "error", err)
		writeError(w, http.StatusInternalServerError, "InternalError", "Failed to build response")
		return
	}

	body, err := json.Marshal(credentialsResponse{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		Token:           creds.SessionToken,
		Expiration:      string(expiry),
		AccountID:       creds.AccountID,
	})
	if err != nil {
		logger.Error("failed to marshal credentials response", "error", err)
		writeError(w, http.StatusInternalServerError, "InternalError", "Failed to build response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// checkTemporary rejects credentials that cannot be served in the refreshable
// form. The server never emits the static form (no Expiration), and it does
// not invent an expiry the source did not supply.
func checkTemporary(creds aws.Credentials) error {
	if creds.SessionToken == "" {
		return errors.New("resolved credentials have no session token")
	}
	if creds.Expires.IsZero() {
		return errors.New("resolved credentials have no expiry")
	}
	return nil
}

func (h *handler) handleMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed")
}

func (h *handler) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "NotFound", "Not found")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	body, _ := json.Marshal(errorResponse{Code: code, Message: message})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
