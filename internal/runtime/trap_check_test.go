package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestTrapCheckMappedProblemAndClear(t *testing.T) {
	bin := t.TempDir()
	state := filepath.Join(bin, "state")
	stub := filepath.Join(bin, "snmptrap")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncase \"$*\" in *private-test-value*) exit 1;; *.0.3*) printf firing > \"$TRAP_TEST_STATE\";; *.0.4*) printf resolved > \"$TRAP_TEST_STATE\";; esac\n[ -f \"$SNMPCONFPATH/snmp.conf\" ] || exit 1\nshift 7\n[ \"$3\" = i ] && [ \"$6\" = s ] || exit 1\nprintf %s \"$1$4\" > \"$TRAP_TEST_STATE.key\"\nprintf %s \"$7\" > \"$TRAP_TEST_STATE.description\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TRAP_TEST_STATE", state)
	t.Setenv("CORRTEST_SNMP_COMMUNITY", "private-test-value")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ext/mw/api/v1/alerts/history" {
			http.NotFound(w, request)
			return
		}
		value, _ := os.ReadFile(state)
		rows := []any{}
		if len(value) > 0 {
			keyBytes, _ := os.ReadFile(state + ".key")
			rows = append(rows, map[string]any{"id": "11111111-1111-4111-8111-111111111111", "alertname": trapAlertName,
				"fingerprint": "fp", "status": string(value), "severity": "major", "startsAt": time.Now().UTC().Format(time.RFC3339Nano),
				"createdAt": time.Now().UTC().Format(time.RFC3339Nano), "labels": []map[string]string{
					{"Label": "alert_group", "Value": trapAlertGroup}, {"Label": "agent", "Value": trapAgent}, {"Label": "alert_key", "Value": string(keyBytes)},
				}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"total_records": len(rows), "total_pages": 1, "per_page": 100, "records": rows})
	}))
	defer server.Close()
	r, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	target, err := r.CreateTarget(context.Background(), domain.TargetInput{DisplayName: "trap", BaseURL: server.URL + "/ext/mw", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.StartTrapCheck(context.Background(), target.ID, TrapCheckOptions{Port: 1162})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := r.GetRun(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == domain.RunCompleted {
			if got.Verdict != domain.VerdictPass || got.CleanupStatus != domain.CleanupClean || strings.Contains(string(got.CanonicalReportJSON), "private-test-value") {
				t.Fatalf("unexpected run result: verdict=%s cleanup=%s report=%s", got.Verdict, got.CleanupStatus, got.CanonicalReportJSON)
			}
			description, err := os.ReadFile(state + ".description")
			if err != nil || string(description) != "CorrTest "+run.ShortToken {
				t.Fatalf("trap description %q: %v", description, err)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("trap check did not complete")
}

func TestTrapCheckRejectsMissingCredentialBeforeRun(t *testing.T) {
	r, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	target, err := r.CreateTarget(context.Background(), domain.TargetInput{DisplayName: "trap", BaseURL: "https://example.com/ext/mw", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORRTEST_SNMP_COMMUNITY", "")
	if _, err := r.StartTrapCheck(context.Background(), target.ID, TrapCheckOptions{Port: 162}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing secret error: %v", err)
	}
	runs, err := r.ListRuns(context.Background(), domain.RunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("created run before credential validation: %d, %v", len(runs), err)
	}
}

func TestEmbeddedWebhookFixtureMatchesSourceScenario(t *testing.T) {
	data, err := os.ReadFile("../../scenarios/reliability/flood-ordinary-webhook.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != ordinaryWebhookFixture {
		t.Fatal("embedded webhook fixture drifted from CLI scenario")
	}
	if _, err := scenario.Decode(strings.NewReader(ordinaryWebhookFixture)); err != nil {
		t.Fatal(fmt.Errorf("embedded fixture: %w", err))
	}
}
