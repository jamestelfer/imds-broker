package containercreds

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTokenFile_ModeAndContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	tok, err := newToken()
	require.NoError(t, err)

	require.NoError(t, writeTokenFile(path, tok))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, tok, got)
}

func TestWriteTokenFile_ReplacesLooserExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	require.NoError(t, writeTokenFile(path, []byte("new")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
}

func TestWriteTokenFile_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	for range 5 {
		require.NoError(t, writeTokenFile(path, []byte("x")))
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "token", entries[0].Name())
}

func TestWriteTokenFile_MissingDirectoryFails(t *testing.T) {
	err := writeTokenFile(filepath.Join(t.TempDir(), "absent", "token"), []byte("x"))
	assert.Error(t, err)
}

func TestWriteTokenFile_ConcurrentReaderNeverSeesPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	tokens := make(map[string]bool)
	for range 20 {
		tok, err := newToken()
		require.NoError(t, err)
		tokens[string(tok)] = true
	}

	var stop atomic.Bool
	var bad atomic.Value
	var wg sync.WaitGroup
	wg.Go(func() {
		for !stop.Load() {
			got, err := os.ReadFile(path)
			if os.IsNotExist(err) || len(got) == 0 {
				continue
			}
			if err != nil {
				bad.Store("read error: " + err.Error())
				return
			}
			if !tokens[string(got)] {
				bad.Store("partial or unknown token: " + string(got))
				return
			}
		}
	})

	for range 5 {
		for tok := range tokens {
			require.NoError(t, writeTokenFile(path, []byte(tok)))
		}
	}
	stop.Store(true)
	wg.Wait()

	assert.Nil(t, bad.Load())
}

func TestRemoveTokenFile_OnlyRemovesOwnToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, writeTokenFile(path, []byte("other")))

	require.NoError(t, removeTokenFile(path, []byte("mine")))
	assert.FileExists(t, path)

	require.NoError(t, removeTokenFile(path, []byte("other")))
	assert.NoFileExists(t, path)

	require.NoError(t, removeTokenFile(path, []byte("other")))
}
