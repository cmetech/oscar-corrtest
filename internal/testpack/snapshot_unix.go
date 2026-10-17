//go:build linux || darwin

package testpack

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

func snapshot(directory string) (map[string][]byte, error) {
	// Normalize only syntax so a trailing slash or /. cannot bypass leaf NOFOLLOW.
	directory = filepath.Clean(directory)
	fd, e := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, fmt.Errorf("kit directory: %w", e)
	}
	root := os.NewFile(uintptr(fd), directory)
	defer root.Close()
	entries, e := root.ReadDir(11)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) != 10 {
		return nil, fmt.Errorf("expected exactly ten entries")
	}
	allowed := map[string]bool{"manifest.json": true, "manifest.sha256": true}
	for _, n := range names {
		allowed[n] = true
	}
	for _, f := range entries {
		if !allowed[f.Name()] {
			return nil, fmt.Errorf("unexpected artifact")
		}
	}
	blobs := map[string][]byte{}
	for name := range allowed {
		child, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, fmt.Errorf("artifact open: %w", e)
		}
		f := os.NewFile(uintptr(child), name)
		s, e := f.Stat()
		if e != nil || !s.Mode().IsRegular() {
			f.Close()
			return nil, fmt.Errorf("regular artifact required")
		}
		data, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
		closeErr := f.Close()
		if e != nil || closeErr != nil || len(data) > maxBytes {
			return nil, fmt.Errorf("artifact read bound/error")
		}
		blobs[name] = data
	}
	return blobs, nil
}
