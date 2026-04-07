package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestResolveLogDir_FallsBackToHomeLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := resolveLogDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "state", "sandy", "logs", "imds-broker"), got)
}
