// Package artifact owns traversal-safe, hash-verified local evidence files.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var runIDPattern = regexp.MustCompile(`^crt_[0-9A-HJKMNP-TV-Z]{26}$`)

// Integrity is the observable state of an artifact against its manifest.
type Integrity string

const (
	IntegrityValid        Integrity = "valid"
	IntegrityMissing      Integrity = "missing"
	IntegrityHashMismatch Integrity = "hash_mismatch"
)

// Manifest is the durable metadata required to verify one artifact.
type Manifest struct {
	RelativePath string `json:"path"`
	MIMEType     string `json:"mimeType"`
	SHA256       string `json:"sha256"`
	ByteSize     int64  `json:"byteSize"`
}

// Store owns one local application data directory.
type Store struct{ root string }

// New opens an artifact store rooted at an absolute local directory.
func New(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("artifact root %q must be absolute", root)
	}
	root = filepath.Clean(root)
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("artifact root %q must be a real directory", root)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, fmt.Errorf("create artifact root: %w", err)
		}
	} else {
		return nil, fmt.Errorf("inspect artifact root: %w", err)
	}
	// #nosec G302 -- directories require execute permission; 0700 is the restrictive directory contract.
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("restrict artifact root: %w", err)
	}
	return &Store{root: root}, nil
}

// Write atomically publishes one new run-scoped artifact without overwrite.
func (s *Store) Write(ctx context.Context, runID, name, mimeType string, source io.Reader) (Manifest, error) {
	if !runIDPattern.MatchString(runID) {
		return Manifest{}, fmt.Errorf("invalid run ID %q", runID)
	}
	if err := validateRelative(name); err != nil {
		return Manifest{}, err
	}
	if mimeType == "" {
		return Manifest{}, fmt.Errorf("artifact MIME type is required")
	}
	relative := path.Join("runs", runID, name)
	full := filepath.Join(s.root, filepath.FromSlash(relative))
	parent := filepath.Dir(full)
	if err := s.ensureDirectories(parent); err != nil {
		return Manifest{}, err
	}
	if _, err := os.Lstat(full); err == nil {
		return Manifest{}, fmt.Errorf("artifact %q already exists", relative)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("inspect artifact destination: %w", err)
	}

	temporary, err := os.CreateTemp(parent, ".corrtest-artifact-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("create artifact temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return Manifest{}, fmt.Errorf("restrict artifact temporary file: %w", err)
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), contextReader{ctx: ctx, source: source})
	if err != nil {
		return Manifest{}, fmt.Errorf("write artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return Manifest{}, fmt.Errorf("sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close artifact: %w", err)
	}
	// A hard-link install is atomic on the same filesystem and fails rather than overwriting.
	if err := os.Link(temporaryPath, full); err != nil {
		return Manifest{}, fmt.Errorf("publish artifact: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(full)
		return Manifest{}, fmt.Errorf("remove artifact staging name: %w", err)
	}
	removeTemporary = false
	if err := syncDirectory(parent); err != nil {
		return Manifest{}, err
	}
	return Manifest{
		RelativePath: relative,
		MIMEType:     mimeType,
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		ByteSize:     size,
	}, nil
}

// Verify compares a stored artifact with its durable manifest.
func (s *Store) Verify(ctx context.Context, manifest Manifest) (Integrity, error) {
	if err := validateStoredPath(manifest.RelativePath); err != nil {
		return "", err
	}
	full := filepath.Join(s.root, filepath.FromSlash(manifest.RelativePath))
	if err := s.verifyParents(filepath.Dir(full)); err != nil {
		return "", err
	}
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return IntegrityMissing, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("artifact %q is not a regular file", manifest.RelativePath)
	}
	// #nosec G304 -- full was validated beneath a 0700 root and every parent was lstat-checked as a non-symlink directory.
	file, err := os.Open(full)
	if err != nil {
		return "", fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, contextReader{ctx: ctx, source: file})
	if err != nil {
		return "", fmt.Errorf("verify artifact: %w", err)
	}
	if size != manifest.ByteSize || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(manifest.SHA256) {
		return IntegrityHashMismatch, nil
	}
	return IntegrityValid, nil
}

func validateRelative(value string) error {
	if value == "" || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") {
		return fmt.Errorf("artifact path %q is invalid", value)
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("artifact path %q is invalid", value)
		}
	}
	if path.Clean(value) != value {
		return fmt.Errorf("artifact path %q is not canonical", value)
	}
	return nil
}

func validateStoredPath(value string) error {
	parts := strings.Split(value, "/")
	if len(parts) < 3 || parts[0] != "runs" || !runIDPattern.MatchString(parts[1]) {
		return fmt.Errorf("stored artifact path %q is invalid", value)
	}
	return validateRelative(value)
}

func (s *Store) ensureDirectories(directory string) error {
	relative, err := filepath.Rel(s.root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact directory escapes data root")
	}
	current := s.root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("artifact directory %q is not a real directory", current)
			}
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create artifact directory: %w", err)
			}
		default:
			return fmt.Errorf("inspect artifact directory: %w", err)
		}
	}
	return nil
}

func (s *Store) verifyParents(directory string) error {
	relative, err := filepath.Rel(s.root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact directory escapes data root")
	}
	current := s.root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspect artifact parent: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact parent %q is not a real directory", current)
		}
	}
	return nil
}

func syncDirectory(directory string) error {
	// #nosec G304 -- directory was derived beneath the validated store root and checked component by component.
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open artifact directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.source.Read(buffer)
	}
}
