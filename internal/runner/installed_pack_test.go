//go:build linux || darwin

package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func installedRunnerHash(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func installedRunnerCanonical(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	b, e = testpack.CanonicalJSON(b)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

type installedRunnerPeer struct {
	t             *testing.T
	mu            sync.Mutex
	now           time.Time
	monotonic     time.Time
	start         time.Time
	server        *httptest.Server
	db            *storage.Database
	dbPath        string
	artifacts     *artifact.Store
	dir           string
	kit           *testpack.Kit
	installation  *testpack.Installation
	api, receiver domain.Target
	run           domain.Run
	posts         int
	gets          int
	sleeps        int
	fault         string
	records       []map[string]any
	reads         map[string][]byte
	observation   int
	cancel        context.CancelFunc
}

func (p *installedRunnerPeer) clock() time.Time { p.mu.Lock(); defer p.mu.Unlock(); return p.now }
func (p *installedRunnerPeer) monotonicClock() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.monotonic
}
func (p *installedRunnerPeer) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sleeps++
	p.now = p.now.Add(d)
	p.monotonic = p.monotonic.Add(d)
	if p.fault == "wall jump" && p.sleeps == 1 {
		p.now = p.now.Add(91 * time.Second)
	}
	return nil
}
func (p *installedRunnerPeer) rawSQL(query string, args ...any) {
	p.t.Helper()
	db, e := sql.Open("sqlite", p.dbPath)
	if e != nil {
		p.t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(query, args...); e != nil {
		p.t.Fatal(e)
	}
}
func newInstalledRunnerPeer(t *testing.T, fault string) *installedRunnerPeer {
	t.Helper()
	p := &installedRunnerPeer{t: t, now: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), fault: fault, reads: map[string][]byte{}}
	p.start = p.now
	p.monotonic = p.now
	apiSuffix := "/ext/mw"
	if fault == "same target" {
		apiSuffix = ""
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	kitdir := filepath.Join(original, "kit")
	if e := os.MkdirAll(kitdir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, sub := range []string{original, kitdir} {
		if e := os.Chmod(sub, 0700); e != nil {
			t.Fatal(e)
		}
	}
	entries, e := os.ReadDir("../testpack/testdata/kit")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, e := os.ReadFile(filepath.Join("../testpack/testdata/kit", entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(kitdir, entry.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	p.kit, e = testpack.Load(kitdir, "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59")
	if e != nil {
		t.Fatal(e)
	}
	entries, e = os.ReadDir("../testpack/testdata/installation/records")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		b, e := os.ReadFile(filepath.Join("../testpack/testdata/installation/records", entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		b = bytes.ReplaceAll(b, []byte("https://owned-target.test"), []byte(p.server.URL+apiSuffix))
		if e = os.WriteFile(filepath.Join(original, entry.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	b, e := os.ReadFile(filepath.Join(original, "completed.json"))
	if e != nil {
		t.Fatal(e)
	}
	var complete map[string]json.RawMessage
	if e = json.Unmarshal(b, &complete); e != nil {
		t.Fatal(e)
	}
	var journal []map[string]any
	for index := 1; index <= 20; index++ {
		for _, phase := range []string{"before", "after"} {
			name := fmt.Sprintf("%04d-%s.json", index, phase)
			raw, e := os.ReadFile(filepath.Join(original, name))
			if e != nil {
				t.Fatal(e)
			}
			journal = append(journal, map[string]any{"path": name, "bytes": len(raw), "sha256": installedRunnerHash(raw)})
		}
	}
	complete["journal_sha256"] = json.RawMessage(fmt.Sprintf("%q", installedRunnerHash(installedRunnerCanonical(t, journal))))
	if e = os.WriteFile(filepath.Join(original, "completed.json"), installedRunnerCanonical(t, complete), 0600); e != nil {
		t.Fatal(e)
	}
	p.installation, e = testpack.LoadInstallation(original, p.kit, p.server.URL+apiSuffix, complete["ownership_declaration"])
	if e != nil {
		t.Fatal(e)
	}
	binding := p.installation.Binding()
	paths := map[string]string{"/api/v1/packs": "0019-after.json", "/api/v1/packs/corrtest_operational/configuration/loads/" + binding.LoadID: "0011-after.json", "/api/v1/packs/corrtest_operational/configuration/items/" + p.kit.Bindings().MappingRowKey: "0020-after.json", "/api/v1/correlation_rules": "0016-after.json", "/api/v1/correlation_rules/7": "0017-after.json", "/api/v1/correlation_rules/runtime": "0018-after.json"}
	for path, name := range paths {
		b, ok := p.installation.Evidence(name)
		if !ok {
			t.Fatal(name)
		}
		var v map[string]json.RawMessage
		if e = json.Unmarshal(b, &v); e != nil {
			t.Fatal(e)
		}
		p.reads[path] = v["body"]
	}
	p.dir = t.TempDir()
	p.dbPath = filepath.Join(p.dir, "corrtest.db")
	p.db, e = storage.Open(context.Background(), p.dbPath)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = p.db.Close() })
	p.artifacts, e = artifact.New(p.dir)
	if e != nil {
		t.Fatal(e)
	}
	p.api = domain.Target{ID: "api", DisplayName: "API", BaseURL: p.server.URL + apiSuffix, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "API_KEY"}, CreatedAt: p.now, UpdatedAt: p.now}
	p.receiver = p.api
	p.receiver.ID = "receiver"
	p.receiver.DisplayName = "Receiver"
	p.receiver.BaseURL = p.server.URL + "/webhookreceiver"
	targets := []domain.Target{p.api, p.receiver}
	if fault == "same target" {
		p.receiver = p.api
		targets = targets[:1]
	}
	for _, target := range targets {
		if e = p.db.CreateTarget(context.Background(), target); e != nil {
			t.Fatal(e)
		}
	}
	p.run = domain.Run{ID: "crt_01234567000000000000000000", ShortToken: "01234567", TargetID: p.api.ID, Status: domain.RunQueued, CleanupStatus: domain.CleanupNotRequired, HarnessVersion: "test", CreatedAt: p.now, UpdatedAt: p.now}
	if e = p.db.CreateRun(context.Background(), p.run); e != nil {
		t.Fatal(e)
	}
	return p
}
func (p *installedRunnerPeer) engine() *InstalledPackRunner {
	p.t.Helper()
	r, e := NewInstalledPackRunner(p.kit, p.installation, p.api, p.receiver, p.db, p.artifacts, InstalledPackRunnerOptions{Now: p.clock, MonotonicNow: p.monotonicClock, Sleep: p.sleep, Random: bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)), Getenv: func(string) string { return "test-key" }, HarnessVersion: "test"})
	if e != nil {
		p.t.Fatal(e)
	}
	return r
}
func (p *installedRunnerPeer) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("X-API-Key") != "test-key" || r.Header.Get("X-Internal-Service") != "" {
		p.t.Error("auth differs")
	}
	if r.Method == "POST" {
		if r.URL.Path != "/webhookreceiver/webhook/corrtest_operational_v1" {
			p.t.Errorf("unexpected mutation %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		snapshot, e := p.db.ReadInstalledPackSnapshot(context.Background(), p.run.ID)
		if e != nil || len(snapshot.Attempts) != p.posts+1 || snapshot.Attempts[p.posts].Result != nil {
			p.t.Errorf("POST before durable UNKNOWN: %v", e)
		}
		saved, e := p.db.GetRun(context.Background(), p.run.ID)
		if e != nil || saved.CleanupStatus != domain.CleanupUnknown {
			p.t.Error("POST lost source responsibility")
		}
		body, e := io.ReadAll(r.Body)
		if e != nil {
			p.t.Error(e)
		}
		manifest, e := oscar.FreezeInstalledPackInput(snapshot.Registration.Spec)
		if e != nil {
			p.t.Fatal(e)
		}
		_, want, e := manifest.Prepare(p.posts)
		if e != nil || !bytes.Equal(body, want) {
			p.t.Error("wrong deterministic body")
		}
		var row map[string]any
		if e = json.Unmarshal(body, &row); e != nil {
			p.t.Error(e)
		}
		p.records = append(p.records, row)
		p.posts++
		if p.fault == "cancel send" {
			p.cancel()
			<-r.Context().Done()
			return
		}
		if p.fault == "outcome failure" {
			p.rawSQL(`CREATE TRIGGER failed_outcome BEFORE UPDATE ON installed_pack_attempts BEGIN SELECT RAISE(ABORT,'held outcome failure'); END`)
		}
		if p.fault == "wrong revision" {
			w.Header().Set("X-Oscar-Profile-Revision", strings.Repeat("0", 64))
		} else {
			w.Header().Set("X-Oscar-Profile-Revision", p.kit.Bindings().ProfileEffectiveSHA256)
		}
		if (p.fault == "delayed final ack" || p.fault == "insufficient interval") && p.posts == 15 {
			p.now = p.now.Add(55 * time.Second)
			p.monotonic = p.monotonic.Add(55 * time.Second)
		}
		if p.fault == "late ack" {
			p.now = p.now.Add(61 * time.Second)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	if r.Method != "GET" {
		p.t.Errorf("unexpected method %s", r.Method)
		w.WriteHeader(500)
		return
	}
	p.gets++
	artifacts, e := p.db.ListArtifacts(context.Background(), p.run.ID)
	if e != nil || len(artifacts) < len(p.installation.Files()) {
		p.t.Error("HTTP before original artifacts")
	}
	path := strings.TrimPrefix(r.URL.Path, "/ext/mw")
	if raw, ok := p.reads[path]; ok {
		if p.fault == "insufficient interval" && p.posts == 0 && path == "/api/v1/correlation_rules/runtime" {
			p.now = p.now.Add(50 * time.Second)
			p.monotonic = p.monotonic.Add(50 * time.Second)
		}
		if p.fault == "preflight" && path == "/api/v1/correlation_rules/runtime" {
			w.WriteHeader(503)
			return
		}
		if p.fault == "postflight drift" && p.posts == 15 && path == "/api/v1/correlation_rules/runtime" {
			raw = bytes.Replace(raw, []byte(`"authoritative":false`), []byte(`"authoritative":true`), 1)
		}
		_, _ = w.Write(raw)
		return
	}
	switch path {
	case "/api/v1/alerts/active-alerts":
		if p.posts > 0 {
			p.observation++
			if p.posts != 15 {
				p.t.Error("observation interrupted fixed send sequence")
			}
		}
		rows := []map[string]any{}
		for index, record := range p.records {
			labels := record["labels"].(map[string]any)
			pairs := []map[string]any{}
			keys := []string{}
			for k := range labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				pairs = append(pairs, map[string]any{"name": k, "value": labels[k]})
			}
			rows = append(rows, map[string]any{"fingerprint": fmt.Sprintf("%016x", index+1), "starts_at": record["startsAt"], "alert_labels": pairs, "alertname": "CorrTestOperationalFlood"})
		}
		if p.fault == "missing observations" || (p.fault == "history collision" && p.posts == 0) {
			rows = []map[string]any{}
		}
		if p.fault == "preexisting" && p.posts == 0 {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"records": rows, "total_records": len(rows), "total_pages": (len(rows) + 99) / 100})
	case "/api/v1/alerts/history":
		if p.fault == "history collision" && p.posts == 0 {
			start, e := time.Parse(time.RFC3339, r.URL.Query().Get("start_datetime"))
			if e != nil || start.After(p.now.Add(-24*time.Hour)) {
				w.Write([]byte(`{"records":[],"total_records":0,"total_pages":0,"page":1,"per_page":100}`))
				return
			}
		}
		rows := []map[string]any{}
		for index, record := range p.records {
			labels := record["labels"].(map[string]any)
			pairs := []map[string]any{}
			for k, v := range labels {
				pairs = append(pairs, map[string]any{"Label": k, "Value": v})
			}
			fingerprint := fmt.Sprintf("oscar-%d", index)
			pairs = append(pairs, map[string]any{"Label": "am_fingerprint", "Value": fmt.Sprintf("%016x", index+1)}, map[string]any{"Label": "oscar_fingerprint", "Value": fingerprint})
			rows = append(rows, map[string]any{"ID": fmt.Sprintf("00000000-0000-4000-8000-%012x", index+1), "alertname": "CorrTestOperationalFlood", "fingerprint": fingerprint, "status": "firing", "startsAt": record["startsAt"], "createdAt": record["startsAt"], "severity": "warning", "labels": pairs, "annotations": []any{}})
		}
		if p.fault == "missing observations" {
			rows = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"records": rows, "total_records": len(rows), "total_pages": (len(rows) + 99) / 100, "page": 1, "per_page": 100})
	case "/api/v1/correlation_rules/audit":
		rows := []map[string]any{}
		fp := r.URL.Query().Get("fingerprint")
		if p.fault == "partial audit" && fp == fmt.Sprintf("%016x", 15) {
			w.WriteHeader(503)
			return
		}
		for index, record := range p.records {
			if fp != fmt.Sprintf("%016x", index+1) {
				continue
			}
			labels := record["labels"].(map[string]any)
			outcome := "enriched"
			if index == 4 {
				outcome = "parent_emitted"
			}
			if p.fault == "transient violation" && p.observation == 1 && index == 5 {
				outcome = "parent_emitted"
			}
			rows = append(rows, map[string]any{"id": index + 1, "created_at": record["startsAt"], "alert_fingerprint": fp, "rule_id": 7, "rule_name": p.kit.Bindings().RuleName, "pattern": "flood", "outcome": outcome, "window_id": "opaque-" + labels["site"].(string), "reason": nil})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows, "total": len(rows), "page": 1, "perPage": 100})
	default:
		p.t.Errorf("unexpected GET %s", r.URL)
		w.WriteHeader(404)
	}
}

func TestInstalledRunnerPublishesOriginalsBeforeReadAndSendsFixed15(t *testing.T) {
	p := newInstalledRunnerPeer(t, "")
	r := p.engine()
	if e := r.Execute(context.Background(), p.run); e != nil {
		t.Fatal(e)
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Verdict != domain.VerdictPass || saved.CleanupStatus != domain.CleanupDirty || p.posts != 15 || p.sleeps < 90 {
		t.Fatalf("run=%+v posts=%d sleeps=%d report=%s", saved, p.posts, p.sleeps, saved.CanonicalReportJSON)
	}
	snapshot, e := p.db.ReadInstalledPackSnapshot(context.Background(), p.run.ID)
	if e != nil || len(snapshot.Attempts) != 15 || snapshot.Registration.Spec.QualificationRun == p.run.ID {
		t.Fatal("lost fixed inputs", e)
	}
	for _, f := range p.installation.Files() {
		b, e := os.ReadFile(filepath.Join(p.dir, "runs", p.run.ID, "installed/original", f.Path))
		if e != nil || installedRunnerHash(b) != f.SHA256 {
			t.Fatal("lost original file", f.Path, e)
		}
	}
	before := p.posts
	if e = p.engine().Execute(context.Background(), p.run); e == nil || p.posts != before {
		t.Fatal("new runner adopted completed run")
	}
	if e = r.Execute(context.Background(), p.run); e == nil || p.posts != before {
		t.Fatal("runner reused")
	}
}

func TestInstalledRunnerFailuresNeverRetryOrEraseResponsibility(t *testing.T) {
	for _, fault := range []string{"preflight", "preexisting", "wrong revision", "late ack", "outcome failure", "cancel send", "missing observations", "transient violation", "postflight drift", "partial audit"} {
		t.Run(fault, func(t *testing.T) {
			p := newInstalledRunnerPeer(t, fault)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p.cancel = cancel
			e := p.engine().Execute(ctx, p.run)
			saved, readErr := p.db.GetRun(context.Background(), p.run.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if saved.Verdict == domain.VerdictPass {
				t.Fatal("failure became PASS", saved.CanonicalReportJSON)
			}
			switch fault {
			case "preflight", "preexisting":
				if p.posts != 0 || saved.CleanupStatus != domain.CleanupNotRequired {
					t.Fatal("preflight sent input", saved, p.posts)
				}
			case "wrong revision", "late ack", "outcome failure", "cancel send":
				if p.posts != 1 || saved.CleanupStatus != domain.CleanupUnknown {
					t.Fatal("uncertain input retried/cleared", saved, p.posts)
				}
			default:
				if p.posts != 15 || saved.CleanupStatus != domain.CleanupDirty {
					t.Fatal("observation failure lost forwarded responsibility", saved, p.posts)
				}
			}
			if fault == "transient violation" {
				var report installedRunReport
				if json.Unmarshal(saved.CanonicalReportJSON, &report) != nil || report.Adverse == nil || report.Adverse.Disposition != oscar.InstalledFloodViolation || report.Evaluation.Disposition != oscar.InstalledFloodEligible || saved.Verdict != domain.VerdictFail || p.sleeps < 90 {
					t.Fatal("adverse poll was not retained through later clean interval", string(saved.CanonicalReportJSON))
				}
			}
			if fault == "partial audit" {
				raw, e := os.ReadFile(filepath.Join(p.dir, "runs", p.run.ID, "installed/observations/000/audit-raw-rows.json"))
				var rows []json.RawMessage
				if e != nil || json.Unmarshal(raw, &rows) != nil || len(rows) != 14 {
					t.Fatal("prior complete audit reads lost", len(rows), e)
				}
			}
			if e == nil && fault != "transient violation" && fault != "missing observations" {
				t.Fatal("local failure had no error")
			}
		})
	}
}

func TestInstalledRunnerArtifactFailurePreventsHTTP(t *testing.T) {
	p := newInstalledRunnerPeer(t, "")
	p.rawSQL(`CREATE TRIGGER fail_artifact BEFORE UPDATE ON artifacts BEGIN SELECT RAISE(ABORT,'artifact publication failed'); END`)
	if e := p.engine().Execute(context.Background(), p.run); e == nil {
		t.Fatal("artifact failure ignored")
	}
	if p.gets != 0 || p.posts != 0 {
		t.Fatal("network before durable originals")
	}
}

func TestInstalledRunnerConstructionRequiresOriginalObjects(t *testing.T) {
	p := newInstalledRunnerPeer(t, "")
	for _, name := range []string{"kit", "installation", "artifacts", "api", "receiver", "credentials"} {
		t.Run(name, func(t *testing.T) {
			k, i, a, api, receiver := p.kit, p.installation, p.artifacts, p.api, p.receiver
			switch name {
			case "kit":
				k = &testpack.Kit{}
			case "installation":
				i = &testpack.Installation{}
			case "artifacts":
				a = nil
			case "api":
				api.BaseURL += "/other"
			case "receiver":
				receiver.BaseURL += "/other"
			case "credentials":
				api.Credential = domain.CredentialRef{}
			}
			if r, e := NewInstalledPackRunner(k, i, api, receiver, p.db, a, InstalledPackRunnerOptions{Getenv: func(string) string { return "key" }}); e == nil || r != nil {
				t.Fatal("invalid constructor accepted")
			}
		})
	}
}

func TestInstalledRunnerCancelledBeforeStartRetainsModeWithoutHTTP(t *testing.T) {
	p := newInstalledRunnerPeer(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := p.engine().Execute(ctx, p.run); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Status != domain.RunCompleted || saved.CleanupStatus != domain.CleanupNotRequired || saved.Verdict != domain.VerdictInconclusive || !bytes.Contains(saved.CompiledPlanJSON, []byte(`"inputPath":"installed-test-pack"`)) || p.gets != 0 || p.posts != 0 {
		t.Fatal("cancelled execution lost mode or performed HTTP", saved, p.gets, p.posts)
	}
}

func TestInstalledRunnerExistingHistoryTokenRefusedBeforeInput(t *testing.T) {
	p := newInstalledRunnerPeer(t, "history collision")
	manifest, e := oscar.FreezeInstalledPackInput(oscar.InstalledPackInputSpec{APIVersion: "corrtest.oscar/installed-input-v1", ManifestSHA256: p.kit.Bindings().ManifestSHA256, QualificationRun: strings.Repeat("ab", 16), StartsAt: p.now.Add(-24 * time.Hour), ReceiverTargetID: p.receiver.ID, ReceiverBaseURL: p.receiver.BaseURL})
	if e != nil {
		t.Fatal(e)
	}
	_, raw, e := manifest.Prepare(0)
	if e != nil {
		t.Fatal(e)
	}
	var record map[string]any
	if e = json.Unmarshal(raw, &record); e != nil {
		t.Fatal(e)
	}
	p.records = []map[string]any{record}
	if e = p.engine().Execute(context.Background(), p.run); e == nil || p.posts != 0 {
		t.Fatal("existing History token admitted", e, p.posts)
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil || saved.CleanupStatus != domain.CleanupNotRequired {
		t.Fatal(saved, e)
	}
}

func TestInstalledRunnerRecorderFailureDoesNotSend(t *testing.T) {
	p := newInstalledRunnerPeer(t, "")
	p.rawSQL(`CREATE TRIGGER fail_attempt BEFORE INSERT ON installed_pack_attempts BEGIN SELECT RAISE(ABORT,'attempt failed'); END`)
	if e := p.engine().Execute(context.Background(), p.run); e == nil {
		t.Fatal("recorder failure ignored")
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if p.posts != 0 || saved.CleanupStatus != domain.CleanupNotRequired || saved.Verdict == domain.VerdictPass {
		t.Fatal(saved, p.posts)
	}
}

func TestInstalledRunnerFinalizationReadsPersistedUnknown(t *testing.T) {
	p := newInstalledRunnerPeer(t, "")
	r := p.engine()
	ctx := context.Background()
	if e := p.db.TransitionRun(ctx, p.run.ID, domain.RunPreflight, p.now, "test original preflight"); e != nil {
		t.Fatal(e)
	}
	spec := oscar.InstalledPackInputSpec{APIVersion: "corrtest.oscar/installed-input-v1", ManifestSHA256: p.kit.Bindings().ManifestSHA256, QualificationRun: strings.Repeat("ab", 16), StartsAt: p.now, ReceiverTargetID: p.receiver.ID, ReceiverBaseURL: p.receiver.BaseURL}
	if e := p.db.RegisterInstalledPack(ctx, p.run.ID, p.kit, p.installation, spec, p.now); e != nil {
		t.Fatal(e)
	}
	for _, state := range []domain.RunStatus{domain.RunSettingUp, domain.RunInjecting} {
		if e := p.db.TransitionRun(ctx, p.run.ID, state, p.now, "test original lifecycle"); e != nil {
			t.Fatal(e)
		}
	}
	manifest, _ := oscar.FreezeInstalledPackInput(spec)
	evidence, _, _ := manifest.Prepare(0)
	if e := p.db.RecordInstalledPackAttempt(ctx, p.run.ID, evidence, p.now); e != nil {
		t.Fatal(e)
	}
	// A committed record followed by an error returned to the caller is not a
	// permission to clear/replay; use the original persisted state, not a flag.
	originalErr := errors.New("recorder result unavailable after durable commit")
	report := installedRunReport{InstalledPackRunReport: oscar.InstalledPackRunReport{RunID: p.run.ID, InputPath: InstalledPackInputPath, Verdict: domain.VerdictInconclusive}}
	if e := r.finish(p.run.ID, &report, originalErr); !errors.Is(e, originalErr) {
		t.Fatal(e)
	}
	saved, e := p.db.GetRun(ctx, p.run.ID)
	if e != nil || saved.CleanupStatus != domain.CleanupUnknown || p.posts != 0 {
		t.Fatal(saved, e, p.posts)
	}
	if e := p.db.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := storage.Open(ctx, p.dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	snap, e := reopened.ReadInstalledPackSnapshot(ctx, p.run.ID)
	if e != nil || len(snap.Attempts) != 1 || snap.Attempts[0].Result != nil {
		t.Fatal(snap, e)
	}
}

func TestInstalledRunnerEvidenceBudgetRefusesWithoutPartialPublication(t *testing.T) {
	for _, kind := range []string{"count", "aggregate", "entry"} {
		t.Run(kind, func(t *testing.T) {
			p := newInstalledRunnerPeer(t, "")
			r := p.engine()
			report := installedRunReport{InstalledPackRunReport: oscar.InstalledPackRunReport{RunID: p.run.ID}}
			raw := []byte(`{}`)
			switch kind {
			case "count":
				report.observationCount = installedObservationCount
			case "aggregate":
				report.observationBytes = installedObservationBytes
			case "entry":
				raw = make([]byte, installedEntryBytes+1)
			}
			if e := r.publish(context.Background(), &report, "installed/observations/refused.json", raw, false); e == nil {
				t.Fatal("budget accepted")
			}
			artifacts, e := p.db.ListArtifacts(context.Background(), p.run.ID)
			if e != nil || len(artifacts) != 0 {
				t.Fatal("budget refusal published", artifacts, e)
			}
		})
	}
}

func TestInstalledRunnerSameExactRootTargetSupported(t *testing.T) {
	p := newInstalledRunnerPeer(t, "same target")
	if _, e := NewInstalledPackRunner(p.kit, p.installation, p.api, p.receiver, p.db, p.artifacts, InstalledPackRunnerOptions{Now: p.clock, Getenv: func(string) string { return "test-key" }}); e != nil {
		t.Fatal(e)
	}
}

func TestInstalledRunnerWaitsFullIntervalAfterLastAcknowledgment(t *testing.T) {
	p := newInstalledRunnerPeer(t, "delayed final ack")
	if e := p.engine().Execute(context.Background(), p.run); e != nil {
		t.Fatal(e)
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Verdict != domain.VerdictPass || p.sleeps < 90 || p.now.Sub(p.start) < 145*time.Second {
		t.Fatalf("incomplete post-forward interval: verdict=%s sleeps=%d elapsed=%v", saved.Verdict, p.sleeps, p.now.Sub(p.start))
	}
}

func TestInstalledRunnerInsufficientOverallBudgetCannotPass(t *testing.T) {
	p := newInstalledRunnerPeer(t, "insufficient interval")
	if e := p.engine().Execute(context.Background(), p.run); e == nil {
		t.Fatal("budget failure ignored")
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Verdict != domain.VerdictInconclusive || saved.CleanupStatus != domain.CleanupDirty || p.posts != 15 {
		t.Fatal(saved, p.posts)
	}
}

func TestInstalledRunnerWallJumpCannotShortenElapsedInterval(t *testing.T) {
	p := newInstalledRunnerPeer(t, "wall jump")
	if e := p.engine().Execute(context.Background(), p.run); e == nil {
		t.Fatal("wall-only jump admitted shortened interval")
	}
	saved, e := p.db.GetRun(context.Background(), p.run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Verdict != domain.VerdictInconclusive || saved.CleanupStatus != domain.CleanupDirty || p.posts != 15 {
		t.Fatal(saved, p.posts)
	}
}
