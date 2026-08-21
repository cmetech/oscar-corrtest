package oscar_test

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestBuildOperationPreviewShowsOrderedCredentialFreeLifecycle(t *testing.T) {
	plan := compilePreviewPlan(t, "parent_child")
	operations, err := oscar.BuildOperationPreview(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) < 4 {
		t.Fatalf("operations=%+v", operations)
	}
	wantStart := []string{"preflight.validate_rule", "preflight.inject_label_probe", "preflight.read_history"}
	for index, stage := range wantStart {
		if operations[index].Stage != stage {
			t.Fatalf("operation %d stage=%q want=%q", index, operations[index].Stage, stage)
		}
	}

	probe, err := oscar.BuildLabelProbeAlert(plan.RunID, plan.ShortToken)
	if err != nil {
		t.Fatal(err)
	}
	probeRequest, err := oscar.BuildAlertRequest(probe)
	if err != nil {
		t.Fatal(err)
	}
	wantProbeBody, err := oscar.CanonicalJSON(probeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if operations[1].Method != "POST" || operations[1].Path != "/api/v1/alerts" || operations[1].Body != string(wantProbeBody) {
		t.Fatalf("probe operation=%+v", operations[1])
	}

	for _, item := range plan.Cases {
		wantRuleJSON, err := oscar.CanonicalJSON(oscar.BuildRuleRequest(item.Rule, "test-version"))
		if err != nil {
			t.Fatal(err)
		}
		validate := findPreview(t, operations, "setup.validate_rule", item.Code, 0)
		create := findPreview(t, operations, "setup.create_rule", item.Code, 0)
		if validate.Method != "POST" || validate.Path != "/api/v1/correlation_rules/validate" || validate.Body != string(wantRuleJSON) {
			t.Fatalf("validate=%+v", validate)
		}
		if create.Method != "POST" || create.Path != "/api/v1/correlation_rules" || create.Body != string(wantRuleJSON) || len(create.RuntimeFields) != 0 {
			t.Fatalf("create=%+v", create)
		}
		for index, alert := range item.Alerts {
			operation := findPreview(t, operations, "stimulus.inject_alert", item.Code, index+1)
			want, err := oscar.BuildAlertRequest(alert)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, _ := oscar.CanonicalJSON(want)
			if operation.Method != "POST" || operation.Path != "/api/v1/alerts" || operation.Body != string(wantJSON) || operation.Attempt != index+1 || operation.ScheduledDelay != alert.Delay {
				t.Fatalf("alert operation=%+v", operation)
			}
		}
	}

	last := operations[len(operations)-1]
	if last.Stage != "cleanup.resolve_alert" {
		t.Fatalf("last operation=%+v", last)
	}
	if !containsStage(operations, "evidence.read_history") || !containsStage(operations, "evidence.read_correlation_audit") || !containsStage(operations, "evidence.read_notification_audit") || !containsStage(operations, "evidence.persist_final_transaction") {
		t.Fatalf("evidence lifecycle incomplete: %+v", operations)
	}
	if !containsStage(operations, "cleanup.delete_rule") {
		t.Fatalf("rule cleanup missing: %+v", operations)
	}

	for _, operation := range operations {
		wire := operation.Method + " " + operation.Path + " " + operation.Body
		for _, forbidden := range []string{"http://", "https://", "X-API-Key", "Authorization", "/71", "server-fingerprint\""} {
			if strings.Contains(wire, forbidden) {
				t.Fatalf("preview leaked target, credential, or invented identity in %q", wire)
			}
		}
	}
}

func TestPreviewEvidenceQueriesMatchLiveClientContract(t *testing.T) {
	plan := compilePreviewPlan(t, "parent_child")
	operations, err := oscar.BuildOperationPreview(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}

	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":0,"total_pages":1,"page":1,"per_page":100,"records":[]}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"rows":[],"total":0,"page":1,"perPage":100}`})
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"items":[],"total":0,"page":1,"per_page":100}`})
	client := newClient(t, server.URL())
	start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if _, err := client.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: plan.Cases[0].Alerts[0].Name, Start: start, End: start.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CorrelationAudit(context.Background(), "live-fingerprint"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NotificationAudit(context.Background(), "live-fingerprint", start, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	live := server.Requests()
	previewStages := []string{"evidence.read_history", "evidence.read_correlation_audit", "evidence.read_notification_audit"}
	for index, stage := range previewStages {
		operation := findPreview(t, operations, stage, "P01", 0)
		parsed, err := url.Parse(operation.Path)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Path != live[index].Path {
			t.Fatalf("stage=%s path=%q live=%q", stage, parsed.Path, live[index].Path)
		}
		if !reflect.DeepEqual(sortedQueryKeys(parsed.Query()), sortedQueryKeys(live[index].Query)) {
			t.Fatalf("stage=%s preview query=%v live query=%v", stage, parsed.Query(), live[index].Query)
		}
	}
	if operation := findPreview(t, operations, "evidence.read_correlation_audit", "P01", 0); operation.Path == "/api/v1/correlation-rule-audit" || !strings.Contains(operation.Path, "fingerprint=%7Bserver-fingerprint%7D") {
		t.Fatalf("correlation audit route or placeholder is wrong: %+v", operation)
	}
}

func TestPreviewResolutionBodyUsesAuthoritativeRuntimePlaceholder(t *testing.T) {
	plan := compilePreviewPlan(t, "flood")
	operations, err := oscar.BuildOperationPreview(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	operation := findPreview(t, operations, "cleanup.resolve_alert", "P01", 0)
	alert := plan.Cases[0].Alerts[0]
	want, err := oscar.BuildResolutionRequest(oscar.HistoryRecord{
		AlertName: alert.Name, Fingerprint: "{server-fingerprint}", Status: alert.Status,
		Labels: alert.Labels, Annotations: alert.Annotations,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := oscar.CanonicalJSON(want)
	if operation.Method != "POST" || operation.Path != "/api/v1/alerts" || operation.Body != string(wantJSON) || !reflect.DeepEqual(operation.RuntimeFields, []string{"server-fingerprint"}) {
		t.Fatalf("resolution=%+v wantBody=%s", operation, wantJSON)
	}
}

func TestOperationPreviewBuildsEveryBuiltinPlan(t *testing.T) {
	for _, document := range scenario.AllBuiltins() {
		t.Run(document.Pattern, func(t *testing.T) {
			operations, err := oscar.BuildOperationPreview(compilePreviewPlan(t, document.Pattern), "test-version")
			if err != nil || len(operations) == 0 {
				t.Fatalf("operations=%d err=%v", len(operations), err)
			}
		})
	}
}

func compilePreviewPlan(t *testing.T, pattern string) compiler.Plan {
	t.Helper()
	plan, err := compiler.Compile(
		domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"},
		scenario.Builtin(pattern), compiler.Capabilities{PipelineMode: "phase_b_dispatch"},
	)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func findPreview(t *testing.T, operations []oscar.OperationPreview, stage, caseCode string, attempt int) oscar.OperationPreview {
	t.Helper()
	for _, operation := range operations {
		if operation.Stage == stage && operation.CaseCode == caseCode && (attempt == 0 || operation.Attempt == attempt) {
			return operation
		}
	}
	t.Fatalf("preview stage=%q case=%q attempt=%d not found", stage, caseCode, attempt)
	return oscar.OperationPreview{}
}

func containsStage(operations []oscar.OperationPreview, stage string) bool {
	for _, operation := range operations {
		if operation.Stage == stage {
			return true
		}
	}
	return false
}

func sortedQueryKeys(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for left := range keys {
		for right := left + 1; right < len(keys); right++ {
			if keys[right] < keys[left] {
				keys[left], keys[right] = keys[right], keys[left]
			}
		}
	}
	return keys
}
