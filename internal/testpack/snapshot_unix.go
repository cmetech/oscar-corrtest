//go:build linux || darwin

package testpack

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openSnapshotDirectory(directory string) (*os.File, error) {
	// Normalize syntax so a trailing slash or /. cannot bypass leaf NOFOLLOW.
	directory = filepath.Clean(directory)
	fd, e := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, fmt.Errorf("snapshot directory: %w", e)
	}
	return os.NewFile(uintptr(fd), directory), nil
}
func snapshot(directory string) (map[string][]byte, error) {
	root, e := openSnapshotDirectory(directory)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	return snapshotKit(root, false)
}
func snapshotKit(root *os.File, private bool) (map[string][]byte, error) {
	allowed := map[string]bool{"manifest.json": true, "manifest.sha256": true}
	for _, name := range names {
		allowed[name] = true
	}
	if e := snapshotNames(root, allowed); e != nil {
		return nil, e
	}
	blobs := map[string][]byte{}
	for name := range allowed {
		raw, e := snapshotFile(root, name, private)
		if e != nil {
			return nil, e
		}
		blobs[name] = raw
	}
	if e := snapshotNames(root, allowed); e != nil {
		return nil, e
	}
	return blobs, nil
}
func snapshotNames(root *os.File, allowed map[string]bool) error {
	if _, e := root.Seek(0, 0); e != nil {
		return e
	}
	entries, e := root.ReadDir(len(allowed) + 1)
	if e != nil && e != io.EOF {
		return e
	}
	if len(entries) != len(allowed) {
		return fmt.Errorf("snapshot entry count differs")
	}
	for _, f := range entries {
		if !allowed[f.Name()] {
			return fmt.Errorf("unexpected snapshot entry")
		}
	}
	return nil
}
func snapshotFile(root *os.File, name string, private bool) ([]byte, error) {
	fd, e := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, fmt.Errorf("snapshot file open: %w", e)
	}
	file := os.NewFile(uintptr(fd), name)
	s, e := file.Stat()
	if e != nil || !s.Mode().IsRegular() || (private && (s.Mode().Perm() != 0600 || s.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0)) {
		file.Close()
		return nil, fmt.Errorf("bounded regular private file required")
	}
	raw, e := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if e != nil || closeErr != nil || len(raw) > maxBytes {
		return nil, fmt.Errorf("snapshot file bound or read error")
	}
	return raw, nil
}
