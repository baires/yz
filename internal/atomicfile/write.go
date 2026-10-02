// Package atomicfile writes private files so a crash leaves the old or the
// new contents, never a truncated file.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpError distinguishes a directory failure from a file write so callers can
// keep their existing messages.
type OpError struct {
	Op  string
	Err error
}

func (e *OpError) Error() string { return e.Err.Error() }

func (e *OpError) Unwrap() error { return e.Err }

// Write replaces dir/name with data. A missing directory is created mode
// 0700. The file is mode 0600.
func Write(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &OpError{Op: "dir", Err: fmt.Errorf("creating directory: %w", err)}
	}
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("setting permissions: %w", err)
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}
