// Package evidence creates portable, self-verifying run evidence bundles.
package evidence

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const bundleAPIVersion = "corrtest.oscar/bundle-v1"

// Result identifies the immutable bundle published by Write.
type Result struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	ByteSize int64  `json:"byteSize"`
}

type manifest struct {
	APIVersion string          `json:"apiVersion"`
	RunID      string          `json:"runId"`
	CreatedAt  time.Time       `json:"createdAt"`
	Files      []manifestEntry `json:"files"`
}

type manifestEntry struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	ByteSize int64  `json:"byteSize"`
}

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name    string        `xml:"name,attr"`
	Failure *junitFailure `xml:"failure,omitempty"`
	Error   *junitFailure `xml:"error,omitempty"`
	Skipped *struct{}     `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>OSCAR correlation run {{.ID}}</title><style>
body{font:16px system-ui,sans-serif;line-height:1.5;max-width:70rem;margin:2rem auto;padding:0 1rem;color:#172033;background:#fff}
dl{display:grid;grid-template-columns:max-content 1fr;gap:.5rem 1rem}dt{font-weight:700}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f4f6fa;padding:1rem;border-radius:.5rem}@media(prefers-color-scheme:dark){body{color:#edf2ff;background:#111827}pre{background:#1f2937}}
</style></head><body><h1>OSCAR correlation test evidence</h1><dl><dt>Run</dt><dd>{{.ID}}</dd><dt>Status</dt><dd>{{.Status}}</dd><dt>Verdict</dt><dd>{{.Verdict}}</dd><dt>Cleanup</dt><dd>{{.CleanupStatus}}</dd><dt>Harness</dt><dd>{{.HarnessVersion}}</dd></dl><h2>Canonical report</h2><pre>{{.Report}}</pre></body></html>`))

// Write atomically creates a portable ZIP. Existing destinations are never overwritten.
func Write(ctx context.Context, destination string, run domain.Run, events []domain.RunEvent) (Result, error) {
	if run.ID == "" || run.Status != domain.RunCompleted || !run.Verdict.Valid() {
		return Result{}, fmt.Errorf("run is not eligible for export")
	}
	if !json.Valid(run.CanonicalReportJSON) || !json.Valid(run.CompiledPlanJSON) {
		return Result{}, fmt.Errorf("run evidence JSON is missing or invalid")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return Result{}, fmt.Errorf("resolve bundle path: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return Result{}, fmt.Errorf("bundle destination already exists")
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect bundle destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Result{}, fmt.Errorf("create bundle directory: %w", err)
	}

	eventsJSON, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return Result{}, err
	}
	reportJSON, err := prettyJSON(run.CanonicalReportJSON)
	if err != nil {
		return Result{}, err
	}
	planJSON, err := prettyJSON(run.CompiledPlanJSON)
	if err != nil {
		return Result{}, err
	}
	var html bytes.Buffer
	if err := reportTemplate.Execute(&html, struct {
		domain.Run
		Report string
	}{Run: run, Report: string(reportJSON)}); err != nil {
		return Result{}, err
	}
	junit, err := renderJUnit(run)
	if err != nil {
		return Result{}, err
	}
	files := map[string][]byte{
		"events.json": append(eventsJSON, '\n'), "junit.xml": junit,
		"plan.json": planJSON, "report.html": html.Bytes(), "report.json": reportJSON,
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	createdAt := run.UpdatedAt.UTC()
	if createdAt.Before(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
		createdAt = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	m := manifest{APIVersion: bundleAPIVersion, RunID: run.ID, CreatedAt: createdAt}
	for _, name := range names {
		digest := sha256.Sum256(files[name])
		m.Files = append(m.Files, manifestEntry{Path: name, SHA256: hex.EncodeToString(digest[:]), ByteSize: int64(len(files[name]))})
	}
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Result{}, err
	}
	files["manifest.json"] = append(manifestJSON, '\n')
	names = append(names, "manifest.json")

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".corrtest-bundle-*")
	if err != nil {
		return Result{}, fmt.Errorf("create bundle staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return Result{}, err
	}
	hash := sha256.New()
	counting := &countWriter{writer: io.MultiWriter(temporary, hash)}
	archive := zip.NewWriter(counting)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			_ = archive.Close()
			_ = temporary.Close()
			return Result{}, err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(createdAt)
		header.SetMode(0o600)
		entry, createErr := archive.CreateHeader(header)
		if createErr != nil {
			return Result{}, createErr
		}
		if _, err := entry.Write(files[name]); err != nil {
			return Result{}, err
		}
	}
	if err := archive.Close(); err != nil {
		return Result{}, err
	}
	if err := temporary.Sync(); err != nil {
		return Result{}, err
	}
	if err := temporary.Close(); err != nil {
		return Result{}, err
	}
	if err := os.Link(temporaryPath, destination); err != nil {
		return Result{}, fmt.Errorf("publish evidence bundle: %w", err)
	}
	return Result{Path: destination, SHA256: hex.EncodeToString(hash.Sum(nil)), ByteSize: counting.count}, nil
}

// Verify validates the manifest and every declared payload, rejecting extra files.
func Verify(ctx context.Context, bundlePath string) error {
	reader, err := zip.OpenReader(bundlePath)
	if err != nil {
		return fmt.Errorf("open evidence bundle: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > 64 {
		return fmt.Errorf("evidence bundle contains too many files")
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		if filepath.Base(file.Name) != file.Name || file.UncompressedSize64 > 16<<20 {
			return fmt.Errorf("unsafe evidence entry %q", file.Name)
		}
		if _, exists := files[file.Name]; exists {
			return fmt.Errorf("duplicate evidence entry %q", file.Name)
		}
		files[file.Name] = file
	}
	manifestFile := files["manifest.json"]
	if manifestFile == nil {
		return fmt.Errorf("evidence manifest is missing")
	}
	data, err := readZipEntry(ctx, manifestFile)
	if err != nil {
		return err
	}
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil || m.APIVersion != bundleAPIVersion || m.RunID == "" {
		return fmt.Errorf("evidence manifest is invalid")
	}
	if len(files) != len(m.Files)+1 {
		return fmt.Errorf("evidence bundle contains undeclared files")
	}
	for _, item := range m.Files {
		file := files[item.Path]
		if file == nil || item.Path == "manifest.json" {
			return fmt.Errorf("manifest entry %q is invalid", item.Path)
		}
		payload, err := readZipEntry(ctx, file)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		if int64(len(payload)) != item.ByteSize || hex.EncodeToString(digest[:]) != item.SHA256 {
			return fmt.Errorf("evidence entry %q failed integrity validation", item.Path)
		}
	}
	return nil
}

func prettyJSON(raw json.RawMessage) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, raw, "", "  "); err != nil {
		return nil, err
	}
	buffer.WriteByte('\n')
	return buffer.Bytes(), nil
}

func renderJUnit(run domain.Run) ([]byte, error) {
	test := junitCase{Name: run.ID}
	suite := junitSuite{Name: "oscar-corrtest", Tests: 1, Cases: []junitCase{test}}
	switch run.Verdict {
	case domain.VerdictFail:
		suite.Failures, suite.Cases[0].Failure = 1, &junitFailure{Message: "correlation assertions failed", Text: run.TerminalError}
	case domain.VerdictError, domain.VerdictInconclusive:
		suite.Errors, suite.Cases[0].Error = 1, &junitFailure{Message: string(run.Verdict), Text: run.TerminalError}
	case domain.VerdictSkipped:
		suite.Skipped, suite.Cases[0].Skipped = 1, &struct{}{}
	}
	data, err := xml.MarshalIndent(suite, "", "  ")
	return append([]byte(xml.Header), append(data, '\n')...), err
}

func readZipEntry(ctx context.Context, file *zip.File) ([]byte, error) {
	stream, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: stream}, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("evidence entry %q exceeds size limit", file.Name)
	}
	return data, nil
}

type countWriter struct {
	writer io.Writer
	count  int64
}

func (writer *countWriter) Write(data []byte) (int, error) {
	n, err := writer.writer.Write(data)
	writer.count += int64(n)
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(data []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(data)
	}
}
