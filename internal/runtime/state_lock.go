package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The lock file stays in place: unlinking it would let another owner lock a
// different inode. The kernel releases ownership on Close or process death.
// Like SQLite, this requires a local filesystem and a private stable directory.
func acquireStateLock(directory string) (*os.File, error) {
	path := filepath.Join(directory, ".corrtest.lock")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("state lock is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect state lock: %w", err)
	}
	// #nosec G304 -- operator-selected private local state directory, validated below.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened state lock: %w", err)
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return nil, fmt.Errorf("state lock identity changed or is not a regular file")
	}
	if err := lockStateFile(file); err != nil {
		return nil, err
	}
	failed = false
	return file, nil
}
