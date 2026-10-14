package runtime

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockStateFile(file *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return fmt.Errorf("state directory already in use by another CorrTest runtime; use its UI or stop it before opening this state directory")
	}
	if err != nil {
		return fmt.Errorf("lock state directory: %w", err)
	}
	return nil
}
