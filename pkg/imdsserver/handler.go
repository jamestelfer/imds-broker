package imdsserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/justinas/alice"
)

// CredentialProvider abstracts AWS credential retrieval.
// In production, this is backed by the AWS SDK credential chain.
// In tests, a mock implementation is injected.
type CredentialProvider interface {
	Retrieve(ctx context.Context) (aws.Credentials, error)
}

// credentialResponse is the IMDS JSON shape for credential endpoints.
type credentialResponse struct {
	Code            string `json:"Code"`
	LastUpdated     string `json:"LastUpdated"`
	Type            string `json:"Type"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Token           string `json:"Token"`
	Expiration      string `json:"Expiration"`
}

// imdsHandler holds the state for a single IMDS-compatible HTTP handler.
type imdsHandler struct {
	region        string
	principalName string
	logger        *slog.Logger
	creds         CredentialProvider
	tok           *token
}

// newHandler constructs an http.Handler implementing the IMDSv2 API.
// principalName is the identity name returned by the credential listing endpoint
// (in production, derived from STS GetCallerIdentity at startup).
func newHandler(region, principalName string, logger *slog.Logger, creds CredentialProvider) http.Handler {
	h := &imdsHandler{
		region:        region,
		principalName: principalName,
		logger:        logger,
		creds:         creds,
		tok:           newToken(),
	}
	return h.buildMux()
}

func (h *imdsHandler) buildMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("PUT /latest/api/token", h.handleToken)

	protected := alice.New(h.requireToken)
	mux.Handle("GET /latest/meta-data/placement/region", protected.ThenFunc(h.handleRegion))
	mux.Handle("GET /latest/meta-data/iam/security-credentials/", protected.ThenFunc(h.handleCredentialList))
	mux.Handle("GET /latest/meta-data/iam/security-credentials/{role}", protected.ThenFunc(h.handleCredentialDetail))

	return alice.New(h.logRequest).Then(mux)
}

// handleToken issues a new IMDSv2 session token.
func (h *imdsHandler) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Forwarded-For") != "" {
		writeError(w, http.StatusUnauthorized, "InvalidHeader", "Token requests can't contain X-Forwarded-For")
		return
	}

	ttlStr := r.Header.Get("X-Aws-Ec2-Metadata-Token-Ttl-Seconds")
	if ttlStr == "" {
		writeError(w, http.StatusUnauthorized, "MissingTTL", "The IMDSv2 token TTL header is missing")
		return
	}

	ttlInt, err := strconv.Atoi(ttlStr)
	if err != nil || ttlInt < 1 || ttlInt > 21600 {
		writeError(w, http.StatusUnauthorized, "InvalidTTL", "The IMDSv2 token TTL must be between 1 and 21600 seconds")
		return
	}

	encoded := h.tok.encode(time.Duration(ttlInt) * time.Second)
	// Go SDK v2 requires the TTL header echoed back.
	w.Header().Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", ttlStr)
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(encoded)
}

// requireToken is middleware that validates the IMDSv2 token on GET requests.
func (h *imdsHandler) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Aws-Ec2-Metadata-Token")
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "MissingToken", "The IMDSv2 token header is missing")
			return
		}
		if err := h.tok.validate(tok); err != nil {
			writeError(w, http.StatusUnauthorized, "InvalidToken", err.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// logRequest is middleware that logs every incoming request.
func (h *imdsHandler) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		h.logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.code,
		)
	})
}

func (h *imdsHandler) handleRegion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, h.region)
}

func (h *imdsHandler) handleCredentialList(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, h.principalName)
}

func (h *imdsHandler) handleCredentialDetail(w http.ResponseWriter, r *http.Request) {
	role := r.PathValue("role")
	if role != h.principalName {
		writeError(w, http.StatusNotFound, "InvalidRole", "Unknown role name")
		return
	}

	creds, err := h.creds.Retrieve(r.Context())
	if err != nil {
		h.logger.Error("failed to retrieve credentials", "error", err)
		writeError(w, http.StatusInternalServerError, "InternalError", "Failed to retrieve credentials")
		return
	}

	expiry := creds.Expires
	if expiry.IsZero() {
		expiry = time.Now().UTC().Add(time.Hour)
	}

	expiryText, _ := expiry.MarshalText()
	lastUpdated, _ := time.Now().UTC().MarshalText()

	resp := credentialResponse{
		Code:            "Success",
		LastUpdated:     string(lastUpdated),
		Type:            "AWS-HMAC",
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		Token:           creds.SessionToken,
		Expiration:      string(expiryText),
	}

	body, err := json.Marshal(resp)
	if err != nil {
		h.logger.Error("failed to marshal credential response", "error", err)
		writeError(w, http.StatusInternalServerError, "InternalError", "Failed to build response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// writeError writes a structured JSON error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	type innerErr struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	type errBody struct {
		Error innerErr `json:"Error"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errBody{Error: innerErr{Code: code, Message: message}})
	_, _ = w.Write(body)
}

// statusWriter captures the HTTP status code for logging.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.code = code
	sw.ResponseWriter.WriteHeader(code)
}
