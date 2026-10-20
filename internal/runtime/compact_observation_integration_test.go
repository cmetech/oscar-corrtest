//go:build linux || darwin

package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func compactObservationPeer(t *testing.T, mode string) (*Runtime, *testpack.Kit, *testpack.Installation, string, *atomic.Int64, <-chan struct{}) {
	t.Helper()
	responses := map[string][]byte{}
	dir := "../testpack/testdata/installation/records"
	files, err := filepath.Glob(filepath.Join(dir, "*-before.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		before, _ := os.ReadFile(name)
		var request struct{ Request struct{ Method, Path string } }
		if json.Unmarshal(before, &request) != nil || request.Request.Method != "GET" {
			continue
		}
		after, _ := os.ReadFile(strings.Replace(name, "-before.json", "-after.json", 1))
		var response struct{ Body json.RawMessage }
		if err := json.Unmarshal(after, &response); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(request.Request.Path)
		responses[u.Path] = response.Body
	}
	var runtime map[string]json.RawMessage
	if err := json.Unmarshal(responses["/api/v1/correlation_rules/runtime"], &runtime); err != nil {
		t.Fatal(err)
	}
	var compact map[string]json.RawMessage
	_ = json.Unmarshal(bytes.Replace(compactRuntimeFixture(), []byte(`"rule_id":1`), []byte(`"rule_id":7`), 1), &compact)
	runtime["enhanced_correlation"], runtime["compact_summary"] = compact["enhanced_correlation"], compact["compact_summary"]
	if mode == "current-load" || mode == "load-changed" {
		runtime["loaded_at"] = json.RawMessage(`"2026-10-20T10:00:00Z"`)
		runtime["last_attempt_ms"] = json.RawMessage(`1792490400000`)
		runtime["checked_at_ms"] = json.RawMessage(`1792490401000`)
	}
	responses["/api/v1/correlation_rules/runtime"], _ = json.Marshal(runtime)
	payload := map[string]any{
		"format": "compact-evidence-v1", "observed_at": "2026-10-20T12:00:00Z",
		"scope":              map[string]any{"rule_id": 7, "epoch": strings.Repeat("a", 32), "revision": strings.Repeat("b", 64)},
		"inventory_complete": true, "generation_count": 0, "partition": map[string]any{"state": "open", "version": 1}, "actions": []any{},
	}
	plain, _ := json.Marshal(payload)
	canonical, err := testpack.CanonicalJSONInt64(plain)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	payload["evidence_sha256"] = hex.EncodeToString(hash[:])
	evidence, _ := json.MarshalIndent(payload, "", "  ")
	calls, runtimeCalls := &atomic.Int64{}, &atomic.Int64{}
	entered := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Method != "GET" || req.Header.Get("X-API-Key") != "synthetic-observer-key" {
			http.Error(w, "wrong read authority", 500)
			return
		}
		if mode == "blocked" {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-req.Context().Done()
			return
		}
		if req.URL.Path == "/api/v1/alerts/correlation/compact/evidence" {
			if req.URL.Query().Get("rule_id") != "7" || req.URL.Query().Get("epoch") != strings.Repeat("a", 32) || len(req.URL.Query()) != 3 {
				http.Error(w, "scope differs", 500)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			if mode == "unavailable" {
				http.Error(w, `{"detail":"synthetic private failure"}`, 503)
				return
			}
			_, _ = w.Write(evidence)
			return
		}
		body, found := responses[req.URL.Path]
		if !found {
			http.Error(w, "unlisted read", 404)
			return
		}
		if req.URL.Path == "/api/v1/correlation_rules/runtime" && runtimeCalls.Add(1) == 2 {
			if mode == "scope-changed" {
				body = bytes.Replace(body, []byte(strings.Repeat("a", 32)), []byte(strings.Repeat("d", 32)), 1)
			}
			if mode == "manifest-changed" {
				body = bytes.Replace(body, []byte(strings.Repeat("c", 64)), []byte(strings.Repeat("d", 64)), 1)
			}
			if mode == "load-changed" {
				body = bytes.Replace(body, []byte("2026-10-20T10:00:00Z"), []byte("2026-10-20T11:00:00Z"), 1)
			}
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	kit, installation := installedRuntimeObjects(t, server.URL)
	app, err := Open(context.Background(), config.Settings{DataDir: t.TempDir()}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	app.getenv = func(string) string { return "synthetic-observer-key" }
	target, err := app.CreateTarget(context.Background(), domain.TargetInput{
		DisplayName: "compact observer", BaseURL: server.URL, TLS: domain.TLSPolicy{CAPath: ca},
		Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "TEST_OBSERVATION_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, kit, installation, target.ID, calls, entered
}

func TestCompactObservationConnectedReadAndIndependentReplay(t *testing.T) {
	app, kit, original, target, calls, _ := compactObservationPeer(t, "")
	path := filepath.Join(t.TempDir(), "observation.json")
	result, err := app.ObserveCompact(context.Background(), kit, original, target, path)
	if err != nil {
		raw, _ := os.ReadFile(path)
		t.Fatalf("connected read failed: %v %s", err, raw)
	}
	if result.Outcome != "observed" || calls.Load() != 13 {
		t.Fatalf("missing real before/evidence/after calls: %+v %d", result, calls.Load())
	}
	raw, _ := os.ReadFile(path)
	if err := ValidateCompactObservation(raw, kit, original); err != nil {
		t.Fatal(err)
	}
	var report CompactObservationReport
	_ = json.Unmarshal(raw, &report)
	if !bytes.Contains(report.Evidence.Body, []byte("\n  ")) || report.Evidence.HTTPStatus != 200 || report.Scope.RuleID != 7 {
		t.Fatal("lost original HTTP bytes or actual runtime scope")
	}
	if strings.Contains(string(raw), "synthetic-observer-key") {
		t.Fatal("credential leaked")
	}
	for _, ambiguous := range [][]byte{
		append([]byte(`{"outcome":"unavailable",`), raw[1:]...),
		append([]byte(`{"OUTCOME":"unavailable",`), raw[1:]...),
	} {
		if err := ValidateCompactObservation(ambiguous, kit, original); err == nil {
			t.Fatal("ambiguous report fields replayed as successful observation")
		}
	}
	for _, mutation := range []string{"scope", "manifest", "digest", "chronology", "claim", "original"} {
		t.Run(mutation, func(t *testing.T) {
			var changed CompactObservationReport
			_ = json.Unmarshal(raw, &changed)
			switch mutation {
			case "scope":
				changed.Scope.Epoch = strings.Repeat("f", 32)
			case "manifest":
				changed.ConsumerManifest = strings.Repeat("f", 64)
			case "digest":
				changed.Evidence.Body = bytes.Replace(changed.Evidence.Body, []byte(`"generation_count": 0`), []byte(`"generation_count": 1`), 1)
			case "chronology":
				changed.After.StartedAt = changed.StartedAt.Add(-time.Hour)
			case "claim":
				changed.Outcome = "PASS"
			case "original":
				changed.Installation.RuleID++
			}
			bad, _ := json.Marshal(changed)
			if err := ValidateCompactObservation(bad, kit, original); err == nil {
				t.Fatal("tampered observation qualified")
			}
		})
	}
}

func TestCompactObservationFailuresRetainUnavailableEvidence(t *testing.T) {
	for _, mode := range []string{"unavailable", "scope-changed", "manifest-changed", "load-changed"} {
		t.Run(mode, func(t *testing.T) {
			app, kit, original, target, calls, _ := compactObservationPeer(t, mode)
			path := filepath.Join(t.TempDir(), "observation.json")
			result, err := app.ObserveCompact(context.Background(), kit, original, target, path)
			if err == nil || result.Outcome != "unavailable" || result.Path != path {
				t.Fatalf("failed read became success: %+v %v", result, err)
			}
			raw, _ := os.ReadFile(path)
			if bytes.Contains(raw, []byte("synthetic private failure")) || ValidateCompactObservation(raw, kit, original) == nil {
				t.Fatal("error body leaked or failed observation qualified")
			}
			var report CompactObservationReport
			_ = json.Unmarshal(raw, &report)
			if mode == "unavailable" && (report.Evidence.HTTPStatus != 503 || calls.Load() != 7) {
				t.Fatal("lost actual unavailable status or retried")
			}
		})
	}
}

func TestCompactObservationCapturesCurrentLoadWithoutChangingOriginalInstallation(t *testing.T) {
	app, kit, original, target, calls, _ := compactObservationPeer(t, "current-load")
	path := filepath.Join(t.TempDir(), "current-load.json")
	result, err := app.ObserveCompact(context.Background(), kit, original, target, path)
	if err != nil || result.Outcome != "observed" || calls.Load() != 13 {
		t.Fatalf("valid current runtime refused: %+v %v calls=%d", result, err, calls.Load())
	}
	raw, _ := os.ReadFile(path)
	if err := ValidateCompactObservation(raw, kit, original); err != nil {
		t.Fatal(err)
	}
	var report CompactObservationReport
	_ = json.Unmarshal(raw, &report)
	if report.Installation != original.Binding() || bytes.Contains(report.Before.Bodies["runtime"], []byte(original.Binding().LoadedAt)) {
		t.Fatal("original installation was rewritten or current runtime was lost")
	}
}

func TestCompactObservationExistingDestinationRefusesBeforeCredentials(t *testing.T) {
	app, kit, original, target, calls, _ := compactObservationPeer(t, "")
	path := filepath.Join(t.TempDir(), "existing.json")
	_ = os.WriteFile(path, []byte("original"), 0600)
	app.getenv = func(string) string { t.Fatal("existing output resolved credentials"); return "" }
	if _, err := app.ObserveCompact(context.Background(), kit, original, target, path); err == nil || calls.Load() != 0 {
		t.Fatal("existing output made network request")
	}
}

func TestCompactObservationShutdownJoinsFailurePublication(t *testing.T) {
	app, kit, original, target, _, entered := compactObservationPeer(t, "blocked")
	path := filepath.Join(t.TempDir(), "cancelled.json")
	done := make(chan error, 1)
	go func() {
		_, err := app.ObserveCompact(context.Background(), kit, original, target, path)
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("never entered actual read: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("read not started")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("Close returned before failure evidence publication")
	}
	var report CompactObservationReport
	if json.Unmarshal(raw, &report) != nil || report.Outcome != "unavailable" || report.FailureClass != "cancelled" {
		t.Fatalf("cancellation not retained: %s", raw)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled observation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal(fmt.Sprintf("Close returned before observer exit: %s", path))
	}
}
