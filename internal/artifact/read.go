package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ReadVerifiedBounded validates the bytes actually captured for export. It
// refuses partial data and cannot escape the store if a path changes mid-read.
func (s *Store) ReadVerifiedBounded(ctx context.Context, manifest Manifest, maximum int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maximum < 0 || maximum > 16<<20 || manifest.ByteSize < 0 || manifest.ByteSize > maximum {
		return nil, fmt.Errorf("artifact exceeds bounded read size")
	}
	if err := validateStoredPath(manifest.RelativePath); err != nil {
		return nil, err
	}
	if err := s.verifyParents(filepath.Dir(filepath.Join(s.root, filepath.FromSlash(manifest.RelativePath)))); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(manifest.RelativePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != manifest.ByteSize {
		return nil, fmt.Errorf("artifact is not an exact-size regular file")
	}
	// Nonblocking open makes a concurrently substituted FIFO rejectable by Stat.
	file, err := root.OpenFile(manifest.RelativePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != manifest.ByteSize {
		return nil, fmt.Errorf("opened artifact differs")
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, source: file}, maximum+1))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if int64(len(raw)) != manifest.ByteSize || hex.EncodeToString(digest[:]) != strings.ToLower(manifest.SHA256) {
		return nil, fmt.Errorf("captured artifact failed integrity validation")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
