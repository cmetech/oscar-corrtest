package applog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Record is the bounded, redacted log representation exposed to the UI.
type Record struct {
	Sequence   uint64            `json:"sequence"`
	Time       time.Time         `json:"time"`
	Level      string            `json:"level"`
	Source     string            `json:"source"`
	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Source is an allowlisted application-owned file.
type Source struct {
	Name         string `json:"name"`
	Path         string `json:"-"`
	Downloadable bool   `json:"downloadable"`
}

// Subscription is a cancellable nonblocking record stream.
type Subscription struct {
	C      <-chan Record
	Cancel func()
}

// Options configures bounded logging resources.
type Options struct {
	MaxBytes         int64
	Backups          int
	RingSize         int
	SubscriberBuffer int
	Now              func() time.Time
}

// System owns all application logging sinks.
type System struct {
	mu       sync.RWMutex
	writer   io.WriteCloser
	stderr   io.Writer
	logDir   string
	options  Options
	ring     []Record
	subs     map[uint64]chan Record
	nextSub  uint64
	sequence atomic.Uint64
	closed   bool
}

// Open creates the rotating JSON log and redacted in-memory stream.
func Open(logDir string, stderr io.Writer, options Options) (*System, error) {
	options = defaultOptions(options)
	writer, err := newRotatingWriter(filepath.Join(logDir, "application.jsonl"), options.MaxBytes, options.Backups)
	if err != nil {
		return nil, err
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &System{writer: writer, stderr: stderr, logDir: logDir, options: options, subs: make(map[uint64]chan Record)}, nil
}

// StderrOnly creates a safe bootstrap fallback when the log directory fails.
func StderrOnly(stderr io.Writer) *System {
	if stderr == nil {
		stderr = io.Discard
	}
	options := defaultOptions(Options{})
	return &System{stderr: stderr, options: options, subs: make(map[uint64]chan Record)}
}

func defaultOptions(options Options) Options {
	if options.MaxBytes <= 0 {
		options.MaxBytes = 10 << 20
	}
	if options.Backups <= 0 {
		options.Backups = 5
	}
	if options.RingSize <= 0 {
		options.RingSize = 500
	}
	if options.SubscriberBuffer <= 0 {
		options.SubscriberBuffer = 64
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return options
}

// Logger returns a source-scoped structured logger.
func (s *System) Logger(source string) *slog.Logger {
	return slog.New(&handler{system: s, source: sanitizeSource(source)})
}

// Recent returns oldest-first records capped to the requested count.
func (s *System) Recent(limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.ring) {
		limit = len(s.ring)
	}
	start := len(s.ring) - limit
	result := make([]Record, 0, limit)
	for _, record := range s.ring[start:] {
		record.Attributes = cloneAttributes(record.Attributes)
		result = append(result, record)
	}
	return result
}

// Subscribe starts a bounded nonblocking live stream.
func (s *System) Subscribe() Subscription {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closed := make(chan Record)
		close(closed)
		return Subscription{C: closed, Cancel: func() {}}
	}
	id := s.nextSub
	s.nextSub++
	channel := make(chan Record, s.options.SubscriberBuffer)
	s.subs[id] = channel
	s.mu.Unlock()
	var once sync.Once
	return Subscription{C: channel, Cancel: func() {
		once.Do(func() {
			s.mu.Lock()
			if current, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(current)
			}
			s.mu.Unlock()
		})
	}}
}

// Sources enumerates only application-owned regular log files.
func (s *System) Sources() []Source {
	if s.logDir == "" {
		return nil
	}
	names := []string{"application.jsonl"}
	for index := 1; index <= s.options.Backups; index++ {
		names = append(names, "application.jsonl."+strconv.Itoa(index))
	}
	names = append(names, "service-bootstrap.log")
	var result []Source
	for _, name := range names {
		path := filepath.Join(s.logDir, name)
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			result = append(result, Source{Name: name, Path: path, Downloadable: true})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return sourceOrder(result[i].Name) < sourceOrder(result[j].Name) })
	return result
}

// OpenSource opens one exact allowlisted source.
func (s *System) OpenSource(name string) (*os.File, error) {
	for _, source := range s.Sources() {
		if source.Name == name {
			return os.Open(source.Path) // #nosec G304 -- source is selected from an exact application-owned allowlist.
		}
	}
	return nil, fmt.Errorf("log source is unavailable")
}

// Close flushes sinks and closes subscribers. It is idempotent.
func (s *System) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for id, channel := range s.subs {
		delete(s.subs, id)
		close(channel)
	}
	if s.writer != nil {
		return s.writer.Close()
	}
	return nil
}

func (s *System) emit(record Record) error {
	record.Sequence = s.sequence.Add(1)
	if record.Time.IsZero() {
		record.Time = s.options.Now().UTC()
	} else {
		record.Time = record.Time.UTC()
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.writer != nil {
		if _, err := io.Writer(s.writer).Write(encoded); err != nil {
			return err
		}
	}
	if s.stderr != nil {
		_, _ = s.stderr.Write(encoded)
	}
	s.ring = append(s.ring, record)
	if len(s.ring) > s.options.RingSize {
		copy(s.ring, s.ring[len(s.ring)-s.options.RingSize:])
		s.ring = s.ring[:s.options.RingSize]
	}
	for _, channel := range s.subs {
		select {
		case channel <- record:
		default:
		}
	}
	return nil
}

type handler struct {
	system *System
	source string
	attrs  []slog.Attr
	groups []string
}

func (h *handler) Enabled(context.Context, slog.Level) bool { return true }

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	attributes := make(map[string]string)
	for _, attr := range h.attrs {
		collectAttr(attributes, strings.Join(h.groups, "."), attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		collectAttr(attributes, strings.Join(h.groups, "."), attr)
		return true
	})
	return h.system.emit(Record{Time: record.Time, Level: strings.ToLower(record.Level.String()), Source: h.source, Message: record.Message, Attributes: attributes})
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *handler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func collectAttr(output map[string]string, prefix string, attr slog.Attr) {
	value := attr.Value.Resolve()
	key := attr.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if value.Kind() == slog.KindGroup {
		for _, nested := range value.Group() {
			collectAttr(output, key, nested)
		}
		return
	}
	if secretKey(key) {
		output[key] = "[REDACTED]"
		return
	}
	output[key] = formatValue(value)
}

func formatValue(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindBool:
		return strconv.FormatBool(value.Bool())
	case slog.KindInt64:
		return strconv.FormatInt(value.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(value.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(value.Float64(), 'g', -1, 64)
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(value.Any())
	}
}

func secretKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(key))
	for _, marker := range []string{"api_key", "authorization", "cookie", "csrf", "token", "credential", "secret", "password"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func sanitizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "application"
	}
	if len(source) > 32 {
		return source[:32]
	}
	return source
}

func cloneAttributes(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sourceOrder(name string) string {
	if name == "application.jsonl" {
		return "0"
	}
	if strings.HasPrefix(name, "application.jsonl.") {
		return "1" + name
	}
	return "9" + name
}
