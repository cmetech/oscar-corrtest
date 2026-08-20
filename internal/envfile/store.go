// Package envfile manages the small user-scoped dotenv overlay used by
// oscar-corrtest. Secret values never appear in returned errors.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	maxFileBytes  = 64 << 10
	maxValueBytes = 16 << 10
)

var keyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Source describes which layer currently supplies a key.
type Source string

const (
	SourceUnset        Source = "unset"
	SourceExternal     Source = "external"
	SourceManaged      Source = "managed"
	SourceLiveOverride Source = "live-override"
)

// KeyStatus is deliberately value-free and safe for UI/API responses.
type KeyStatus struct {
	Configured               bool   `json:"configured"`
	Source                   Source `json:"source"`
	ExternalResumesOnRestart bool   `json:"externalResumesOnRestart"`
}

type liveValue struct {
	value string
	set   bool
}

// Store owns one managed dotenv file and a process-local live overlay.
type Store struct {
	mu      sync.RWMutex
	path    string
	lookup  func(string) (string, bool)
	managed map[string]string
	live    map[string]liveValue
	lines   []string
	newline string
}

// Open reads path when present. A nil lookup uses os.LookupEnv.
func Open(path string, lookup func(string) (string, bool)) (*Store, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("managed environment path is required")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the resolved application-owned environment file.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read managed environment: %w", err)
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("managed environment file exceeds %d bytes", maxFileBytes)
	}
	newline := "\n"
	if strings.Contains(string(data), "\r\n") {
		newline = "\r\n"
	}
	lines := splitLines(string(data))
	managed := make(map[string]string)
	for index, line := range lines {
		key, value, assignment, parseErr := parseLine(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if parseErr != nil {
			return nil, fmt.Errorf("parse managed environment line %d: %w", index+1, parseErr)
		}
		if assignment {
			managed[key] = value
		}
	}
	return &Store{path: path, lookup: lookup, managed: managed, live: make(map[string]liveValue), lines: lines, newline: newline}, nil
}

// Getenv resolves live override, external process environment, then managed file.
func (s *Store) Getenv(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if value, ok := s.live[key]; ok {
		if value.set {
			return value.value
		}
		return ""
	}
	if value, ok := s.lookup(key); ok && value != "" {
		return value
	}
	return s.managed[key]
}

// Status reports configuration origin without revealing secret-derived data.
func (s *Store) Status(key string) KeyStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	external, externalSet := s.lookup(key)
	externalSet = externalSet && external != ""
	if value, ok := s.live[key]; ok {
		return KeyStatus{Configured: value.set && value.value != "", Source: SourceLiveOverride, ExternalResumesOnRestart: externalSet}
	}
	if externalSet {
		return KeyStatus{Configured: true, Source: SourceExternal}
	}
	if value, ok := s.managed[key]; ok && value != "" {
		return KeyStatus{Configured: true, Source: SourceManaged}
	}
	return KeyStatus{Source: SourceUnset}
}

// Replace atomically persists and activates key.
func (s *Store) Replace(key, value string) error {
	if err := validate(key, value); err != nil {
		return err
	}
	return s.rewrite(key, &value)
}

// Clear atomically removes the managed assignment and masks any external value
// for the remainder of this process.
func (s *Store) Clear(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("invalid managed environment key")
	}
	return s.rewrite(key, nil)
}

func (s *Store) rewrite(key string, value *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, 0, len(s.lines)+1)
	for _, line := range s.lines {
		parsedKey, _, assignment, _ := parseLine(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if assignment && parsedKey == key {
			continue
		}
		result = append(result, line)
	}
	if value != nil {
		if len(result) > 0 && !strings.HasSuffix(result[len(result)-1], "\n") {
			result[len(result)-1] += s.newline
		}
		result = append(result, key+"="+encodeValue(*value)+s.newline)
	}
	doc := strings.Join(result, "")
	if len(doc) > maxFileBytes {
		return fmt.Errorf("managed environment file exceeds %d bytes", maxFileBytes)
	}
	if err := atomicWrite(s.path, []byte(doc)); err != nil {
		return fmt.Errorf("replace managed environment: %w", err)
	}
	s.lines = result
	if value == nil {
		delete(s.managed, key)
		s.live[key] = liveValue{}
	} else {
		s.managed[key] = *value
		s.live[key] = liveValue{value: *value, set: true}
	}
	return nil
}

func validate(key, value string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("invalid managed environment key")
	}
	if len(value) > maxValueBytes || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("invalid managed environment value")
	}
	return nil
}

func parseLine(line string) (string, string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false, nil
	}
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}
	key, raw, ok := strings.Cut(trimmed, "=")
	key = strings.TrimSpace(key)
	if !ok || !keyPattern.MatchString(key) {
		return "", "", false, fmt.Errorf("invalid assignment")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return key, "", true, nil
	}
	if raw[0] == '\'' {
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", "", false, fmt.Errorf("unterminated single-quoted value")
		}
		return key, raw[1 : len(raw)-1], true, nil
	}
	if raw[0] == '"' {
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", "", false, fmt.Errorf("invalid double-quoted value")
		}
		return key, value, true, nil
	}
	if comment := strings.Index(raw, " #"); comment >= 0 {
		raw = strings.TrimSpace(raw[:comment])
	}
	return key, raw, true, nil
}

func encodeValue(value string) string {
	if value == "" || strings.ContainsAny(value, " \t#'\"") {
		return strconv.Quote(value)
	}
	return value
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	var result []string
	scanner := bufio.NewScanner(strings.NewReader(value))
	scanner.Split(scanLinesWithEndings)
	for scanner.Scan() {
		result = append(result, scanner.Text())
	}
	return result
}

func scanLinesWithEndings(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := strings.IndexByte(string(data), '\n'); index >= 0 {
		return index + 1, data[:index+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	remove = false
	return nil
}
