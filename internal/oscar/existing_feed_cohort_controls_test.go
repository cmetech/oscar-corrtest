package oscar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func existingFeedCohortFixture(t *testing.T, profile string, handler func(http.ResponseWriter, *http.Request)) (existingFeedCohortConfig, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	spec := existingFeedSpec(t, profile)
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	config := existingFeedCohortConfig{Manifest: spec, StateDir: state, Phase: "first", MappingSnapshotPath: filepath.Join(root, "mapping.json")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)) - 1
		attempt, err := os.ReadFile(filepath.Join(state, fmtIndex(index)+".attempt.json"))
		if err != nil {
			t.Error("POST before durable attempt", err)
		}
		var evidence ExistingFeedRequestEvidence
		if err := json.Unmarshal(attempt, &evidence); err != nil {
			t.Error(err)
		}
		body, _ := io.ReadAll(r.Body)
		saved, _ := base64.StdEncoding.DecodeString(evidence.Request.BodyBase64)
		if string(body) != string(saved) || evidence.Manifest != config.Manifest {
			t.Error("wire or persisted manifest differs")
		}
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("X-Oscar-Profile-Revision", spec.ProfileHash)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	t.Cleanup(server.Close)
	manifest, target, _ := existingFeedReceiver(t, profile, server.URL)
	config.Manifest = manifest.spec
	credential := filepath.Join(root, "receiver-key")
	if err := os.WriteFile(credential, []byte("receiver-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	target.Credential.Kind = "file"
	target.Credential.Reference = credential
	config.Receiver = target
	snapshot, _ := base64.StdEncoding.DecodeString(spec.Mapping.BodyBase64)
	if err := os.WriteFile(config.MappingSnapshotPath, snapshot, 0400); err != nil {
		t.Fatal(err)
	}
	return config, &calls
}

func TestExistingFeedCohortReopensWithoutRepeatingInputs(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) {
			config, calls := existingFeedCohortFixture(t, profile, nil)
			for _, phase := range []string{"first", "remaining", "no-match"} {
				config.Phase = phase
				if err := runExistingFeedOwnedCohort(context.Background(), config); err != nil {
					t.Fatal(err)
				}
				before := calls.Load()
				if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != before {
					t.Fatal("reopened phase repeated POST")
				}
			}
			if calls.Load() != 6 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestExistingFeedCohortRefusesMissingOrChangedProof(t *testing.T) {
	for _, mode := range []string{"missing snapshot", "changed snapshot", "foreign manifest", "foreign phase", "missing first phase", "unsafe state directory"} {
		t.Run(mode, func(t *testing.T) {
			config, calls := existingFeedCohortFixture(t, "splunk", nil)
			switch mode {
			case "missing snapshot":
				config.MappingSnapshotPath += ".missing"
			case "changed snapshot":
				config.MappingSnapshotPath = filepath.Join(t.TempDir(), "foreign.json")
				if err := os.WriteFile(config.MappingSnapshotPath, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign manifest":
				config.Manifest.ProfileHash = strings.Repeat("0", 64)
			case "foreign phase":
				config.Phase = "clear"
			case "missing first phase":
				config.Phase = "remaining"
			case "unsafe state directory":
				if err := os.Chmod(config.StateDir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != 0 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestExistingFeedCohortRetainsUnknownAndRefusesLaterPhases(t *testing.T) {
	config, calls := existingFeedCohortFixture(t, "splunk", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	for _, phase := range []string{"first", "remaining", "no-match"} {
		config.Phase = phase
		if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != 1 {
			t.Fatal("unknown attempt resumed")
		}
	}
	if _, err := os.Stat(filepath.Join(config.StateDir, "0.attempt.json")); err != nil {
		t.Fatal("unknown attempt lost", err)
	}
}

func TestExistingFeedCohortRefusesPartialOrCorruptJournal(t *testing.T) {
	for _, mode := range []string{"partial attempt", "missing result", "corrupt result", "wrong result identity", "conflicting response", "foreign manifest", "orphan result"} {
		t.Run(mode, func(t *testing.T) {
			config, calls := existingFeedCohortFixture(t, "splunk", nil)
			if err := runExistingFeedOwnedCohort(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(config.StateDir, "0.result.json")
			switch mode {
			case "partial attempt":
				path = filepath.Join(config.StateDir, "0.attempt.json")
			case "missing result":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "foreign manifest":
				path = filepath.Join(config.StateDir, "manifest.json")
			case "orphan result":
				if err := os.Remove(filepath.Join(config.StateDir, "0.attempt.json")); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "missing result" && mode != "orphan result" {
				contents := []byte(`{`)
				if mode == "wrong result identity" {
					contents = []byte(`{"bodySha256":"foreign","result":{"class":"forwarded","statusCode":200}}`)
				}
				if mode == "conflicting response" {
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var saved existingFeedCohortResult
					if err := json.Unmarshal(raw, &saved); err != nil {
						t.Fatal(err)
					}
					saved.Result.Body = []byte(`{"status":"rejected"}`)
					contents, _ = json.Marshal(saved)
					contents = append(contents, '\n')
				}
				if err := os.WriteFile(path, contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			config.Phase = "remaining"
			if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != 1 {
				t.Fatal("corrupt journal resumed")
			}
		})
	}
}

func fmtIndex(index int) string { return string(rune('0' + index)) }

func TestExistingFeedCohortPartialAttemptPreventsFirstPOST(t *testing.T) {
	config, calls := existingFeedCohortFixture(t, "splunk", nil)
	if err := os.WriteFile(filepath.Join(config.StateDir, "0.attempt.json"), []byte(`{"manifest":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != 0 {
		t.Fatal("partial pre-send attempt retried")
	}
}

func TestExistingFeedCohortProcessLossRetainsUnknown(t *testing.T) {
	received, release := make(chan struct{}), make(chan struct{})
	config, calls := existingFeedCohortFixture(t, "splunk", func(w http.ResponseWriter, r *http.Request) { close(received); <-release })
	configPath := filepath.Join(t.TempDir(), "config.json")
	body, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestExistingFeedOwnedCohort$", "-test.timeout=10s")
	command.Env = append(os.Environ(), "CORRTEST_EXISTING_FEED_LAB_CONFIG="+configPath)
	if err := command.Start(); err != nil {
		close(release)
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		close(release)
		t.Fatal("child did not reach recorded HTTP attempt")
	}
	if err := command.Process.Kill(); err != nil {
		close(release)
		t.Fatal(err)
	}
	_ = command.Wait()
	close(release)
	before, err := os.ReadFile(filepath.Join(config.StateDir, "0.attempt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.StateDir, "0.result.json")); !os.IsNotExist(err) {
		t.Fatal("interrupted response unexpectedly has result")
	}
	for _, phase := range []string{"first", "remaining"} {
		config.Phase = phase
		if err := runExistingFeedOwnedCohort(context.Background(), config); err == nil || calls.Load() != 1 {
			t.Fatal("killed attempt repeated")
		}
	}
	after, err := os.ReadFile(filepath.Join(config.StateDir, "0.attempt.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("unknown attempt changed")
	}
}
