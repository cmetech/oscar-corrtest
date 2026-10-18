//go:build linux || darwin

package testpack

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// The original privately stable directory is a transfer prerequisite. These
// bounded relative-descriptor reads do not claim an atomic filesystem snapshot.
func snapshotInstallation(directory string) (map[string][]byte, error) {
	root, e := openSnapshotDirectory(directory)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	if e = privateInstallationDirectory(root); e != nil {
		return nil, e
	}
	completed, e := snapshotFile(root, "completed.json", true)
	if e != nil {
		return nil, e
	}
	value, e := installationJSON(completed)
	if e != nil {
		return nil, e
	}
	count, ok := value["journal_operations"].(int64)
	if !ok || count < 1 || count > 160 {
		return nil, fmt.Errorf("original journal count required")
	}
	allowed := map[string]bool{"attempt.json": true, "completed.json": true, "kit": true}
	for n := int64(1); n <= count; n++ {
		for _, suffix := range []string{"before", "after"} {
			allowed[fmt.Sprintf("%04d-%s.json", n, suffix)] = true
		}
	}
	if e = snapshotNames(root, allowed); e != nil {
		return nil, e
	}
	blobs := map[string][]byte{"completed.json": completed}
	size := len(completed)
	for name := range allowed {
		if name == "completed.json" || name == "kit" {
			continue
		}
		raw, e := snapshotFile(root, name, true)
		if e != nil {
			return nil, e
		}
		size += len(raw)
		if size > installationJSONLimit {
			return nil, fmt.Errorf("original JSON aggregate exceeds 32 MiB")
		}
		blobs[name] = raw
	}
	fd, e := unix.Openat(int(root.Fd()), "kit", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, fmt.Errorf("original kit directory: %w", e)
	}
	kit := os.NewFile(uintptr(fd), "kit")
	defer kit.Close()
	if e = privateInstallationDirectory(kit); e != nil {
		return nil, e
	}
	kitBlobs, e := snapshotKit(kit, true)
	if e != nil {
		return nil, e
	}
	for name, raw := range kitBlobs {
		blobs["kit/"+name] = raw
	}
	if e = snapshotNames(root, allowed); e != nil {
		return nil, e
	}
	return blobs, nil
}
func privateInstallationDirectory(file *os.File) error {
	s, e := file.Stat()
	if e != nil || !s.IsDir() || s.Mode().Perm() != 0700 || s.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("original directory must be private 0700")
	}
	return nil
}
