package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	brokerconfig "github.com/jamestelfer/imds-broker/pkg/config"
)

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

// runWith builds a command exposing the given flags and a root --log-level
// flag, runs it with args, and invokes capture inside the action so flag state
// reflects parsing.
func runWith(t *testing.T, flags []cli.Flag, args []string, capture func(cmd *cli.Command)) {
	t.Helper()
	cmd := &cli.Command{
		Name: "imds-broker",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "log-level"},
		},
		Commands: []*cli.Command{
			{
				Name:  "sub",
				Flags: flags,
				Action: func(_ context.Context, c *cli.Command) error {
					capture(c)
					return nil
				},
			},
		},
	}
	require.NoError(t, cmd.Run(context.Background(), args))
}

// unsetEnv removes an env var for the test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		}
	})
}

func TestEffectiveFilter_ConfigDefaultUsedWhenFlagAbsent(t *testing.T) {
	unsetEnv(t, "IMDS_BROKER_PROFILE_FILTER")
	cfg := &brokerconfig.Config{ProfileFilter: "ViewOnly"}
	runWith(t, []cli.Flag{profileFilterFlag()},
		[]string{"imds-broker", "sub"},
		func(c *cli.Command) { assert.Equal(t, "ViewOnly", effectiveFilter(c, cfg)) })
}

func TestEffectiveFilter_FlagOverridesConfig(t *testing.T) {
	unsetEnv(t, "IMDS_BROKER_PROFILE_FILTER")
	cfg := &brokerconfig.Config{ProfileFilter: "ViewOnly"}
	runWith(t, []cli.Flag{profileFilterFlag()},
		[]string{"imds-broker", "sub", "--profile-filter", "Admin"},
		func(c *cli.Command) { assert.Equal(t, "Admin", effectiveFilter(c, cfg)) })
}

func TestEffectiveFilter_EnvOverridesConfig(t *testing.T) {
	t.Setenv("IMDS_BROKER_PROFILE_FILTER", "Admin")
	cfg := &brokerconfig.Config{ProfileFilter: "ViewOnly"}
	runWith(t, []cli.Flag{profileFilterFlag()},
		[]string{"imds-broker", "sub"},
		func(c *cli.Command) { assert.Equal(t, "Admin", effectiveFilter(c, cfg)) })
}

func TestEffectiveRegion_ConfigDefaultAndFlagOverride(t *testing.T) {
	cfg := &brokerconfig.Config{Region: "ap-southeast-2"}
	regionFlag := func() []cli.Flag { return []cli.Flag{&cli.StringFlag{Name: "region"}} }
	runWith(t, regionFlag(),
		[]string{"imds-broker", "sub"},
		func(c *cli.Command) { assert.Equal(t, "ap-southeast-2", effectiveRegion(c, cfg)) })
	runWith(t, regionFlag(),
		[]string{"imds-broker", "sub", "--region", "us-east-1"},
		func(c *cli.Command) { assert.Equal(t, "us-east-1", effectiveRegion(c, cfg)) })
}

func TestEffectiveLogLevel_Precedence(t *testing.T) {
	withConfig := &brokerconfig.Config{LogLevel: "debug"}
	noConfig := &brokerconfig.Config{}
	// config default applies when flag absent
	runWith(t, nil, []string{"imds-broker", "sub"},
		func(c *cli.Command) { assert.Equal(t, "debug", effectiveLogLevel(c, withConfig)) })
	// built-in default applies when neither flag nor config set
	runWith(t, nil, []string{"imds-broker", "sub"},
		func(c *cli.Command) { assert.Equal(t, "info", effectiveLogLevel(c, noConfig)) })
	// flag wins over config
	runWith(t, nil, []string{"imds-broker", "--log-level", "warn", "sub"},
		func(c *cli.Command) { assert.Equal(t, "warn", effectiveLogLevel(c, withConfig)) })
}
