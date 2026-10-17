package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestOwnedExistingFeedExportRetainsValidatedDurableOutcomes(t *testing.T) {
	for _, state := range []string{"unknown", "forwarded", "rejected", "indeterminate", "unattempted", "corrupt-outcome", "plan-drift", "unattempted-plan-drift", "missing-registration"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			directory := t.TempDir()
			runtime, err := Open(ctx, config.Settings{DataDir: directory}, version.Info{Version: "test"})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			target, err := runtime.CreateTarget(ctx, domain.TargetInput{DisplayName: "owned History", BaseURL: "http://owned-history", APIProfile: "public-v1"})
			if err != nil {
				t.Fatal(err)
			}
			run, err := runtime.CreateRun(ctx, target.ID, "", "test")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := os.ReadFile("../oscar/testdata/existing_feed/splunk.json")
			if err != nil {
				t.Fatal(err)
			}
			spec := oscar.ExistingFeedManifestSpec{CohortID: strings.Repeat("a", 32), TargetID: "receiver", ReceiverOrigin: "http://owned-receiver", Profile: "splunk", ProfileHash: "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b", Mapping: oscar.ExistingFeedMappingSnapshot{Source: "tests/oscar-webhookreceiver/fixtures/splunk/baseline.json", SHA256: "11a7c673edb751f5c0ce3524457977b503a39ee36979b51ce9a1c6a1f5213eae", BodyBase64: base64.StdEncoding.EncodeToString(snapshot)}, UserAgent: "owned-existing-feed/1", RequestHost: "owned-receiver", StartsAt: time.Now().UTC().Truncate(time.Second)}
			plan := runner.OwnedExistingFeedPlan{APIVersion: runner.OwnedExistingFeedVersion, Manifest: spec, NativeTargetID: "native", NativeOrigin: "http://owned-native", HistoryTargetID: target.ID, HistoryOrigin: target.BaseURL, OwnershipReference: "owned-controller/artifact"}
			if state != "missing-registration" {
				if err = runtime.database.RegisterExistingFeed(ctx, run.ID, spec, spec.StartsAt); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(state, "plan-drift") {
				plan.Manifest.CohortID = strings.Repeat("b", 32)
			}
			document, _ := json.Marshal(map[string]any{"runId": run.ID, "pattern": "owned-existing-feed-identity", "inputPath": "owned-existing-feed", "ownedExistingFeed": plan})
			if err = runtime.database.SetRunExecutionDocuments(ctx, run.ID, []byte(`{}`), document, spec.StartsAt); err != nil {
				t.Fatal(err)
			}
			for _, status := range []domain.RunStatus{domain.RunPreflight, domain.RunSettingUp, domain.RunInjecting} {
				if err = runtime.database.TransitionRun(ctx, run.ID, status, spec.StartsAt, "test"); err != nil {
					t.Fatal(err)
				}
			}
			manifest, _ := oscar.FreezeExistingFeedManifest(spec)
			request, _, _ := manifest.Prepare(0)
			if state != "unattempted" && state != "unattempted-plan-drift" && state != "missing-registration" {
				if err = runtime.database.RecordExistingFeedAttempt(ctx, run.ID, request, spec.StartsAt); err != nil {
					t.Fatal(err)
				}
			}
			var result *oscar.InjectionResult
			transportError := ""
			switch state {
			case "forwarded", "rejected", "indeterminate":
				result = &oscar.InjectionResult{Class: oscar.InjectionClass(state), StatusCode: 200, Body: []byte(`{"status":"ok"}`)}
				if state == "rejected" {
					result.Body = []byte(`{"status":"acl_denied"}`)
				}
				if state == "indeterminate" {
					result.StatusCode = 0
					result.Body = nil
					transportError = "request interrupted"
				}
				if err = runtime.database.CompleteExistingFeedAttempt(ctx, run.ID, "splunk", 0, request.Request.BodySHA256, *result, transportError, spec.StartsAt); err != nil {
					t.Fatal(err)
				}
			}
			for _, status := range []domain.RunStatus{domain.RunCancelling, domain.RunCleaningUp} {
				if err = runtime.database.TransitionRun(ctx, run.ID, status, spec.StartsAt, "test"); err != nil {
					t.Fatal(err)
				}
			}
			if err = runtime.database.CompleteRun(ctx, run.ID, domain.VerdictError, domain.CleanupUnknown, []byte(`{"coverage":{"inputPath":"owned-existing-feed"}}`), "test outcome", spec.StartsAt); err != nil {
				t.Fatal(err)
			}
			if state == "corrupt-outcome" {
				raw, openErr := sql.Open("sqlite", filepath.Join(directory, "corrtest.db"))
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer raw.Close()
				if _, err = raw.Exec(`UPDATE existing_feed_attempts SET result_json='{}',completed_at='2026-10-17T00:00:00Z'`); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "evidence.zip")
			_, err = runtime.ExportRun(ctx, run.ID, destination)
			if state == "corrupt-outcome" || strings.HasSuffix(state, "plan-drift") || state == "missing-registration" {
				if err == nil {
					t.Fatal("unbound/corrupt outcomes exported")
				}
				if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
					t.Fatal("failed export published file")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = runtime.VerifyBundle(ctx, destination); err != nil {
				t.Fatal(err)
			}
			archive, err := zip.OpenReader(destination)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			found := false
			for _, entry := range archive.File {
				if entry.Name != "existing-feed-attempts.json" {
					continue
				}
				found = true
				reader, readErr := entry.Open()
				if readErr != nil {
					t.Fatal(readErr)
				}
				raw, _ := io.ReadAll(reader)
				_ = reader.Close()
				var document struct {
					APIVersion string                         `json:"apiVersion"`
					RunID      string                         `json:"runId"`
					Manifest   oscar.ExistingFeedManifestSpec `json:"manifest"`
					Attempts   []storage.ExistingFeedAttempt  `json:"attempts"`
				}
				if err = json.Unmarshal(raw, &document); err != nil {
					t.Fatal(err)
				}
				if document.APIVersion != "corrtest.oscar/existing-feed-attempts-v1" || document.RunID != run.ID || !reflect.DeepEqual(document.Manifest, spec) {
					t.Fatal("export manifest binding changed")
				}
				if state == "unattempted" {
					if len(document.Attempts) != 0 {
						t.Fatal("invented attempt")
					}
					continue
				}
				if len(document.Attempts) != 1 || !reflect.DeepEqual(document.Attempts[0].Evidence, request) || !reflect.DeepEqual(document.Attempts[0].Result, result) || document.Attempts[0].TransportError != transportError {
					t.Fatalf("outcome changed: %s", raw)
				}
				if bytes.Contains(raw, []byte(`"not_sent"`)) {
					t.Fatal("UNKNOWN relabeled unsent")
				}
			}
			if !found {
				t.Fatal("durable feed outcomes absent from bundle")
			}
			retained, err := runtime.GetRun(ctx, run.ID)
			if err != nil || retained.CleanupStatus != domain.CleanupUnknown {
				t.Fatal("export changed responsibility")
			}
		})
	}
}
