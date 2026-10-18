//go:build linux || darwin

package evidence

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func installedArchiveFixture(t *testing.T) (storage.InstalledPackExportSnapshot, []Attachment) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "kit"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"../testpack/testdata/kit", "kit"}, {"../testpack/testdata/installation/records", ""}} {
		entries, err := os.ReadDir(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(pair[0], entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, pair[1], entry.Name()), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	kit, err := testpack.Load(filepath.Join(root, "kit"), "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var completed map[string]json.RawMessage
	json.Unmarshal(raw, &completed)
	original, err := testpack.LoadInstallation(root, kit, "https://owned-target.test", completed["ownership_declaration"])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	run := domain.Run{ID: "crt_01234567000000000000000000", ShortToken: "01234567", TargetID: "api", Status: domain.RunInterrupted, CleanupStatus: domain.CleanupUnknown, HarnessVersion: "host-test", CreatedAt: now, UpdatedAt: now}
	b := original.Binding()
	k := kit.Bindings()
	run.CompiledPlanJSON, _ = json.Marshal(installedArchivePlan{run.ID, "flood", "installed-test-pack", k, b, "api", "receiver", "https://owned-receiver.test/webhookreceiver"})
	spec := oscar.InstalledPackInputSpec{APIVersion: "corrtest.oscar/installed-input-v1", ManifestSHA256: k.ManifestSHA256, QualificationRun: strings.Repeat("a", 32), StartsAt: now, ReceiverTargetID: "receiver", ReceiverBaseURL: "https://owned-receiver.test/webhookreceiver"}
	registration := storage.InstalledPackRegistration{APIVersion: "corrtest.oscar/installed-registration-v1", RunID: run.ID, APITargetID: "api", APIBaseURL: b.TargetOrigin, Spec: spec, Kit: k, Scenario: kit.Scenario(), Installation: b, KitFiles: kit.Files(), InstallationFiles: original.Files()}
	manifest, err := oscar.FreezeInstalledPackInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	e, _, err := manifest.Prepare(0)
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := json.Marshal(e)
	snapshot := storage.InstalledPackExportSnapshot{Run: run, Snapshot: &storage.InstalledPackSnapshot{Registration: registration, Attempts: []storage.InstalledPackAttempt{{Evidence: e, RecordedAt: now}}}, Events: []domain.RunEvent{{RunID: run.ID, Sequence: 1, Type: "alert.attempting", Level: "info", OccurredAt: now, Summary: "actual recorded request", DetailJSON: string(detail)}}}
	var attachments []Attachment
	for _, f := range original.Files() {
		raw, _ := original.Evidence(f.Path)
		name := "runs/" + run.ID + "/installed/original/" + f.Path
		snapshot.Artifacts = append(snapshot.Artifacts, domain.Artifact{ID: run.ID + ":installed/original/" + f.Path, RunID: run.ID, Kind: "installed-pack", RelativePath: name, MIMEType: "application/octet-stream", SHA256: f.SHA256, ByteSize: f.Size, RedactionState: "private-original-response", Availability: domain.ArtifactAvailable, CreatedAt: now})
		attachments = append(attachments, Attachment{Name: name, Content: raw, SHA256: f.SHA256, ByteSize: f.Size})
	}
	if err = os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	return snapshot, attachments
}

func TestInstalledArchiveInterruptedUnknownAndOriginals(t *testing.T) {
	s, a := installedArchiveFixture(t)
	p := filepath.Join(t.TempDir(), "installed.zip")
	result, err := WriteInstalledPack(context.Background(), p, s, a)
	if err != nil {
		t.Fatal(err)
	}
	if result.ByteSize == 0 || result.SHA256 == "" {
		t.Fatal("missing result")
	}
	if err = Verify(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == "installed-run.json" {
			raw, err := readZipEntry(context.Background(), f)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(`"canonicalReport"`)) || bytes.Contains(raw, []byte(`"PASS"`)) {
				t.Fatal("invented report")
			}
		}
	}
	if _, err = WriteInstalledPack(context.Background(), p, s, a); err == nil {
		t.Fatal("overwrote bundle")
	}
}
func TestInstalledArchiveRejectsBeforePublication(t *testing.T) {
	for _, name := range []string{"missing-original", "changed-original", "foreign-event", "changed-input", "clean-unknown", "foreign-plan", "foreign-api-target", "extra-attachment", "pending-original", "traversal", "duplicate-ledger", "complete-without-report"} {
		t.Run(name, func(t *testing.T) {
			s, a := installedArchiveFixture(t)
			switch name {
			case "missing-original":
				s.Artifacts = s.Artifacts[1:]
				a = a[1:]
			case "changed-original":
				a[0].Content = []byte(`{}`)
				a[0].SHA256 = installedHashBytes(a[0].Content)
				a[0].ByteSize = int64(len(a[0].Content))
				s.Artifacts[0].SHA256 = a[0].SHA256
				s.Artifacts[0].ByteSize = a[0].ByteSize
			case "foreign-event":
				s.Events[0].RunID = "foreign"
			case "changed-input":
				s.Snapshot.Attempts[0].Evidence.Request.BodyBase64 = "e30="
			case "clean-unknown":
				s.Run.CleanupStatus = domain.CleanupClean
			case "foreign-api-target":
				var plan installedArchivePlan
				json.Unmarshal(s.Run.CompiledPlanJSON, &plan)
				plan.APITargetID = "foreign"
				s.Run.CompiledPlanJSON, _ = json.Marshal(plan)
			case "foreign-plan":
				s.Run.CompiledPlanJSON = json.RawMessage(`{"inputPath":"installed-test-pack"}`)
			case "extra-attachment":
				a = append(a, Attachment{Name: "unexpected", Content: []byte("x"), SHA256: installedHashBytes([]byte("x")), ByteSize: 1})
			case "pending-original":
				s.Artifacts[0].Availability = domain.ArtifactPending
				s.Artifacts[0].SHA256 = ""
				s.Artifacts[0].ByteSize = 0
				a = a[1:]
			case "traversal":
				s.Artifacts[0].RelativePath = "../escape"
				a[0].Name = "../escape"
			case "duplicate-ledger":
				s.Artifacts = append(s.Artifacts, s.Artifacts[0])
			case "complete-without-report":
				s.Run.Status = domain.RunCompleted
				s.Run.Verdict = domain.VerdictPass
			}
			p := filepath.Join(t.TempDir(), "bad.zip")
			if _, err := WriteInstalledPack(context.Background(), p, s, a); err == nil {
				t.Fatal("accepted contradiction")
			}
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Fatal("published invalid evidence", err)
			}
		})
	}
}
func TestInstalledArchivePreregistrationPreservesPending(t *testing.T) {
	s, _ := installedArchiveFixture(t)
	s.Snapshot = nil
	s.Events = nil
	s.Artifacts = s.Artifacts[:1]
	s.Artifacts[0].Availability = domain.ArtifactPending
	s.Artifacts[0].SHA256 = ""
	s.Artifacts[0].ByteSize = 0
	p := filepath.Join(t.TempDir(), "partial.zip")
	if _, err := WriteInstalledPack(context.Background(), p, s, nil); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	s.Events = []domain.RunEvent{{RunID: s.Run.ID, Sequence: 1, Type: "alert.attempting", OccurredAt: s.Run.CreatedAt}}
	if _, err := WriteInstalledPack(context.Background(), filepath.Join(t.TempDir(), "bad.zip"), s, nil); err == nil {
		t.Fatal("accepted input without registration")
	}
}

func installedRewrite(t *testing.T, src string, mutate func(map[string][]byte)) string {
	t.Helper()
	z, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		raw, err := readZipEntry(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = raw
	}
	z.Close()
	mutate(files)
	var m manifest
	if err = json.Unmarshal(files["manifest.json"], &m); err != nil {
		t.Fatal(err)
	}
	m.Files = nil
	for n, raw := range files {
		if n != "manifest.json" {
			m.Files = append(m.Files, manifestEntry{n, installedHashBytes(raw), int64(len(raw))})
		}
	}
	files["manifest.json"], _ = json.Marshal(m)
	p := filepath.Join(t.TempDir(), "rehashed.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for n, raw := range files {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetMode(0600)
		e, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestInstalledArchiveRehashedContradictionsRefuse(t *testing.T) {
	s, a := installedArchiveFixture(t)
	p := filepath.Join(t.TempDir(), "good.zip")
	if _, err := WriteInstalledPack(context.Background(), p, s, a); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"registration", "request", "event", "original", "missing-original", "extra-entry", "duplicate-json", "ledger", "outcome", "ordinary-discriminator"} {
		t.Run(kind, func(t *testing.T) {
			bad := installedRewrite(t, p, func(files map[string][]byte) {
				switch kind {
				case "registration", "request", "outcome":
					var state installedArchiveState
					json.Unmarshal(files["installed-run.json"], &state)
					if kind == "registration" {
						state.Registration.Installation.RuleID++
					} else if kind == "request" {
						state.Attempts[0].Evidence.Request.BodyBase64 = "e30="
					} else {
						state.Attempts[0].Outcome = "outcome-00.json"
						files["outcome-00.json"] = []byte(`{}`)
					}
					files["installed-run.json"], _ = json.Marshal(state)
				case "event":
					var events []domain.RunEvent
					json.Unmarshal(files["events.json"], &events)
					events[0].DetailJSON = "{}"
					files["events.json"], _ = json.Marshal(events)
				case "original", "missing-original":
					var index []installedArchiveIndex
					json.Unmarshal(files["installed-index.json"], &index)
					if kind == "missing-original" {
						delete(files, index[0].Entry)
						index = index[1:]
					} else {
						files[index[0].Entry] = []byte(`{}`)
						index[0].Artifact.SHA256 = installedHashBytes(files[index[0].Entry])
						index[0].Artifact.ByteSize = 2
					}
					files["installed-index.json"], _ = json.Marshal(index)
				case "extra-entry":
					files["original-331.bin"] = []byte("extra")
				case "duplicate-json":
					files["installed-run.json"] = append([]byte(`{"run":null,`), files["installed-run.json"][1:]...)
				case "ledger":
					var index []installedArchiveIndex
					json.Unmarshal(files["installed-index.json"], &index)
					index[1].Entry = index[0].Entry
					files["installed-index.json"], _ = json.Marshal(index)
				case "ordinary-discriminator":
					var m manifest
					json.Unmarshal(files["manifest.json"], &m)
					m.APIVersion = bundleAPIVersion
					files["manifest.json"], _ = json.Marshal(m)
				}
			})
			if err := Verify(context.Background(), bad); err == nil {
				t.Fatal("rehashed contradiction accepted")
			}
		})
	}
}
func TestInstalledArchivePendingLargeLedgerAndCancellation(t *testing.T) {
	s, _ := installedArchiveFixture(t)
	s.Snapshot = nil
	s.Events = nil
	s.Artifacts = nil
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("installed/observations/%03d/active-raw-rows.json", i)
		s.Artifacts = append(s.Artifacts, domain.Artifact{ID: s.Run.ID + ":" + name, RunID: s.Run.ID, Kind: "installed-pack", RelativePath: "runs/" + s.Run.ID + "/" + name, MIMEType: "application/octet-stream", RedactionState: "private-original-response", Availability: domain.ArtifactPending, CreatedAt: s.Run.CreatedAt})
	}
	var attachments []Attachment
	for i := 1; i < len(s.Artifacts); i++ {
		raw := []byte("[]")
		s.Artifacts[i].Availability = domain.ArtifactAvailable
		s.Artifacts[i].SHA256 = installedHashBytes(raw)
		s.Artifacts[i].ByteSize = 2
		attachments = append(attachments, Attachment{s.Artifacts[i].RelativePath, raw, s.Artifacts[i].SHA256, 2})
	}
	p := filepath.Join(t.TempDir(), "ledger.zip")
	if _, err := WriteInstalledPack(context.Background(), p, s, attachments); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WriteInstalledPack(ctx, filepath.Join(t.TempDir(), "cancel.zip"), s, nil); err == nil {
		t.Fatal("cancelled publication")
	}
	if err := Verify(ctx, p); err == nil {
		t.Fatal("cancelled verification")
	}
}

func TestInstalledClaimCannotUseOrdinaryWriterOrVerifier(t *testing.T) {
	s, _ := installedArchiveFixture(t)
	run := s.Run
	run.Status = domain.RunCompleted
	run.Verdict = domain.VerdictPass
	run.CompiledPlanJSON = json.RawMessage(`{"pattern":"flood"}`)
	run.CanonicalReportJSON = json.RawMessage(`{"verdict":"PASS"}`)
	ordinary := filepath.Join(t.TempDir(), "ordinary.zip")
	if _, err := Write(context.Background(), ordinary, run, nil); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), ordinary); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"plan", "report", "coverage", "version"} {
		t.Run(kind, func(t *testing.T) {
			plan := json.RawMessage(`{"pattern":"flood"}`)
			report := json.RawMessage(`{"verdict":"PASS"}`)
			switch kind {
			case "plan":
				plan = json.RawMessage(`{"inputPath":"installed-test-pack"}`)
			case "report":
				report = json.RawMessage(`{"inputPath":"installed-test-pack","verdict":"PASS"}`)
			case "coverage":
				report = json.RawMessage(`{"coverage":{"inputPath":"installed-test-pack"},"verdict":"PASS"}`)
			case "version":
				report = json.RawMessage(`{"apiVersion":"corrtest.oscar/installed-run-v1","verdict":"PASS"}`)
			}
			changed := run
			changed.CompiledPlanJSON = plan
			changed.CanonicalReportJSON = report
			if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "bad.zip"), changed, nil); err == nil {
				t.Fatal("ordinary writer accepted installed claim")
			}
			bad := installedRewrite(t, ordinary, func(files map[string][]byte) { files["plan.json"] = plan; files["report.json"] = report })
			if err := Verify(context.Background(), bad); err == nil {
				t.Fatal("ordinary verifier accepted installed claim")
			}
		})
	}
}

func TestInstalledArchiveDeclaredBudgetsBeforeDecompression(t *testing.T) {
	for _, kind := range []string{"original-entry", "observation-entry", "outcome-entry", "metadata-total", "original-total", "observation-total", "original-count", "observation-count", "outcome-count", "total-count", "link", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			files := []*zip.File{}
			add := func(name string, size uint64, mode os.FileMode) {
				h := zip.FileHeader{Name: name, UncompressedSize64: size}
				h.SetMode(mode)
				files = append(files, &zip.File{FileHeader: h})
			}
			for _, n := range []string{"manifest.json", "installed-run.json", "installed-index.json", "events.json"} {
				add(n, 1, 0600)
			}
			switch kind {
			case "original-entry":
				add("original-000.bin", (1<<20)+1, 0600)
			case "observation-entry":
				add("observation-0000.bin", (16<<20)+1, 0600)
			case "outcome-entry":
				add("outcome-00.json", (2<<20)+1, 0600)
			case "metadata-total":
				files[0].UncompressedSize64 = 8 << 20
				files[1].UncompressedSize64 = 8 << 20
			case "original-total":
				for i := 0; i < 43; i++ {
					add(fmt.Sprintf("original-%03d.bin", i), 1<<20, 0600)
				}
			case "observation-total":
				for i := 0; i < 5; i++ {
					add(fmt.Sprintf("observation-%04d.bin", i), 16<<20, 0600)
				}
			case "original-count":
				for i := 0; i < 333; i++ {
					add(fmt.Sprintf("original-%03d.bin", i), 1, 0600)
				}
			case "observation-count":
				for i := 0; i < 1025; i++ {
					add(fmt.Sprintf("observation-%04d.bin", i), 1, 0600)
				}
			case "outcome-count":
				for i := 0; i < 16; i++ {
					add(fmt.Sprintf("outcome-%02d.json", i), 1, 0600)
				}
			case "total-count":
				for i := 0; i < 1372; i++ {
					add(fmt.Sprintf("observation-%04d.bin", i), 1, 0600)
				}
			case "link":
				add("original-000.bin", 1, os.ModeSymlink|0600)
			case "duplicate":
				add("manifest.json", 1, 0600)
			}
			// Synthetic headers have no reader. The check must refuse before attempting
			// decompression; passing this preflight would panic on the absent stream.
			if err := verifyInstalledPack(context.Background(), &zip.ReadCloser{Reader: zip.Reader{File: files}}); err == nil {
				t.Fatal("unbounded declared archive")
			}
		})
	}
}

func TestInstalledArchivePrivateStagingRemoved(t *testing.T) {
	staging := t.TempDir()
	t.Setenv("TMPDIR", staging)
	s, a := installedArchiveFixture(t)
	output := filepath.Join(t.TempDir(), "ok.zip")
	before, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = WriteInstalledPack(context.Background(), output, s, a); err != nil {
		t.Fatal(err)
	}
	if err = Verify(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("private original staging leaked")
	}
}
