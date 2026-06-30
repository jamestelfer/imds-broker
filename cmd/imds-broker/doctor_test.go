package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
)

// doctorFixture sets up a temporary XDG_CONFIG_HOME (optionally with a config
// file) and a temporary AWS config file, then returns a buffer capturing
// doctor's output and a run function that returns the command error.
func doctorFixture(t *testing.T, configYAML *string, awsConfig string) (*bytes.Buffer, func(args ...string) error) {
	t.Helper()
	unsetEnv(t, "IMDS_BROKER_PROFILE_FILTER")

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if configYAML != nil {
		path := filepath.Join(xdg, brokerconfig.RelPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(*configYAML), 0o600))
	}

	awsDir := t.TempDir()
	awsPath := filepath.Join(awsDir, "config")
	require.NoError(t, os.WriteFile(awsPath, []byte(awsConfig), 0o600))
	t.Setenv("AWS_CONFIG_FILE", awsPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(awsDir, "credentials_nonexistent"))

	buf := &bytes.Buffer{}
	run := func(args ...string) error {
		app := &cli.Command{
			Name:   "imds-broker",
			Writer: buf,
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "log-level"},
			},
			Commands: []*cli.Command{doctorCommand()},
		}
		return app.Run(context.Background(), append([]string{"imds-broker"}, args...))
	}
	return buf, run
}

const awsThreeProfiles = `
[profile prod-ReadOnly]
region = us-east-1
[profile dev-ViewOnly]
region = us-east-1
[profile admin]
region = us-east-1
`

func TestDoctor_ValidConfigReportsSourcesAndCounts(t *testing.T) {
	yaml := "profile-filter: \"ViewOnly\"\nregion: \"ap-southeast-2\"\nlog-level: \"debug\"\n"
	buf, run := doctorFixture(t, &yaml, awsThreeProfiles)

	require.NoError(t, run("doctor"))
	out := buf.String()

	assert.Contains(t, out, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), brokerconfig.RelPath))
	assert.Contains(t, out, "found")
	assert.Contains(t, out, "profile filter: ViewOnly  (source: config file)")
	assert.Contains(t, out, "region:         ap-southeast-2  (source: config file)")
	assert.Contains(t, out, "log level:      debug  (source: config file)")
	assert.Contains(t, out, "discoverable profiles: 3")
	assert.Contains(t, out, "matched by filter:     1")
	assert.Contains(t, out, "imds-broker profiles")
	assert.Contains(t, out, "advisory only")
	// must not list matched profile names
	assert.NotContains(t, out, "dev-ViewOnly")
}

func TestDoctor_MissingConfigUsesDefaults(t *testing.T) {
	buf, run := doctorFixture(t, nil, awsThreeProfiles)

	require.NoError(t, run("doctor"))
	out := buf.String()

	assert.Contains(t, out, "not found")
	assert.Contains(t, out, "(source: built-in default)")
	assert.Contains(t, out, "log level:      info  (source: built-in default)")
}

func TestDoctor_FlagOverridesFilterSource(t *testing.T) {
	yaml := "profile-filter: \"ViewOnly\"\n"
	buf, run := doctorFixture(t, &yaml, awsThreeProfiles)

	require.NoError(t, run("doctor", "--profile-filter", "admin"))
	out := buf.String()

	assert.Contains(t, out, "profile filter: admin  (source: command-line flag)")
	assert.Contains(t, out, "matched by filter:     1")
}

func TestDoctor_EmptyFlagFilterNormalisesToDefault(t *testing.T) {
	yaml := "profile-filter: \"ViewOnly\"\n"
	buf, run := doctorFixture(t, &yaml, awsThreeProfiles)

	// An explicit empty --profile-filter must report and count against the
	// built-in default, not the empty string.
	require.NoError(t, run("doctor", "--profile-filter", ""))
	out := buf.String()

	assert.Contains(t, out, "profile filter: ReadOnly|ViewOnly  (source: command-line flag)")
	// ReadOnly|ViewOnly matches prod-ReadOnly and dev-ViewOnly.
	assert.Contains(t, out, "matched by filter:     2")
}

func TestDoctor_ZeroMatchExitsZero(t *testing.T) {
	yaml := "profile-filter: \"NoSuchProfile\"\n"
	buf, run := doctorFixture(t, &yaml, awsThreeProfiles)

	require.NoError(t, run("doctor"))
	assert.Contains(t, buf.String(), "matched by filter:     0")
}

func TestDoctor_UnknownKeyExitsNonZero(t *testing.T) {
	yaml := "bogus-key: \"x\"\n"
	_, run := doctorFixture(t, &yaml, awsThreeProfiles)

	err := run("doctor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configuration error")
}

func TestDoctor_InvalidRegexExitsNonZero(t *testing.T) {
	yaml := "profile-filter: \"[invalid\"\n"
	_, run := doctorFixture(t, &yaml, awsThreeProfiles)

	require.Error(t, run("doctor"))
}

func TestDoctor_InvalidLogLevelExitsNonZero(t *testing.T) {
	yaml := "log-level: \"verbose\"\n"
	_, run := doctorFixture(t, &yaml, awsThreeProfiles)

	require.Error(t, run("doctor"))
}

func TestDoctor_InvalidFlagFilterIsConfigurationError(t *testing.T) {
	buf, run := doctorFixture(t, nil, awsThreeProfiles)

	err := run("doctor", "--profile-filter", "[invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configuration error")
	// distinct from a profile discovery failure message
	assert.NotContains(t, buf.String(), "discovery failure")
}
