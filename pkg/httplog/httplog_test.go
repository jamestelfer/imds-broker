package httplog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/httplog"
)

func TestMiddleware_InjectsRequestLoggerAndLogsRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := httplog.Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httplog.FromContext(r.Context()).Info("inner")
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	dec := json.NewDecoder(&buf)
	var inner, outer map[string]any
	require.NoError(t, dec.Decode(&inner))
	require.NoError(t, dec.Decode(&outer))

	assert.Equal(t, "inner", inner["msg"])
	assert.NotEmpty(t, inner["request_id"])
	assert.Equal(t, inner["request_id"], outer["request_id"])
	assert.Equal(t, "http request", outer["msg"])
	assert.InDelta(t, http.StatusTeapot, outer["status"], 0)
	assert.Equal(t, "/x", outer["path"])
}

func TestFromContext_DefaultsWhenAbsent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, slog.Default(), httplog.FromContext(req.Context()))
}
