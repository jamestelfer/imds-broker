package containercreds

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// writeTokenFile atomically replaces path with token. The temporary file is
// created in the target directory so the rename stays on one filesystem, and
// os.CreateTemp creates it with mode 0600. A reader therefore sees either the
// previous file (or none) or the complete new token, never a partial write.
//
// Owner-only mode means a client container must run as the broker's UID (or
// root) to read a shared token file.
func writeTokenFile(path string, token []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("containercreds: create token file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	// Enforce the mode explicitly; it must not depend on CreateTemp defaults.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("containercreds: chmod token file: %w", err)
	}
	if _, err := tmp.Write(token); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("containercreds: write token file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("containercreds: sync token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("containercreds: close token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("containercreds: replace token file: %w", err)
	}
	return nil
}

// removeTokenFile deletes path if it still holds token, so a stopped server
// does not leave a stale readiness signal and does not delete a file a newer
// server instance has written.
func removeTokenFile(path string, token []byte) error {
	got, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("containercreds: read token file: %w", err)
	}
	if string(got) != string(token) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("containercreds: remove token file: %w", err)
	}
	return nil
}
