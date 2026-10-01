package containercreds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rotatingSource issues credentials valid for one hour, with a new access key
// on every call.
type rotatingSource struct{ calls int }

func (s *rotatingSource) Retrieve(context.Context) (aws.Credentials, error) {
	s.calls++
	return aws.Credentials{
		AccessKeyID:     fmt.Sprintf("AKID%d", s.calls),
		SecretAccessKey: "secret",
		SessionToken:    "session",
		CanExpire:       true,
		Expires:         time.Now().Add(time.Hour),
	}, nil
}

func TestHandler_ServesCachedCredentialsUntilExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &rotatingSource{}
		cache := aws.NewCredentialsCache(source, func(o *aws.CredentialsCacheOptions) {
			o.ExpiryWindow = 0
		})
		token := []byte("token")
		h := newHandler(token, slog.New(slog.DiscardHandler), cache)

		fetch := func() map[string]string {
			req := httptest.NewRequest(http.MethodGet, Path, nil)
			req.Header.Set("Authorization", string(token))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var got map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			return got
		}

		first := fetch()
		time.Sleep(59 * time.Minute)
		second := fetch()
		assert.Equal(t, 1, source.calls, "unexpired credentials must not be re-resolved")
		assert.Equal(t, first, second)

		time.Sleep(2 * time.Minute)
		third := fetch()
		assert.Equal(t, 2, source.calls)
		assert.Equal(t, "AKID2", third["AccessKeyId"])
		assert.Equal(t, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), third["Expiration"])
	})
}
