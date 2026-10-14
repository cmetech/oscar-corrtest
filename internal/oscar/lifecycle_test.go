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
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestLifecycleRequestCarriesEpisodeTimesWithoutChangingFingerprint(t *testing.T) {
	start := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	alert := compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A", "oscar_test_run_id": "run"}, StartsAt: start}
	firing, err := oscar.BuildAlertRequest(alert)
	if err != nil {
		t.Fatal(err)
	}
	alert.Status = "resolved"
	alert.EndsAt = start.Add(time.Second)
	clear, err := oscar.BuildAlertRequest(alert)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(clear)
	var wire struct {
		Alerts []map[string]any `json:"alerts"`
	}
	json.Unmarshal(raw, &wire)
	if wire.Alerts[0]["startsAt"] != "2026-10-13T10:00:00Z" || wire.Alerts[0]["endsAt"] != "2026-10-13T10:00:01Z" {
		t.Fatalf("episode times absent or changed: %s", raw)
	}
	if firing.Alerts[0].Fingerprint != clear.Alerts[0].Fingerprint {
		t.Fatal("recovery changed source fingerprint")
	}
}

func TestHistoryCleanupRetainsReadBackFiringEpisode(t *testing.T) {
	server := testoscar.New(t)
	server.Enqueue(testoscar.Response{Status: 200, Body: `{"total_records":1,"records":[{"id":1,"alertname":"A","fingerprint":"server","status":"firing","startsAt":"2026-10-13T10:00:00Z","labels":[{"Label":"oscar_test_run_id","Value":"run"}]}]}`})
	client := newClient(t, server.URL())
	rows, err := client.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "A", Start: time.Now().Add(-time.Hour), End: time.Now()})
	if err != nil || len(rows) != 1 {
		t.Fatalf("history read: %v %v", rows, err)
	}
	request, err := oscar.BuildResolutionRequest(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(request)
	if !strings.Contains(string(raw), `"startsAt":"2026-10-13T10:00:00Z"`) {
		t.Fatalf("cleanup lost source episode: %s", raw)
	}
}

func TestNamedEpisodePreviewDeclaresRuntimeTimes(t *testing.T) {
	doc := scenario.Builtin("persistence")
	for i := range doc.Cases[1].Events {
		doc.Cases[1].Events[i].Source = "A"
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_lifecycle", ShortToken: "LIFE1234"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	operations, err := oscar.BuildOperationPreview(plan, "test")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range operations {
		if operation.Stage == "stimulus.inject_alert" && operation.CaseCode == "N01" && operation.Attempt == 2 {
			found = true
			if len(operation.RuntimeFields) == 0 || !strings.Contains(operation.Body, "startsAt") || !strings.Contains(operation.Body, "endsAt") {
				t.Fatalf("preview omits runtime episode: %+v", operation)
			}
		}
	}
	if !found {
		t.Fatal("recovery preview missing")
	}
}
