package containercreds

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// writeTokenFile atomically replaces path with token: a reader sees the old
// file, no file, or the whole new token, never a partial write. The temporary
// file lives in the target directory so the rename stays on one filesystem.
//
// os.CreateTemp creates the file with mode 0600, so a client container must
// run as the broker's UID, or as root, to read a shared token file.
func writeTokenFile(path string, token []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("containercreds: create token file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err := tmp.Write(token); err != nil {
		return fmt.Errorf("containercreds: write token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("containercreds: close token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("containercreds: replace token file: %w", err)
	}
	return nil
}

// removeTokenFile deletes path only if it still holds token, so a stopped
// server leaves no stale readiness signal and never deletes a token written
// by a newer instance.
func removeTokenFile(path string, token []byte) error {
	got, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if errors.Is(err, os.ErrNotExist) || (err == nil && !bytes.Equal(got, token)) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("containercreds: read token file: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("containercreds: remove token file: %w", err)
	}
	return nil
}
