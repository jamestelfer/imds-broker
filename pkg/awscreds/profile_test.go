package awscreds_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/awscreds"
)

// TestResolveProfile_RejectsBlankProfile sets environment credentials, which
// the SDK would fall back to for an empty profile.
func TestResolveProfile_RejectsBlankProfile(t *testing.T) {
	for name, profile := range map[string]string{"empty": "", "whitespace": " \t"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
			t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENVFALLBACK00000")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
			t.Setenv("AWS_SESSION_TOKEN", "env-session")

			_, err := awscreds.ResolveProfile(t.Context(), profile, "us-east-1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "profile name is required")
		})
	}
}
