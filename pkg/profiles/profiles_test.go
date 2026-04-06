package profiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jamestelfer/imds-broker/pkg/profiles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig writes a fake ~/.aws/config to a temp dir and sets AWS_CONFIG_FILE.
// It also points AWS_SHARED_CREDENTIALS_FILE at a nonexistent path so no real
// credentials file is read during tests.
func writeConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials_nonexistent"))
}

// writeCredentials writes a fake ~/.aws/credentials file and sets its env var.
func writeCredentials(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "credentials")
	require.NoError(t, os.WriteFile(credsPath, []byte(content), 0o600))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)
}

// Test 1 (tracer bullet): List returns profiles matching the default filter.
func TestList_DefaultFilter(t *testing.T) {
	writeConfig(t, `
[default]
region = us-east-1

[profile prod-ReadOnly]
region = us-east-1

[profile dev-ViewOnly]
region = us-east-1

[profile admin]
region = us-east-1
`)

	result, err := profiles.List("")
	require.NoError(t, err)
	assert.Equal(t, []string{"dev-ViewOnly", "prod-ReadOnly"}, result)
}

// Test 2: List with custom filter returns only matching profiles.
func TestList_CustomFilter(t *testing.T) {
	writeConfig(t, `
[profile prod-ReadOnly]
[profile dev-ViewOnly]
[profile admin]
[profile staging-admin]
`)

	result, err := profiles.List("admin")
	require.NoError(t, err)
	assert.Equal(t, []string{"admin", "staging-admin"}, result)
}

// Test 3: Empty config returns empty list without error.
func TestList_EmptyConfig(t *testing.T) {
	writeConfig(t, "")

	result, err := profiles.List("")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// Test 4: Invalid regex returns a clear error.
func TestList_InvalidRegex_ReturnsError(t *testing.T) {
	writeConfig(t, "[profile prod-ReadOnly]")

	_, err := profiles.List("[invalid")
	require.Error(t, err)
}

// Test 5: Profiles from credentials file are included and deduplicated.
func TestList_CredentialsFileProfiles(t *testing.T) {
	writeConfig(t, `
[profile prod-ReadOnly]
`)
	writeCredentials(t, `
[prod-ReadOnly]
aws_access_key_id = AKIA...

[dev-ReadOnly]
aws_access_key_id = AKIA...
`)

	result, err := profiles.List("ReadOnly")
	require.NoError(t, err)
	assert.Equal(t, []string{"dev-ReadOnly", "prod-ReadOnly"}, result)
}

// Test 6: Default section in config file does not appear by default filter.
func TestList_DefaultSectionNotMatchedByDefaultFilter(t *testing.T) {
	writeConfig(t, `
[default]
region = us-east-1
`)

	result, err := profiles.List("")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// Test 7: Missing config files return empty list (not an error).
func TestList_MissingFiles_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "no_config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "no_credentials"))

	result, err := profiles.List("ReadOnly")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// Test 8: Results are returned in sorted order.
func TestList_SortedOutput(t *testing.T) {
	writeConfig(t, `
[profile z-ReadOnly]
[profile a-ReadOnly]
[profile m-ReadOnly]
`)

	result, err := profiles.List("ReadOnly")
	require.NoError(t, err)
	assert.Equal(t, []string{"a-ReadOnly", "m-ReadOnly", "z-ReadOnly"}, result)
}
