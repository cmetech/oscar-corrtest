//go:build linux || darwin

package command

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Same supported platform boundary as the fixed Kit loader. The declaration is
// a bounded copied operator statement, never machine proof of target ownership.
func readInstalledOwnership(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open regular ownership declaration")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, fmt.Errorf("ownership declaration must be a regular file at most64KiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return nil, fmt.Errorf("ownership declaration read exceeds bound")
	}
	return raw, nil
}
