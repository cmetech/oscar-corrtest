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

func TestPreviewRecordsProposedOwnershipImmediatelyBeforeEachRuleMutation(t *testing.T) {
	operations, err := oscar.BuildOperationPreview(compilePreviewPlan(t, "flood"), "test-version")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"P01", "N01"} {
		validateIndex := findPreviewIndex(t, operations, "setup.validate_rule", code, 0)
		if validateIndex == 0 || validateIndex+1 >= len(operations) {
			t.Fatalf("validate index for %s=%d", code, validateIndex)
		}
		ownership := operations[validateIndex-1]
		create := operations[validateIndex+1]
		if ownership.Stage != "setup.record_proposed_ownership" || ownership.CaseCode != code || ownership.Method != "LOCAL" || ownership.Path != "" || ownership.Body != "" ||
			!strings.Contains(strings.ToLower(ownership.Summary), "proposed ownership is durably recorded before oscar mutation") {
			t.Fatalf("ownership before %s validation=%+v", code, ownership)
		}
		if create.Stage != "setup.create_rule" || create.CaseCode != code {
			t.Fatalf("operation after %s validation=%+v", code, create)
		}
	}
}

func TestPreviewCanonicalizesReversedSourceAndPlanToP01ThenN01(t *testing.T) {
	canonical := compilePreviewPlan(t, "flood")
	want, err := oscar.BuildOperationPreview(canonical, "test-version")
	if err != nil {
		t.Fatal(err)
	}

	source := scenario.Builtin("flood")
	source.Cases[0], source.Cases[1] = source.Cases[1], source.Cases[0]
	reversedSource, err := compiler.Compile(
		domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"},
		source, compiler.Capabilities{PipelineMode: "phase_b_dispatch"},
	)
	if err != nil {
		t.Fatal(err)
	}
	reversedPlan := canonical
	reversedPlan.Cases = append([]compiler.CasePlan(nil), canonical.Cases...)
	reversedPlan.Cases[0], reversedPlan.Cases[1] = reversedPlan.Cases[1], reversedPlan.Cases[0]

	for name, plan := range map[string]compiler.Plan{"reversed source": reversedSource, "reversed plan": reversedPlan} {
		t.Run(name, func(t *testing.T) {
			got, err := oscar.BuildOperationPreview(plan, "test-version")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("preview changed with input case ordering")
			}
			if !strings.Contains(got[0].Body, canonical.Cases[0].Rule.Name) {
				t.Fatalf("compatibility preflight did not use P01: %+v", got[0])
			}
			for _, stage := range []string{"setup.record_proposed_ownership", "setup.validate_rule", "setup.create_rule", "stimulus.inject_alert", "evidence.read_history", "cleanup.delete_rule", "cleanup.resolve_alert"} {
				codes := distinctStageCaseCodes(got, stage)
				if !reflect.DeepEqual(codes, []string{"P01", "N01"}) {
					t.Fatalf("stage=%s codes=%v", stage, codes)
				}
			}
		})
	}
}

func TestPreviewRejectsMissingOrDuplicateRequiredCaseCodes(t *testing.T) {
	base := compilePreviewPlan(t, "flood")
	tests := []struct {
		name  string
		cases []compiler.CasePlan
	}{
		{name: "missing N01", cases: []compiler.CasePlan{base.Cases[0]}},
		{name: "duplicate P01", cases: []compiler.CasePlan{base.Cases[0], base.Cases[0], base.Cases[1]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			plan.Cases = test.cases
			if _, err := oscar.BuildOperationPreview(plan, "test-version"); err == nil {
				t.Fatalf("invalid case codes accepted: %+v", test.cases)
			}
		})
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

func TestPreviewEvidenceReadsOncePerDistinctExpectedSourceIdentity(t *testing.T) {
	t.Run("persistence firing and resolved share one audit identity", func(t *testing.T) {
		plan := compilePreviewPlan(t, "persistence")
		operations, err := oscar.BuildOperationPreview(plan, "test-version")
		if err != nil {
			t.Fatal(err)
		}
		n01 := plan.Cases[1]
		if len(n01.Alerts) != 2 || n01.Alerts[0].Labels["oscar_test_event_id"] != n01.Alerts[1].Labels["oscar_test_event_id"] {
			t.Fatalf("fixture does not reuse identity: %+v", n01.Alerts)
		}
		audits := previewsFor(operations, "evidence.read_correlation_audit", "N01")
		if len(audits) != 1 || !strings.Contains(audits[0].Summary, n01.Alerts[0].Name) || !strings.Contains(audits[0].Summary, n01.Alerts[0].Labels["oscar_test_event_id"]) {
			t.Fatalf("N01 audit previews=%+v", audits)
		}
	})

	t.Run("parent child audits and notifications follow distinct identity order", func(t *testing.T) {
		plan := compilePreviewPlan(t, "parent_child")
		operations, err := oscar.BuildOperationPreview(plan, "test-version")
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range plan.Cases {
			audits := previewsFor(operations, "evidence.read_correlation_audit", item.Code)
			notifications := previewsFor(operations, "evidence.read_notification_audit", item.Code)
			if len(audits) != len(item.Alerts) || len(notifications) != len(item.Alerts) {
				t.Fatalf("case=%s audits=%d notifications=%d alerts=%d", item.Code, len(audits), len(notifications), len(item.Alerts))
			}
			for index, alert := range item.Alerts {
				for _, operation := range []oscar.OperationPreview{audits[index], notifications[index]} {
					if !strings.Contains(operation.Summary, alert.Name) || !strings.Contains(operation.Summary, alert.Labels["oscar_test_event_id"]) {
						t.Fatalf("case=%s identity %d operation=%+v", item.Code, index, operation)
					}
				}
			}
		}
	})
}

func TestPreviewKeepsAttemptOrderOnlyInOperationMetadata(t *testing.T) {
	plan := compilePreviewPlan(t, "persistence")
	operations, err := oscar.BuildOperationPreview(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	stimulus := findPreview(t, operations, "stimulus.inject_alert", "N01", 2)
	if stimulus.Attempt != 2 || stimulus.ScheduledDelay != 10*time.Second {
		t.Fatalf("stimulus metadata=%+v", stimulus)
	}
	for _, operation := range operations {
		if strings.Contains(operation.Body, "oscar_test_attempt_index") || strings.Contains(operation.Body, `"delay"`) {
			t.Fatalf("attempt or delay leaked into %s OSCAR body: %s", operation.Stage, operation.Body)
		}
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
	return operations[findPreviewIndex(t, operations, stage, caseCode, attempt)]
}

func findPreviewIndex(t *testing.T, operations []oscar.OperationPreview, stage, caseCode string, attempt int) int {
	t.Helper()
	for index, operation := range operations {
		if operation.Stage == stage && operation.CaseCode == caseCode && (attempt == 0 || operation.Attempt == attempt) {
			return index
		}
	}
	t.Fatalf("preview stage=%q case=%q attempt=%d not found", stage, caseCode, attempt)
	return -1
}

func previewsFor(operations []oscar.OperationPreview, stage, caseCode string) []oscar.OperationPreview {
	var result []oscar.OperationPreview
	for _, operation := range operations {
		if operation.Stage == stage && operation.CaseCode == caseCode {
			result = append(result, operation)
		}
	}
	return result
}

func distinctStageCaseCodes(operations []oscar.OperationPreview, stage string) []string {
	var result []string
	for _, operation := range operations {
		if operation.Stage == stage && (len(result) == 0 || result[len(result)-1] != operation.CaseCode) {
			result = append(result, operation.CaseCode)
		}
	}
	return result
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
