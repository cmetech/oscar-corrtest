package oscar_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestPublicV1RuleLifecycleUsesCreateReadDeleteOnly(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"valid":true,"errors":[]}`})
	server.Enqueue(testoscar.Response{Status: 201, Body: `{"id":71,"name":"corrtest-flood-p01-7q9k2m4a","pattern":"flood","window_seconds":30,"match_criteria":{"alertname":"SOURCE","min_count":5},"priority":100,"max_synthetic_per_minute":10,"enabled":true,"description":"run=crt_abc","created_by":"oscar-corrtest/test"}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"id":71,"name":"corrtest-flood-p01-7q9k2m4a","pattern":"flood","window_seconds":30,"match_criteria":{"alertname":"SOURCE","min_count":5},"priority":100,"max_synthetic_per_minute":10,"enabled":true,"description":"run=crt_abc","created_by":"oscar-corrtest/test"}`})
	server.Enqueue(testoscar.Response{Status: 204})
	client := newClient(t, server.URL())
	rule := compiler.RulePlan{Name: "corrtest-flood-p01-7q9k2m4a", Pattern: "flood", WindowSeconds: 30,
		MatchCriteria: map[string]any{"alertname": "SOURCE", "min_count": 5}, EmitAlertName: "PARENT",
		EmitLabels: map[string]string{"oscar_test_run_id": "crt_abc"}, Description: "run=crt_abc"}
	if err := client.ValidateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateRule(context.Background(), rule)
	if err != nil || created.ID != 71 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	read, err := client.GetRule(context.Background(), 71)
	if err != nil || read.Name != rule.Name {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	if err := client.DeleteRule(context.Background(), 71); err != nil {
		t.Fatal(err)
	}
	requests := server.Requests()
	want := []string{"POST /api/v1/correlation_rules/validate", "POST /api/v1/correlation_rules", "GET /api/v1/correlation_rules/71", "DELETE /api/v1/correlation_rules/71"}
	for index, request := range requests {
		got := request.Method + " " + request.Path
		if got != want[index] {
			t.Fatalf("request %d=%q want %q", index, got, want[index])
		}
		if request.Header.Get("Authorization") != "[REDACTED]" {
			t.Fatalf("authorization not supplied/redacted: %v", request.Header)
		}
	}
	for _, request := range requests {
		if strings.Contains(request.Path, "import") || request.Method == "PUT" {
			t.Fatalf("unsafe lifecycle request: %+v", request)
		}
	}
}

func TestInjectClassifiesResponseAndNeverSuppliesFingerprint(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"status":"accepted","task_id":"job-1"}`})
	client := newClient(t, server.URL())
	result, err := client.Inject(context.Background(), compiler.AlertPlan{Name: "CORRTEST_FLOOD_P01_SOURCE_7Q9K2M4A", Status: "firing",
		Labels: map[string]string{"alertname": "CORRTEST_FLOOD_P01_SOURCE_7Q9K2M4A", "oscar_test_run_id": "crt_abc"}, Annotations: map[string]string{"summary": "test"}})
	if err != nil || result.Class != oscar.InjectionAccepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	request := server.Requests()[0]
	if request.Path != "/api/v1/alerts" {
		t.Fatalf("path=%q", request.Path)
	}
	if strings.Contains(strings.ToLower(request.Body), "fingerprint") || strings.Contains(request.Body, "am_fingerprint") {
		t.Fatalf("client fingerprint leaked into payload: %s", request.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["groupKey"]; !ok {
		t.Fatalf("missing Alertmanager group envelope: %v", body)
	}
}

func TestHistoryProvidesServerFingerprintAndAuditUsesIt(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":1,"total_pages":1,"page":1,"per_page":25,"records":[{"id":"h1","alertname":"CORRTEST_FLOOD_P01_SOURCE_7Q9K2M4A","fingerprint":"server-fp","status":"firing","createdAt":"2026-08-20T00:00:01Z","labels":[{"Label":"oscar_test_run_id","Value":"crt_abc"}],"annotations":[]}]}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"rows":[{"id":9,"created_at":"2026-08-20T00:00:02Z","alert_fingerprint":"server-fp","rule_id":71,"rule_name":"corrtest-flood-p01-7q9k2m4a","pattern":"flood","outcome":"parent_emitted"}],"total":1,"page":1,"perPage":25}`})
	client := newClient(t, server.URL())
	start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	records, err := client.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "CORRTEST_FLOOD_P01_SOURCE_7Q9K2M4A", Start: start, End: start.Add(time.Minute)})
	if err != nil || len(records) != 1 || records[0].Fingerprint != "server-fp" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	audit, err := client.CorrelationAudit(context.Background(), records[0].Fingerprint)
	if err != nil || len(audit) != 1 || audit[0].Outcome != "parent_emitted" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
	if got := server.Requests()[1].Query.Get("fingerprint"); got != "server-fp" {
		t.Fatalf("audit fingerprint=%q", got)
	}
}

func TestUnknownSuccessfulInjectionIsIndeterminate(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"message":"queued somewhere"}`})
	result, err := newClient(t, server.URL()).Inject(context.Background(), compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A"}})
	if err != nil || result.Class != oscar.InjectionIndeterminate {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func newClient(t *testing.T, baseURL string) *oscar.Client {
	t.Helper()
	client, err := oscar.New(domain.Target{BaseURL: baseURL, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OSCAR_KEY"}},
		oscar.Options{Getenv: func(name string) string { return map[string]string{"OSCAR_KEY": "secret-value"}[name] }, HarnessVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
