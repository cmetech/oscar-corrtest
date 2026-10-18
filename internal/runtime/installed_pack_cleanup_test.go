package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestInstalledPackCleanupRefusesBeforeClientConstruction(t *testing.T) {
	for _, tc := range []struct {
		name, planned, reported string
		corruptRegistration     bool
	}{
		{"compiled mode", "installed-test-pack", "", false},
		{"reported mode", "", "installed-test-pack", false},
		{"report cannot hide compiled mode", "installed-test-pack", "alert-engine", false},
		{"lost markers cannot hide corrupt registration", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			settings := config.Settings{DataDir: t.TempDir()}
			app, err := Open(ctx, settings, version.Info{Version: "test"})
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			target, err := app.CreateTarget(ctx, domain.TargetInput{DisplayName: "unused", BaseURL: "http://127.0.0.1:1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "TEST_KEY"}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := app.CreateRun(ctx, target.ID, "", "test")
			if err != nil {
				t.Fatal(err)
			}
			plan, _ := json.Marshal(map[string]string{"runId": run.ID, "pattern": "flood", "inputPath": tc.planned})
			if err := app.database.SetRunExecutionDocuments(ctx, run.ID, []byte(`{}`), plan, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			completeTestRun(t, app, run.ID, domain.CleanupUnknown)
			// Model lost/inconsistent terminal metadata independently of the durable contract.
			db, err := sql.Open("sqlite", filepath.Join(settings.DataDir, "corrtest.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			report, _ := json.Marshal(map[string]any{"coverage": map[string]string{"inputPath": tc.reported}})
			if _, err = db.Exec(`UPDATE runs SET canonical_report_json=? WHERE id=?`, string(report), run.ID); err != nil {
				t.Fatal(err)
			}
			if tc.corruptRegistration {
				_, err = db.Exec(`INSERT INTO installed_pack_runs VALUES(?,?,?,?,?,?,?,?)`, run.ID, strings.Repeat("a", 32), strings.Repeat("b", 64), target.ID, target.BaseURL, `{}`, strings.Repeat("c", 64), time.Now().UTC().Format(time.RFC3339Nano))
				if err != nil {
					t.Fatal(err)
				}
			}
			credentialReads := 0
			app.getenv = func(string) string { credentialReads++; return "secret" }
			before, err := app.ListRunEvents(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := app.RetryCleanup(ctx, run.ID)
			if err == nil || !strings.Contains(err.Error(), "installed") || updated.CleanupStatus != domain.CleanupUnknown || credentialReads != 0 {
				t.Fatalf("cleanup must refuse before client and preserve UNKNOWN: status=%s credentialReads=%d err=%v", updated.CleanupStatus, credentialReads, err)
			}
			after, err := app.ListRunEvents(ctx, run.ID)
			if err != nil || len(after) != len(before) {
				t.Fatalf("cleanup changed durable events: %v", err)
			}
		})
	}
}
