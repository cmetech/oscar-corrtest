package runtime

import (
	"fmt"
	"os"
	"path/filepath"
)

const compactObservationMaxBytes = 64 << 20

// Reserve before resolving credentials or contacting a target. The temporary
// file is published with Link, so another writer's destination is never replaced.
type compactObservationOutput struct {
	path, lockPath string
	file, lock     *os.File
	attempted      bool
}

func prepareCompactObservationOutput(path string) (*compactObservationOutput, error) {
	if path == "" {
		return nil, fmt.Errorf("compact observation requires an evidence destination")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("compact observation destination unavailable")
	}
	if _, err := os.Lstat(absolute); !os.IsNotExist(err) {
		return nil, fmt.Errorf("compact observation destination must be new")
	}
	w := &compactObservationOutput{path: absolute, lockPath: absolute + ".observe-lock"}
	w.lock, err = os.OpenFile(w.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("compact observation destination reservation unavailable")
	}
	if _, err = os.Lstat(absolute); !os.IsNotExist(err) {
		w.close()
		return nil, fmt.Errorf("compact observation destination must be new")
	}
	w.file, err = os.CreateTemp(filepath.Dir(absolute), ".corrtest-compact-observation-*")
	if err == nil {
		err = w.file.Chmod(0600)
	}
	if err != nil {
		w.close()
		return nil, fmt.Errorf("compact observation staging unavailable")
	}
	return w, nil
}

func (w *compactObservationOutput) publish(raw []byte) error {
	if w.attempted || w.file == nil || len(raw) == 0 || len(raw) > compactObservationMaxBytes {
		return fmt.Errorf("compact observation exceeds artifact bound")
	}
	w.attempted = true
	if _, err := w.file.Write(raw); err != nil {
		return fmt.Errorf("compact observation write unavailable")
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("compact observation sync unavailable")
	}
	if err := os.Link(w.file.Name(), w.path); err != nil {
		return fmt.Errorf("compact observation publication unavailable")
	}
	parent, err := os.Open(filepath.Dir(w.path))
	if err != nil {
		return fmt.Errorf("compact observation directory sync unavailable")
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("compact observation directory sync unavailable")
	}
	return nil
}

func (w *compactObservationOutput) close() {
	for _, file := range []*os.File{w.file, w.lock} {
		if file == nil {
			continue
		}
		owned, err := file.Stat()
		actual, currentErr := os.Lstat(file.Name())
		if err == nil && currentErr == nil && os.SameFile(owned, actual) {
			_ = os.Remove(file.Name())
		}
		_ = file.Close()
	}
}
