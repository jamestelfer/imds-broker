package profiles_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamestelfer/imds-broker/pkg/profiles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig writes a fake ~/.aws/config and sets AWS_CONFIG_FILE.
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

// writeCredentials writes a fake ~/.aws/credentials and sets AWS_SHARED_CREDENTIALS_FILE.
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

	result, err := profiles.List(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, []profiles.Profile{
		{Name: "dev-ViewOnly", Region: "us-east-1"},
		{Name: "prod-ReadOnly", Region: "us-east-1"},
	}, result)
}

// Test 2: List with custom filter returns only matching profiles.
func TestList_CustomFilter(t *testing.T) {
	writeConfig(t, `
[profile prod-ReadOnly]
region = us-east-1
[profile dev-ViewOnly]
region = us-east-1
[profile admin]
region = us-east-1
[profile staging-admin]
region = us-east-1
`)

	result, err := profiles.List(context.Background(), "admin")
	require.NoError(t, err)
	assert.Equal(t, []profiles.Profile{
		{Name: "admin", Region: "us-east-1"},
		{Name: "staging-admin", Region: "us-east-1"},
	}, result)
}

// Test 3: Empty config returns empty list without error.
func TestList_EmptyConfig(t *testing.T) {
	writeConfig(t, "")

	result, err := profiles.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// Test 4: Invalid regex returns a clear error.
func TestList_InvalidRegex_ReturnsError(t *testing.T) {
	writeConfig(t, "[profile prod-ReadOnly]\nregion = us-east-1")

	_, err := profiles.List(context.Background(), "[invalid")
	require.Error(t, err)
}

// Test 5: Profiles from credentials file are included and deduplicated.
func TestList_CredentialsFileProfiles(t *testing.T) {
	writeConfig(t, `
[profile prod-ReadOnly]
region = us-east-1
`)
	writeCredentials(t, `
[prod-ReadOnly]
aws_access_key_id = AKIA...
aws_secret_access_key = secret

[dev-ReadOnly]
aws_access_key_id = AKIA...
aws_secret_access_key = secret
`)

	result, err := profiles.List(context.Background(), "ReadOnly")
	require.NoError(t, err)
	assert.Equal(t, []profiles.Profile{
		{Name: "dev-ReadOnly"},
		{Name: "prod-ReadOnly", Region: "us-east-1"},
	}, result)
}

// Test 6: [default] section does not match the default filter.
func TestList_DefaultSectionNotMatchedByDefaultFilter(t *testing.T) {
	writeConfig(t, `
[default]
region = us-east-1
`)

	result, err := profiles.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// Test 7: Missing config files return empty list (not an error).
func TestList_MissingFiles_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "no_config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "no_credentials"))

	result, err := profiles.List(context.Background(), "ReadOnly")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// Test 9: Account ID is extracted from role_arn when present.
func TestList_AccountIDFromRoleARN(t *testing.T) {
	writeConfig(t, `
[profile prod-ReadOnly]
region = ap-southeast-2
role_arn = arn:aws:iam::123456789012:role/ReadOnly
`)

	result, err := profiles.List(context.Background(), "ReadOnly")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "123456789012", result[0].AccountID)
}

// Test 10: Account ID falls back to granted_sso_account_id when no role_arn.
func TestList_AccountIDFromGrantedSSO(t *testing.T) {
	writeConfig(t, `
[profile prod-ReadOnly]
region = ap-southeast-2
granted_sso_account_id = 987654321098
`)

	result, err := profiles.List(context.Background(), "ReadOnly")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "987654321098", result[0].AccountID)
}

// Test 8: Results are sorted alphabetically.
func TestList_SortedOutput(t *testing.T) {
	writeConfig(t, `
[profile z-ReadOnly]
region = us-east-1
[profile a-ReadOnly]
region = us-east-1
[profile m-ReadOnly]
region = us-east-1
`)

	result, err := profiles.List(context.Background(), "ReadOnly")
	require.NoError(t, err)
	assert.Equal(t, []profiles.Profile{
		{Name: "a-ReadOnly", Region: "us-east-1"},
		{Name: "m-ReadOnly", Region: "us-east-1"},
		{Name: "z-ReadOnly", Region: "us-east-1"},
	}, result)
}
