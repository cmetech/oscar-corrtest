package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

// Test-only Flood/sequence/co-occurrence wire responder. The existing independent Model determines
// matched trigger decisions. Captured JSON supplies episode History state;
// neither branch reads assertions, expected verdicts or case polarity.
type correlationWireFixture struct {
	mu             sync.Mutex
	model          *testoscar.Model
	mode           string
	rules          map[int]compiler.RulePlan
	history        map[string]oscar.HistoryRecord
	auditToHistory map[string]string
	auditToName    map[string]string
	auditIDs       map[string]int
	stimuli        []oscar.AlertRequest
	cleanup        []oscar.AlertRequest
	deleted        map[int]bool
}

func newCorrelationWireFixture(mode string) *correlationWireFixture {
	return &correlationWireFixture{model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 123456789, time.UTC)), mode: mode, rules: map[int]compiler.RulePlan{}, history: map[string]oscar.HistoryRecord{}, auditToHistory: map[string]string{}, auditToName: map[string]string{}, auditIDs: map[string]int{}, deleted: map[int]bool{}}
}
func wireJSON(value any) testoscar.Response {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return testoscar.Response{Body: string(raw)}
}
func (f *correlationWireFixture) respond(request testoscar.Request) testoscar.Response {
	f.mu.Lock()
	defer f.mu.Unlock()
	bad := func(err error) testoscar.Response {
		return testoscar.Response{Status: 500, Body: fmt.Sprintf(`{"detail":%q}`, err.Error())}
	}
	if request.Header.Get("X-Api-Key") != "[REDACTED]" {
		return bad(fmt.Errorf("missing API key"))
	}
	ctx := context.Background()
	if request.Method == http.MethodPost && strings.HasPrefix(request.Path, "/api/v1/correlation_rules") {
		var body oscar.RuleRequest
		if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
			return bad(err)
		}
		rule := compiler.RulePlan{Name: body.Name, Pattern: body.Pattern, Description: body.Description, WindowSeconds: body.WindowSeconds, GroupBy: body.GroupByLabels, MatchCriteria: body.MatchCriteria}
		if body.EmitSpec != nil {
			rule.EmitAlertName = body.EmitSpec.AlertName
			rule.EmitLabels = body.EmitSpec.Labels
		}
		if rule.Pattern != "flood" && rule.Pattern != "sequence" && rule.Pattern != "co_occurrence" {
			return bad(fmt.Errorf("fixture supports Flood, sequence and co-occurrence only"))
		}
		if request.Path == "/api/v1/correlation_rules/validate" {
			if err := f.model.ValidateRule(ctx, rule); err != nil {
				return bad(err)
			}
			return wireJSON(map[string]any{"valid": true, "errors": []any{}})
		}
		created, err := f.model.CreateRule(ctx, rule)
		if err != nil {
			return bad(err)
		}
		f.rules[created.ID] = rule
		return wireJSON(map[string]any{"id": created.ID, "name": created.Name, "pattern": created.Pattern, "description": created.Description})
	}
	if request.Method == http.MethodDelete && strings.HasPrefix(request.Path, "/api/v1/correlation_rules/") {
		id, err := strconv.Atoi(strings.TrimPrefix(request.Path, "/api/v1/correlation_rules/"))
		if err != nil {
			return bad(err)
		}
		if _, ok := f.rules[id]; !ok {
			return bad(fmt.Errorf("unowned delete"))
		}
		f.deleted[id] = true
		if err := f.model.DeleteRule(ctx, id); err != nil {
			return bad(err)
		}
		return testoscar.Response{Status: 204}
	}
	if request.Method == http.MethodPost && request.Path == "/api/v1/alerts" {
		var body oscar.AlertGroupRequest
		if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
			return bad(err)
		}
		if len(body.Alerts) != 1 {
			return bad(fmt.Errorf("expected one stimulus"))
		}
		alert := body.Alerts[0]
		if alert.Annotations["oscar_test_cleanup"] != "" {
			f.cleanup = append(f.cleanup, alert)
			return wireJSON(map[string]string{"status": "accepted"})
		}
		f.stimuli = append(f.stimuli, alert)
		if f.mode == "old refire indeterminate" && len(f.stimuli) == 8 {
			return wireJSON(map[string]string{"status": "unknown"})
		}
		if (f.mode == "missing first history" && len(f.stimuli) == 1) || (f.mode == "old refire history" && len(f.stimuli) == 8) {
			return testoscar.Response{Status: 503, Body: `{"detail":"outcome uncertain"}`}
		}
		starts := f.model.Now().Truncate(time.Millisecond)
		if alert.StartsAt != "" {
			var err error
			starts, err = time.Parse(time.RFC3339Nano, alert.StartsAt)
			if err != nil {
				return bad(err)
			}
		}
		input := compiler.AlertPlan{Name: alert.Labels["alertname"], Status: alert.Status, Labels: alert.Labels, Annotations: alert.Annotations, StartsAt: starts}
		if _, err := f.model.Inject(ctx, input); err != nil {
			return bad(err)
		}
		rows, err := f.model.FindHistory(ctx, oscar.HistoryQuery{AlertName: input.Name})
		if err != nil || len(rows) == 0 {
			return bad(fmt.Errorf("model History unavailable: %v", err))
		}
		row := rows[len(rows)-1]
		row.StartsAt = starts
		row.Labels["am_fingerprint"] = alert.Fingerprint
		f.history[row.Fingerprint+"/"+starts.Format(time.RFC3339Nano)] = row
		f.auditToHistory[alert.Fingerprint], f.auditToName[alert.Fingerprint] = row.Fingerprint, row.AlertName
		if ((f.mode == "stored first 503" || f.mode == "ambiguous first identity" || f.mode == "wrong first labels") && len(f.stimuli) == 1) || (f.mode == "stored refire 503" && len(f.stimuli) == 8) {
			return testoscar.Response{Status: 503, Body: `{"detail":"stored but response failed"}`}
		}
		return wireJSON(map[string]string{"status": "accepted", "task_id": "wire-task"})
	}
	if request.Method == http.MethodGet && request.Path == "/api/v1/alerts/history" {
		var filter struct {
			Items []struct{ Field, Operator, Value string }
		}
		if err := json.Unmarshal([]byte(request.Query.Get("filter")), &filter); err != nil {
			return bad(err)
		}
		if len(filter.Items) != 1 || filter.Items[0].Field != "alertname" || filter.Items[0].Operator != "equals" {
			return bad(fmt.Errorf("expected exact History filter"))
		}
		var records []oscar.HistoryRecord
		for _, row := range f.history {
			if row.AlertName == filter.Items[0].Value {
				records = append(records, row)
			}
		}
		// Current episodes arrive first; old rows cannot overwrite cleanup identity.
		sort.Slice(records, func(i, j int) bool { return records[i].StartsAt.After(records[j].StartsAt) })
		wire := make([]map[string]any, 0, len(records))
		for _, row := range records {
			labels := []map[string]string{}
			for key, value := range row.Labels {
				if key != "alertname" {
					labels = append(labels, map[string]string{"Label": key, "Value": value})
				}
			}
			record := map[string]any{"id": row.ID, "alertname": row.AlertName, "fingerprint": row.Fingerprint, "status": row.Status, "startsAt": row.StartsAt.Format(time.RFC3339Nano), "createdAt": row.CreatedAt.Format(time.RFC3339Nano), "labels": labels, "annotations": []any{}}
			if f.mode == "sequence ignored recovery" || f.mode == "coocc ignored recovery" {
				record["status"] = "firing"
			}
			if f.mode == "missing startsAt" {
				delete(record, "startsAt")
			}
			if f.mode == "wrong first labels" {
				for _, label := range labels {
					if label["Label"] == "site" {
						label["Value"] = "foreign-site"
					}
				}
			}
			wire = append(wire, record)
			if f.mode == "ambiguous first identity" {
				other := map[string]any{}
				for key, value := range record {
					other[key] = value
				}
				other["fingerprint"] = "foreign-fingerprint"
				wire = append(wire, other)
			}
		}
		return wireJSON(map[string]any{"total_records": len(wire), "total_pages": 1, "page": 1, "per_page": 100, "records": wire})
	}
	if request.Method == http.MethodGet && request.Path == "/api/v1/correlation_rules/audit" {
		key := request.Query.Get("fingerprint")
		historyFP, found := f.auditToHistory[key]
		if !found || len(key) != 16 || key == historyFP {
			return bad(fmt.Errorf("audit query did not use am_fingerprint"))
		}
		matched := false
		var matchedRule compiler.RulePlan
		for _, rule := range f.rules {
			clause, _ := rule.MatchCriteria["match"].(map[string]any)
			if clause["alertname"] == f.auditToName[key] {
				matched = true
			}
			if rule.Pattern == "sequence" || rule.Pattern == "co_occurrence" {
				criteriaKey := "sequence"
				if rule.Pattern == "co_occurrence" {
					criteriaKey = "required_matches"
				}
				clauses, _ := rule.MatchCriteria[criteriaKey].([]any)
				for _, raw := range clauses {
					clause, _ := raw.(map[string]any)
					if clause["alertname"] == f.auditToName[key] {
						matched = true
						matchedRule = rule
					}
				}
			}
		}
		if !matched {
			if f.mode == "audit 503" {
				return testoscar.Response{Status: 503, Body: `{"detail":"audit unavailable"}`}
			}
			row := map[string]any{"id": 0, "created_at": f.model.Now().Format(time.RFC3339Nano), "alert_fingerprint": key, "rule_id": nil, "rule_name": nil, "pattern": nil, "outcome": "released_no_trigger", "reason": "no_matching_rules"}
			// Stable IDs are assigned once per authoritative source identity.
			if f.auditIDs[key] == 0 {
				f.auditIDs[key] = len(f.auditIDs) + 1
			}
			row["id"] = f.auditIDs[key]
			if f.mode == "missing null identity" {
				delete(row, "pattern")
			}
			if f.mode == "wrong audit fingerprint" {
				row["alert_fingerprint"] = historyFP
			}
			return wireJSON(map[string]any{"rows": []any{row}, "total": 1, "perPage": 100})
		}
		audits, err := f.model.CorrelationAudit(ctx, historyFP)
		if err != nil {
			return bad(err)
		}
		wire := make([]map[string]any, 0, len(audits))
		for _, row := range audits {
			if (row.Pattern == "sequence" || row.Pattern == "co_occurrence") && row.Outcome == "no_trigger" {
				row.Outcome = "enriched"
			}
			if f.mode == "coocc premature trigger" && row.Pattern == "co_occurrence" {
				row.Outcome = "parent_emitted"
			}
			if f.mode == "sequence reversed trigger" && matchedRule.Pattern == "sequence" {
				clauses := matchedRule.MatchCriteria["sequence"].([]any)
				first := clauses[0].(map[string]any)["alertname"]
				second := clauses[1].(map[string]any)["alertname"]
				var ordered []oscar.AlertRequest
				for _, input := range f.stimuli {
					if input.Status == "firing" && input.Labels["oscar_test_rule_name"] == matchedRule.Name {
						ordered = append(ordered, input)
					}
				}
				if len(ordered) == 2 && ordered[0].Labels["alertname"] == second && ordered[1].Labels["alertname"] == first && f.auditToName[key] == first {
					row.Outcome = "parent_emitted"
				}
			}
			wire = append(wire, map[string]any{"id": row.ID, "created_at": row.CreatedAt.Format(time.RFC3339Nano), "alert_fingerprint": key, "rule_id": row.RuleID, "rule_name": row.RuleName, "pattern": row.Pattern, "outcome": row.Outcome, "reason": row.Reason})
		}
		return wireJSON(map[string]any{"rows": wire, "total": len(wire), "perPage": 100})
	}
	return bad(fmt.Errorf("unexpected method/path %s %s", request.Method, request.Path))
}

// Discard a completed first input response to model a transport failure after
// server acceptance, without changing the real client's serialization/decoding.
type lostAlertResponse struct {
	base    http.RoundTripper
	dropped bool
}

func (t *lostAlertResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil && !t.dropped && request.Method == http.MethodPost && request.URL.Path == "/api/v1/alerts" {
		t.dropped = true
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, fmt.Errorf("test transport lost response after acceptance")
	}
	return response, err
}

func TestFloodFixturesThroughRealHTTPClient(t *testing.T) {
	for _, test := range []struct {
		fixture, mode string
		want          domain.Verdict
	}{
		{"flood-lifecycle-downstream.yaml", "valid", domain.VerdictPass},
		{"flood-lifecycle-downstream.yaml", "stored first 503", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "stored first transport", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "ambiguous first identity", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "wrong first labels", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "old refire indeterminate", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "missing first history", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "stored refire 503", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "old refire history", domain.VerdictError},
		{"flood-lifecycle-downstream.yaml", "missing startsAt", domain.VerdictInconclusive},
		{"flood-downstream-no-match.yaml", "valid", domain.VerdictPass},
		{"flood-downstream-no-match.yaml", "missing null identity", domain.VerdictInconclusive},
		{"flood-downstream-no-match.yaml", "wrong audit fingerprint", domain.VerdictInconclusive},
		{"flood-downstream-no-match.yaml", "audit 503", domain.VerdictError},
	} {
		t.Run(test.fixture+"/"+test.mode, func(t *testing.T) {
			raw, err := os.ReadFile("../../scenarios/reliability/" + test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			document, err := scenario.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t)
			run := newRun()
			if err := db.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, document, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			fixture := newCorrelationWireFixture(test.mode)
			server := testoscar.NewWithResponder(t, fixture.respond)
			options := oscar.Options{Getenv: func(string) string { return "local-test-key" }}
			if test.mode == "stored first transport" {
				options.HTTPClient = &http.Client{Transport: &lostAlertResponse{base: http.DefaultTransport}}
			}
			client, err := oscar.New(domain.Target{BaseURL: server.URL(), APIProfile: "public-v1"}, options)
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(db, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if (err != nil) != (test.want == domain.VerdictError) {
				t.Fatalf("error=%v", err)
			}
			stored, _ := db.GetRun(context.Background(), run.ID)
			if stored.Verdict != test.want {
				t.Fatalf("verdict=%s want=%s report=%s", stored.Verdict, test.want, stored.CanonicalReportJSON)
			}
			var report struct {
				Coverage    map[string]string
				Checkpoints []struct{ Verdict string }
				Attempts    []domain.AlertAttemptFact
			}
			if err := json.Unmarshal(stored.CanonicalReportJSON, &report); err != nil {
				t.Fatal(err)
			}
			if report.Coverage["physicalClear"] != "NOT_PROVEN" || report.Coverage["receiverAdmission"] != "NOT_TESTED" {
				t.Fatal("wire integration overclaims coverage")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			wantCleanup := domain.CleanupClean
			if test.mode == "ambiguous first identity" || test.mode == "wrong first labels" || test.mode == "missing first history" || (test.mode == "old refire history" || test.mode == "old refire indeterminate") {
				wantCleanup = domain.CleanupDirty
			}
			if len(fixture.deleted) != 2 || (len(fixture.cleanup) == 0 && wantCleanup == domain.CleanupClean) || stored.CleanupStatus != wantCleanup {
				t.Fatalf("cleanup incomplete: deletes=%d clear-requests=%d status=%s", len(fixture.deleted), len(fixture.cleanup), stored.CleanupStatus)
			}
			if test.want == domain.VerdictError && test.mode != "audit 503" {
				wantInputs := 1
				if strings.Contains(test.mode, "refire") {
					wantInputs = 8
				}
				if len(fixture.stimuli) != wantInputs || len(report.Attempts) != wantInputs {
					t.Fatalf("uncertain injection lost or retried: stimuli=%d attempts=%d", len(fixture.stimuli), len(report.Attempts))
				}
				last := report.Attempts[len(report.Attempts)-1]
				wantStatus := 503
				if test.mode == "stored first transport" {
					wantStatus = 0
				}
				if test.mode == "old refire indeterminate" {
					wantStatus = 200
				}
				if last.StatusCode != wantStatus || last.InjectionClass != "indeterminate" || last.SendState != "ATTEMPTED" || last.StartsAt.IsZero() {
					t.Fatalf("uncertain attempt=%+v", last)
				}
				if test.mode == "old refire history" || test.mode == "old refire indeterminate" {
					for _, clean := range fixture.cleanup {
						if clean.Labels["oscar_test_event_id"] == fixture.stimuli[7].Labels["oscar_test_event_id"] {
							t.Fatal("old episode used to clean uncertain refire")
						}
					}
				}
			}
			if (test.mode == "ambiguous first identity" || test.mode == "wrong first labels" || test.mode == "missing first history") && len(fixture.cleanup) != 0 {
				t.Fatal("unproved identity received cleanup")
			}
			if test.mode == "missing startsAt" && len(fixture.stimuli) != 7 {
				t.Fatalf("later lifecycle inputs continued: %d", len(fixture.stimuli))
			}
			if test.fixture == "flood-lifecycle-downstream.yaml" && test.mode == "valid" {
				if len(report.Checkpoints) != 5 || len(fixture.stimuli) != 13 {
					t.Fatal("lifecycle checkpoints/stimuli missing")
				}
				first, refire := fixture.stimuli[0], fixture.stimuli[7]
				if first.Fingerprint != refire.Fingerprint || first.StartsAt == refire.StartsAt {
					t.Fatal("refire lost stable labels or changed episode")
				}
				for _, clean := range fixture.cleanup {
					fp := clean.Labels["oscar_fingerprint"]
					latest := time.Time{}
					for _, row := range fixture.history {
						if row.Fingerprint == fp && row.StartsAt.After(latest) {
							latest = row.StartsAt
						}
					}
					if clean.StartsAt != latest.Format(time.RFC3339Nano) {
						t.Fatalf("cleanup bound wrong episode: %+v", clean)
					}
				}
			}
			for _, request := range server.Requests() {
				if strings.Contains(request.Path, "webhookreceiver") {
					t.Fatal("unexpected receiver path")
				}
			}
		})
	}
}
