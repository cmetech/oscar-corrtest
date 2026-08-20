// Package releasearchive creates and inspects deterministic release archives.
package releasearchive

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type zipEntry struct {
	path string
	name string
	info fs.FileInfo
}

// WriteZip writes the contents of rootDir to outputPath in deterministic order.
func WriteZip(outputPath, rootDir string, epoch time.Time) (returnErr error) {
	rootInfo, err := os.Stat(rootDir)
	if err != nil {
		return fmt.Errorf("stat staged root: %w", err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("staged root is not a directory: %s", rootDir)
	}

	var entries []zipEntry
	err = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == rootDir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in release archive: %s", path)
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("unsupported file type in release archive: %s", path)
		}
		relative, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if info.IsDir() {
			name += "/"
		}
		entries = append(entries, zipEntry{path: path, name: name, info: info})
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect staged tree: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary, err := os.CreateTemp(outputDir, "."+filepath.Base(outputPath)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()

	archive := zip.NewWriter(temporary)
	for _, entry := range entries {
		header, err := zip.FileInfoHeader(entry.info)
		if err != nil {
			return fmt.Errorf("create ZIP header for %s: %w", entry.name, err)
		}
		header.Name = entry.name
		header.Method = zip.Deflate
		header.Modified = epoch.UTC()
		header.SetMode(entry.info.Mode())
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("create ZIP entry %s: %w", entry.name, err)
		}
		if entry.info.IsDir() {
			continue
		}
		file, err := os.Open(entry.path)
		if err != nil {
			return fmt.Errorf("open staged file %s: %w", entry.name, err)
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("write ZIP entry %s: %w", entry.name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close staged file %s: %w", entry.name, closeErr)
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finish ZIP archive: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set ZIP archive permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close ZIP archive: %w", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return fmt.Errorf("publish ZIP archive: %w", err)
	}
	return nil
}

// ListZip returns ZIP member names in lexical order without extracting files.
func ListZip(path string) ([]string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open ZIP archive: %w", err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		if strings.ContainsRune(file.Name, '\x00') {
			return nil, fmt.Errorf("invalid ZIP member name")
		}
		names = append(names, file.Name)
	}
	sort.Strings(names)
	return names, nil
}
