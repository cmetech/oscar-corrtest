package oscar_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestLiveClientUsesPublicRequestBuilders(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"valid":true,"errors":[]}`})
	server.Enqueue(testoscar.Response{Status: 201, Body: `{"id":71}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"status":"accepted","task_id":"job-1"}`})

	client, err := oscar.New(domain.Target{BaseURL: server.URL(), APIProfile: "public-v1"}, oscar.Options{HarnessVersion: "test-version", Getenv: func(string) string { return "secret" }})
	if err != nil {
		t.Fatal(err)
	}
	rule := compiler.RulePlan{
		Name: "corrtest-flood-p01-preview1", Pattern: "flood", WindowSeconds: 30,
		GroupBy: []string{"site"}, MatchCriteria: map[string]any{"min_count": 5},
		EmitAlertName: "CORRTEST_FLOOD_P01_SYNTHETIC_PREVIEW1", EmitLabels: map[string]string{"oscar_test_run_id": "crt_preview"},
	}
	wantRule := oscar.BuildRuleRequest(rule, "test-version")
	if err := client.ValidateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}

	alert := compiler.AlertPlan{
		Name: "CORRTEST_FLOOD_P01_SOURCE_PREVIEW1", Status: "firing",
		Labels:      map[string]string{"alertname": "wrong-name", "oscar_test_run_id": "crt_preview"},
		Annotations: map[string]string{"summary": "test"},
	}
	wantAlert, err := oscar.BuildAlertRequest(alert)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Inject(context.Background(), alert); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()
	if len(requests) != 3 {
		t.Fatalf("requests=%d want=3", len(requests))
	}
	assertJSONValueEqual(t, requests[0].Body, wantRule)
	assertJSONValueEqual(t, requests[1].Body, wantRule)
	assertJSONValueEqual(t, requests[2].Body, wantAlert)
}

func TestBuildAlertRequestPinsEnvelopeAndDoesNotMutatePlan(t *testing.T) {
	labels := map[string]string{"alertname": "wrong", "oscar_test_run_id": "crt_abc"}
	annotations := map[string]string{"summary": "test"}
	request, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: "A", Status: "firing", Labels: labels, Annotations: annotations})
	if err != nil {
		t.Fatal(err)
	}
	if request.Receiver != "oscar-corrtest" || request.Status != "firing" || request.GroupKey != "crt_abc:A" ||
		request.GroupLabels["alertname"] != "A" || request.CommonLabels["alertname"] != "A" || len(request.Alerts) != 1 ||
		request.Alerts[0].Fingerprint != "0508217108a09216" || request.Alerts[0].Labels["alertname"] != "A" {
		t.Fatalf("request=%+v", request)
	}
	request.CommonLabels["new"] = "value"
	request.CommonAnnotations["new"] = "value"
	if labels["alertname"] != "wrong" || labels["new"] != "" || annotations["new"] != "" || request.Alerts[0].Labels["new"] != "" || request.Alerts[0].Annotations["new"] != "" {
		t.Fatalf("request maps alias compiler plan maps: labels=%v annotations=%v request=%+v", labels, annotations, request)
	}
	for _, invalid := range []compiler.AlertPlan{{Name: "A"}, {Status: "firing"}} {
		if _, err := oscar.BuildAlertRequest(invalid); err == nil {
			t.Fatalf("invalid alert accepted: %+v", invalid)
		}
	}
	if _, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: "A", Status: "firing"}); err == nil {
		t.Fatal("alert without compiler-supplied labels was accepted")
	}
}

func TestPublicRequestBuildersPinCurrentWireShape(t *testing.T) {
	rule := compiler.RulePlan{
		Name: "corrtest-flood-p01-preview1", Pattern: "flood", WindowSeconds: 30,
		GroupBy: []string{"site"}, MatchCriteria: map[string]any{"min_count": 5},
		EmitAlertName: "PARENT", EmitLabels: map[string]string{"oscar_test_run_id": "crt_abc"}, Description: "owned",
	}
	encoded, err := oscar.CanonicalJSON(oscar.BuildRuleRequest(rule, "test-version"))
	if err != nil {
		t.Fatal(err)
	}
	wantRule := `{"name":"corrtest-flood-p01-preview1","pattern":"flood","window_seconds":30,"group_by_labels":["site"],"match_criteria":{"min_count":5},"priority":100,"max_synthetic_per_minute":10,"enabled":true,"description":"owned","created_by":"oscar-corrtest/test-version","emit_spec":{"alertname":"PARENT","labels":{"oscar_test_run_id":"crt_abc"}}}`
	if string(encoded) != wantRule {
		t.Fatalf("rule JSON=%s", encoded)
	}

	record := oscar.HistoryRecord{AlertName: "A", Fingerprint: "server-fingerprint", Labels: map[string]string{"alertname": "A", "oscar_test_run_id": "crt_abc"}}
	resolution, err := oscar.BuildResolutionRequest(record)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != "resolved" || resolution.GroupKey != "crt_abc:cleanup:A" || resolution.CommonLabels["oscar_fingerprint"] != "server-fingerprint" ||
		resolution.CommonAnnotations["oscar_test_cleanup"] != "resolved by oscar-corrtest after exact history read-back" || len(resolution.Alerts) != 1 || resolution.Alerts[0].Fingerprint != "fedf414208e60c28" {
		t.Fatalf("resolution=%+v", resolution)
	}

	probe, err := oscar.BuildLabelProbeAlert("crt_abc", "7q9k2m4a")
	if err != nil {
		t.Fatal(err)
	}
	wantProbeLabels := map[string]string{
		"alertname": "CORRTEST_PROBE_P00_SOURCE_7Q9K2M4A", "category": "corrtest_probe", "oscar_test": "true", "oscar_test_harness": "corrtest",
		"oscar_test_schema_version": "v1", "oscar_test_run_id": "crt_abc", "oscar_test_run_short": "7Q9K2M4A",
		"oscar_test_suite": "diagnostic", "oscar_test_scenario": "label-survival", "oscar_test_pattern": "probe",
		"oscar_test_case": "label-survival", "oscar_test_case_code": "P00", "oscar_test_polarity": "diagnostic",
		"oscar_test_alert_class": "source", "oscar_test_alert_role": "probe", "oscar_test_rule_name": "none", "severity": "warning",
	}
	if probe.Name != "CORRTEST_PROBE_P00_SOURCE_7Q9K2M4A" || probe.Status != "firing" || !reflect.DeepEqual(probe.Labels, wantProbeLabels) || probe.Annotations["summary"] != "[CORRTEST][PROBE] reserved label survival" {
		t.Fatalf("probe=%+v", probe)
	}
}

func TestLiveResolutionUsesPublicRequestBuilder(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"status":"accepted","task_id":"cleanup-1"}`})
	client := newClient(t, server.URL())
	record := oscar.HistoryRecord{
		AlertName: "CORRTEST_FLOOD_P01_SOURCE_7Q9K2M4A", Fingerprint: "server-fingerprint", Status: "firing",
		Labels:      map[string]string{"alertname": "CORRTEST_FLOOD_P01_SOURCE_7Q9K2M4A", "oscar_test_run_id": "crt_abc"},
		Annotations: map[string]string{"summary": "source"},
	}
	want, err := oscar.BuildResolutionRequest(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResolveHistory(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	assertJSONValueEqual(t, server.Requests()[0].Body, want)
}

func TestLiveProbeUsesPublicRequestBuilder(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"status":"accepted","task_id":"probe-1"}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":1,"total_pages":1,"page":1,"per_page":100,"records":[{"id":"h1","alertname":"CORRTEST_PROBE_P00_SOURCE_7Q9K2M4A","fingerprint":"server-probe-fp","status":"firing","createdAt":"2026-08-20T00:00:01Z","labels":[{"Label":"oscar_test_run_id","Value":"crt_abc"}],"annotations":[]}]}`})
	client := newClient(t, server.URL())
	probe, err := oscar.BuildLabelProbeAlert("crt_abc", "7q9k2m4a")
	if err != nil {
		t.Fatal(err)
	}
	want, err := oscar.BuildAlertRequest(probe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ProbeLabelSurvival(context.Background(), "crt_abc", "7q9k2m4a"); err != nil {
		t.Fatal(err)
	}
	requests := server.Requests()
	if len(requests) != 2 || requests[1].Path != "/api/v1/alerts/history" || !strings.Contains(requests[1].Query.Get("filter"), probe.Name) {
		t.Fatalf("probe requests=%+v", requests)
	}
	assertJSONValueEqual(t, requests[0].Body, want)
}

func assertJSONValueEqual(t *testing.T, body string, want any) {
	t.Helper()
	var got any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode recorded body: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var normalizedWant any
	if err := json.Unmarshal(wantJSON, &normalizedWant); err != nil {
		t.Fatal(err)
	}
	if !valuesEqual(got, normalizedWant) {
		t.Fatalf("body=%s want=%s", body, wantJSON)
	}
}

func valuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
