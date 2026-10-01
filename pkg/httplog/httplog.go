// Package httplog provides request-scoped slog logging middleware shared by
// the broker's HTTP servers.
package httplog

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
)

type ctxKey struct{}

// Middleware returns an alice-compatible constructor that attaches a child of
// logger carrying a per-request "request_id" to the request context, and logs
// one record per request after the handler completes.
//
// Only the method, path, status, and client address are logged. Headers are
// never logged, so authorisation values cannot leak through this middleware.
func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqLogger := logger.With("request_id", requestID())
			sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, reqLogger)))
			reqLogger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.code,
				"client", r.RemoteAddr,
			)
		})
	}
}

// FromContext returns the request-scoped logger attached by Middleware, or
// slog.Default() if none is present.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

func requestID() string {
	return fmt.Sprintf("%016x", rand.Uint64()) //nolint:gosec // correlation ID, not a secret
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
