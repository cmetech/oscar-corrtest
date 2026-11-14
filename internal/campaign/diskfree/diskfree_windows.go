//go:build windows

// Package diskfree reports free bytes on the filesystem holding a path.
package diskfree

import "errors"

// Bytes is unsupported on Windows; runner hosts are Linux/macOS.
func Bytes(string) (uint64, error) { return 0, errors.New("disk free is not supported on windows") }
