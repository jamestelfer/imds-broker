package brokerconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePath_UsesXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/some/custom/config")

	got, err := ResolvePath()
	require.NoError(t, err)
	assert.Equal(t, "/some/custom/config/imds-broker/config.yaml", got)
}

func TestResolvePath_FallsBackToHomeDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := ResolvePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".config", "imds-broker", "config.yaml"), got)
}

func TestLoad_AbsentFileYieldsZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, Config{}, cfg)
}

func TestLoad_ReadsRecognisedKeys(t *testing.T) {
	path := writeConfig(t, `
profile-filter: "Prod"
region: ap-southeast-2
log-level: debug
`)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, Config{ProfileFilter: "Prod", Region: "ap-southeast-2", LogLevel: "debug"}, cfg)
}

func TestLoad_IgnoresUnknownKeys(t *testing.T) {
	path := writeConfig(t, `
profile-filter: "ReadOnly"
quiet: true
bind-addr: "0.0.0.0:0"
`)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "ReadOnly", cfg.ProfileFilter)
}

func TestLoad_MalformedFileReturnsError(t *testing.T) {
	path := writeConfig(t, "profile-filter: \"unterminated\n  : : :")

	_, err := Load(path)
	require.Error(t, err)
}

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
