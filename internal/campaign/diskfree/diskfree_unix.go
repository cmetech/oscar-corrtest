//go:build !windows

// Package diskfree reports free bytes on the filesystem holding a path.
package diskfree

import "syscall"

func Bytes(path string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil // #nosec G115 -- sizes are non-negative
}
