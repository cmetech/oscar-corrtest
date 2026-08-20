package oscar_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestClientUsesExternalAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-API-Key"); got != "secret-value" {
			http.Error(w, `{"detail":"No API key provided"}`, http.StatusUnauthorized)
			return
		}
		if got := request.Header.Get("Authorization"); got != "" {
			http.Error(w, `{"detail":"Bearer is not accepted by public-v1"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"errors":[]}`))
	}))
	defer server.Close()

	client := newClient(t, server.URL)
	err := client.ValidateRule(context.Background(), compiler.RulePlan{
		Name: "corrtest-flood-p01-7q9k2m4a", Pattern: "flood", WindowSeconds: 30,
		MatchCriteria: map[string]any{"match": map[string]string{"alertname": "SOURCE"}, "min_count": 5},
	})
	if err != nil {
		t.Fatal(err)
	}
}

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
		if request.Header.Get("X-Api-Key") != "[REDACTED]" {
			t.Fatalf("API key not supplied/redacted: %v", request.Header)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("public-v1 must not send bearer authorization: %v", request.Header)
		}
	}
	for _, request := range requests {
		if strings.Contains(request.Path, "import") || request.Method == "PUT" {
			t.Fatalf("unsafe lifecycle request: %+v", request)
		}
	}
}

func TestInjectClassifiesResponseAndKeepsTransportFingerprintNonAuthoritative(t *testing.T) {
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
	if strings.Contains(request.Body, "oscar_fingerprint") || strings.Contains(request.Body, "am_fingerprint") {
		t.Fatalf("authoritative fingerprint label leaked into payload: %s", request.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["groupKey"]; !ok {
		t.Fatalf("missing Alertmanager group envelope: %v", body)
	}
	alerts := body["alerts"].([]any)
	if alerts[0].(map[string]any)["fingerPrint"] == "" {
		t.Fatalf("missing required Alertmanager transport fingerprint: %v", body)
	}
}

func TestInjectRecognizesCurrentOscarAsyncResponse(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"id":"11111111-1111-1111-1111-111111111111","status":"Alert group processing initiated in async mode"}`})
	result, err := newClient(t, server.URL()).Inject(context.Background(), compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A"}})
	if err != nil || result.Class != oscar.InjectionAccepted || result.TaskID == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestInjectClassifiesCurrentOscarTwoHundredBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want oscar.InjectionClass
	}{
		{name: "middleware request limiter", body: `{"status":"rate_limited","message":"Request rate limit exceeded. Processing may be delayed.","details":"Request accepted but rate-limited. No retries needed."}`, want: oscar.InjectionQueued},
		{name: "alert fingerprint limiter", body: `{"id":"11111111-1111-1111-1111-111111111111","status":"Alert rate limited (fingerprint: abc123def456...)"}`, want: oscar.InjectionRejected},
		{name: "circuit breaker queue", body: `{"status":"accepted","queued":true,"message":"Alerts accepted and queued for processing. System is at high load."}`, want: oscar.InjectionQueued},
		{name: "acl filtered", body: `{"status":"filtered","message":"All alerts filtered by ACL rules"}`, want: oscar.InjectionRejected},
		{name: "async accepted", body: `{"id":"11111111-1111-1111-1111-111111111111","status":"Alert group processing initiated in async mode"}`, want: oscar.InjectionAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := testoscar.New(t)
			server.Enqueue(testoscar.Response{Status: 200, Body: test.body})
			result, err := newClient(t, server.URL()).Inject(context.Background(), compiler.AlertPlan{
				Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A", "oscar_test_run_id": "crt_abc"},
			})
			if err != nil || result.Class != test.want {
				t.Fatalf("result=%+v err=%v want=%s", result, err, test.want)
			}
		})
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

func TestHistoryDecodesOscarAnnotationKey(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":1,"total_pages":1,"page":1,"per_page":100,"records":[{"id":"h1","alertname":"A","fingerprint":"server-fp","status":"firing","createdAt":"2026-08-20T00:00:01Z","labels":[{"Label":"oscar_test_run_id","Value":"crt_abc"}],"annotations":[{"Annotation":"summary","Value":"from OSCAR"}]}]}`})
	start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	records, err := newClient(t, server.URL()).FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "A", Start: start, End: start.Add(time.Minute)})
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	if got := records[0].Annotations["summary"]; got != "from OSCAR" {
		t.Fatalf("summary=%q annotations=%v", got, records[0].Annotations)
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

func TestLabelProbeRequiresUniqueHistoryAndEveryReservedLabel(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"status":"accepted","task_id":"probe-1"}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":1,"total_pages":1,"page":1,"per_page":100,"records":[{"ID":"11111111-1111-1111-1111-111111111111","alertname":"CORRTEST_PROBE_P00_SOURCE_7Q9K2M4A","fingerprint":"server-probe-fp","status":"firing","createdAt":"2026-08-20T00:00:01Z","labels":[{"Label":"alertname","Value":"CORRTEST_PROBE_P00_SOURCE_7Q9K2M4A"},{"Label":"oscar_test_run_id","Value":"crt_abc"}],"annotations":[]}]}`})
	client := newClient(t, server.URL())
	result, err := client.ProbeLabelSurvival(context.Background(), "crt_abc", "7Q9K2M4A")
	if err != nil || !result.Accepted || !result.HistoryFound || result.Fingerprint != "server-probe-fp" || len(result.MissingLabels) == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestNotificationAuditIsBoundedByServerFingerprint(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"items":[{"id":"n1","alert_fingerprint":"server-child-fp","notifier_type":"email","status":"suppressed","created_at":"2026-08-20T00:00:03Z","labels":{"oscar_test_run_id":"crt_abc"}}],"total":1,"page":1,"per_page":100}`})
	records, err := newClient(t, server.URL()).NotificationAudit(context.Background(), "server-child-fp", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 20, 0, 1, 0, 0, time.UTC))
	if err != nil || len(records) != 1 || records[0].NotifierType != "email" || records[0].Status != "suppressed" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	request := server.Requests()[0]
	if request.Path != "/api/v1/notification-audit/" || request.Query.Get("alert_fingerprint") != "server-child-fp" || request.Query.Get("per_page") != "100" {
		t.Fatalf("request=%+v", request)
	}
}

func TestListEvidenceAndOwnershipReadsAllDeclaredPages(t *testing.T) {
	t.Run("rules", func(t *testing.T) {
		server := testoscar.New(t)
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"rows":[{"id":1,"name":"other","pattern":"flood","description":"other"}],"total":2,"page":1,"perPage":1}`})
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"rows":[{"id":71,"name":"owned","pattern":"flood","description":"run=crt_abc"}],"total":2,"page":2,"perPage":1}`})
		rules, err := newClient(t, server.URL()).FindRules(context.Background(), "owned")
		if err != nil || len(rules) != 1 || rules[0].ID != 71 {
			t.Fatalf("rules=%+v err=%v", rules, err)
		}
		if requests := server.Requests(); len(requests) != 2 || requests[1].Query.Get("page") != "2" {
			t.Fatalf("requests=%+v", requests)
		}
	})

	t.Run("history", func(t *testing.T) {
		server := testoscar.New(t)
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":2,"total_pages":2,"page":1,"per_page":1,"records":[{"id":"h1","alertname":"A","fingerprint":"fp1","status":"firing","createdAt":"2026-08-20T00:00:01Z","labels":[],"annotations":[]}]}`})
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":2,"total_pages":2,"page":2,"per_page":1,"records":[{"id":"h2","alertname":"A","fingerprint":"fp2","status":"firing","createdAt":"2026-08-20T00:00:02Z","labels":[],"annotations":[]}]}`})
		start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
		records, err := newClient(t, server.URL()).FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "A", Start: start, End: start.Add(time.Minute)})
		if err != nil || len(records) != 2 {
			t.Fatalf("records=%+v err=%v", records, err)
		}
	})

	t.Run("audit", func(t *testing.T) {
		server := testoscar.New(t)
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"rows":[{"id":1,"alert_fingerprint":"fp","outcome":"enriched"}],"total":2,"page":1,"perPage":1}`})
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"rows":[{"id":2,"alert_fingerprint":"fp","outcome":"parent_emitted"}],"total":2,"page":2,"perPage":1}`})
		records, err := newClient(t, server.URL()).CorrelationAudit(context.Background(), "fp")
		if err != nil || len(records) != 2 {
			t.Fatalf("records=%+v err=%v", records, err)
		}
	})

	t.Run("notification", func(t *testing.T) {
		server := testoscar.New(t)
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"items":[{"id":"n1","alert_fingerprint":"fp","status":"queued"}],"total":2,"page":1,"per_page":1}`})
		server.Enqueue(testoscar.Response{Status: 200, Body: `{"items":[{"id":"n2","alert_fingerprint":"fp","status":"suppressed"}],"total":2,"page":2,"per_page":1}`})
		start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
		records, err := newClient(t, server.URL()).NotificationAudit(context.Background(), "fp", start, start.Add(time.Minute))
		if err != nil || len(records) != 2 {
			t.Fatalf("records=%+v err=%v", records, err)
		}
	})
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
