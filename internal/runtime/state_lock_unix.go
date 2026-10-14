//go:build !windows

package runtime

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockStateFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return fmt.Errorf("state directory already in use by another CorrTest runtime; use its UI or stop it before opening this state directory")
	}
	if err != nil {
		return fmt.Errorf("lock state directory: %w", err)
	}
	return nil
}
