package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig creates a config file under a temporary XDG_CONFIG_HOME and
// points the environment at it.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, RelPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestResolvePath_UsesXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	path, err := ResolvePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/xdg", RelPath), path)
}

func TestResolvePath_FallsBackToHomeConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/example")
	path, err := ResolvePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/home/example", ".config", RelPath), path)
}

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.Found)
	assert.Empty(t, cfg.ProfileFilter)
	assert.Empty(t, cfg.Region)
	assert.Empty(t, cfg.LogLevel)
	assert.Equal(t, filepath.Join(dir, RelPath), cfg.Path)
}

func TestLoad_ValidConfig(t *testing.T) {
	writeConfig(t, "profile-filter: \".*ViewOnly.*\"\nregion: \"ap-southeast-2\"\nlog-level: \"debug\"\n")

	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.Found)
	assert.Equal(t, ".*ViewOnly.*", cfg.ProfileFilter)
	assert.Equal(t, "ap-southeast-2", cfg.Region)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestLoad_EmptyFileReturnsDefaults(t *testing.T) {
	writeConfig(t, "")

	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.Found)
	assert.Empty(t, cfg.ProfileFilter)
}

func TestLoad_UnknownKeyFails(t *testing.T) {
	writeConfig(t, "profile-filter: \"x\"\nunknown-key: \"y\"\n")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown-key")
}

func TestLoad_MalformedFails(t *testing.T) {
	writeConfig(t, "profile-filter: \"x\nregion: [unterminated\n")

	_, err := Load()
	require.Error(t, err)
}

func TestLoad_InvalidRegexFails(t *testing.T) {
	writeConfig(t, "profile-filter: \"[invalid\"\n")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profile-filter")
}

func TestLoad_MultipleDocumentsFails(t *testing.T) {
	writeConfig(t, "profile-filter: \"first\"\n---\nregion: \"second\"\n")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple YAML documents")
}

func TestLoad_InvalidLogLevelFails(t *testing.T) {
	writeConfig(t, "log-level: \"verbose\"\n")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log-level")
}

func TestLoad_UnreadableFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	path := writeConfig(t, "profile-filter: \"x\"\n")
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := Load()
	require.Error(t, err)
}
