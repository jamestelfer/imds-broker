package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamestelfer/imds-broker/pkg/brokerconfig"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAWSConfig writes a fake ~/.aws/config, points AWS_CONFIG_FILE at it, and
// directs AWS_SHARED_CREDENTIALS_FILE at a nonexistent path so no real
// credentials file is read during tests.
func writeAWSConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials_nonexistent"))
}

const profilesFixture = `
[profile prod-ReadOnly]
region = us-east-1

[profile dev-ViewOnly]
region = us-east-1

[profile admin]
region = us-east-1
`

// profileNames decodes the JSON written by runProfiles into a name slice.
func profileNames(t *testing.T, raw []byte) []string {
	t.Helper()
	var ps []profiles.Profile
	require.NoError(t, json.Unmarshal(raw, &ps))
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}

func TestRunProfiles_ConfigFilterIsAuthoritative(t *testing.T) {
	writeAWSConfig(t, profilesFixture)

	var buf bytes.Buffer
	err := runProfiles(context.Background(), brokerconfig.Config{ProfileFilter: "admin"}, "", &buf)
	require.NoError(t, err)

	assert.Equal(t, []string{"admin"}, profileNames(t, buf.Bytes()))
}

func TestRunProfiles_NoConfigUsesDefaultFilter(t *testing.T) {
	writeAWSConfig(t, profilesFixture)

	var buf bytes.Buffer
	err := runProfiles(context.Background(), brokerconfig.Config{}, "", &buf)
	require.NoError(t, err)

	assert.Equal(t, []string{"dev-ViewOnly", "prod-ReadOnly"}, profileNames(t, buf.Bytes()))
}

func TestRunProfiles_IntersectsConfigAndSuppliedFilter(t *testing.T) {
	writeAWSConfig(t, `
[profile prod-ReadOnly]
region = us-east-1

[profile prod-Admin]
region = us-east-1

[profile dev-ReadOnly]
region = us-east-1
`)

	var buf bytes.Buffer
	err := runProfiles(context.Background(), brokerconfig.Config{ProfileFilter: "prod"}, "ReadOnly", &buf)
	require.NoError(t, err)

	assert.Equal(t, []string{"prod-ReadOnly"}, profileNames(t, buf.Bytes()))
}

func TestRunProfiles_SuppliedFilterCannotWidenProtected(t *testing.T) {
	writeAWSConfig(t, profilesFixture)

	var buf bytes.Buffer
	// Protected ReadOnly + supplied ".*" must still yield only ReadOnly.
	err := runProfiles(context.Background(), brokerconfig.Config{ProfileFilter: "ReadOnly"}, ".*", &buf)
	require.NoError(t, err)

	assert.Equal(t, []string{"prod-ReadOnly"}, profileNames(t, buf.Bytes()))
}

func TestRunProfiles_SuppliedFilterAloneWhenConfigOmitsIt(t *testing.T) {
	writeAWSConfig(t, profilesFixture)

	var buf bytes.Buffer
	err := runProfiles(context.Background(), brokerconfig.Config{}, "admin", &buf)
	require.NoError(t, err)

	assert.Equal(t, []string{"admin"}, profileNames(t, buf.Bytes()))
}

func TestOpenLogFile_CreatesDirectoryAndReturnsWriter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	w, err := openLogFile("mycommand")
	require.NoError(t, err)
	require.NotNil(t, w)
	defer func() { _ = w.Close() }()

	logDir := filepath.Join(dir, "sandy", "logs", "imds-broker")
	assert.DirExists(t, logDir)
}

func TestOpenLogFile_FilenameContainsCmdNameAndPID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	w, err := openLogFile("serve")
	require.NoError(t, err)
	defer func() { _ = w.Close() }()

	// Write something so lumberjack actually creates the file.
	_, err = fmt.Fprintf(w, `{"level":"info"}`)
	require.NoError(t, err)

	expected := filepath.Join(dir, "sandy", "logs", "imds-broker",
		fmt.Sprintf("serve-%d.log", os.Getpid()))
	assert.FileExists(t, expected)
}

func TestResolveLogDir_UsesXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/some/custom/state")

	got, err := resolveLogDir()
	require.NoError(t, err)
	assert.Equal(t, "/some/custom/state/sandy/logs/imds-broker", got)
}

func TestNewCommandLogger_WritesTextToExtraWriter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	var buf bytes.Buffer
	logger, lw, err := newCommandLogger("serve", "info", &buf)
	require.NoError(t, err)
	require.NotNil(t, logger)
	defer func() { _ = lw.Close() }()

	logger.Info("hello from serve")

	assert.Contains(t, buf.String(), "hello from serve")
}

func TestNewCommandLogger_NilExtraWriterLogsToFileOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	logger, lw, err := newCommandLogger("serve", "info", nil)
	require.NoError(t, err)
	require.NotNil(t, logger)
	defer func() { _ = lw.Close() }()

	// Should not panic; file log still works.
	logger.Info("quiet mode")
}

func TestResolveLogDir_FallsBackToHomeLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := resolveLogDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "state", "sandy", "logs", "imds-broker"), got)
}
