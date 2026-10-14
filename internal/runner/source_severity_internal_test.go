package runner

import (
	"encoding/json"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"testing"
	"time"
)

func TestSourceEpisodeRequiresNonconflictingObservedSeverity(t *testing.T) {
	start := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	offset := time.Duration(0)
	for _, tc := range []struct {
		name  string
		top   any
		label string
		ready bool
	}{
		{"promoted", "warning", "", true},
		{"matching dual", "warning", "warning", true},
		{"label only", nil, "warning", true},
		{"missing", nil, "", false},
		{"changed", "critical", "", false},
		{"conflict top", "critical", "warning", false},
		{"conflict label", "warning", "critical", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alert := compiler.AlertPlan{Name: "A", Source: "A", EpisodeOffset: &offset, Labels: map[string]string{"alertname": "A", "oscar_test_event_id": "child", "severity": "warning"}}
			labels := map[string]string{"alertname": "A", "oscar_test_event_id": "child"}
			if tc.label != "" {
				labels["severity"] = tc.label
			}
			raw, _ := json.Marshal(map[string]any{"alertName": "A", "fingerprint": "fp", "startsAt": start, "status": "firing", "labels": labels, "severity": tc.top})
			var row oscar.HistoryRecord
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			evidence, ready := bindSourceEpisodes(compiler.CasePlan{EpisodeBase: start, Alerts: []compiler.AlertPlan{alert}}, []oscar.HistoryRecord{row})
			if ready != tc.ready || evidence[0].Ready != tc.ready {
				t.Fatalf("evidence=%+v ready=%t", evidence, ready)
			}
			if tc.label == "" {
				if _, ok := evidence[0].Rows[0].Labels["severity"]; ok {
					t.Error("severity fabricated into observed labels")
				}
			}
		})
	}
}

func TestResolvedSeverityObservationDoesNotRelaxNativeInputIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		observe   bool
	}{
		{"canonical", `{"status":"resolved","severity":"cleared","originalSeverity":"critical"}`, true},
		{"matching original label", `{"status":"resolved","severity":"cleared","originalSeverity":"critical","labels":{"severity":"critical"}}`, true},
		{"changed label", `{"status":"resolved","severity":"cleared","originalSeverity":"critical","labels":{"severity":"warning"}}`, false},
		{"missing original", `{"status":"resolved","severity":"cleared"}`, false},
		{"wrong original", `{"status":"resolved","severity":"cleared","originalSeverity":"warning"}`, false},
		{"not resolved", `{"status":"firing","severity":"cleared","originalSeverity":"critical"}`, false},
		{"arbitrary current", `{"status":"resolved","severity":"warning","originalSeverity":"critical"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var row oscar.HistoryRecord
			if err := json.Unmarshal([]byte(tc.raw), &row); err != nil {
				t.Fatal(err)
			}
			if got := matchesEpisodeInputLabel(row, "severity", "critical"); got != tc.observe {
				t.Fatalf("observation=%t", got)
			}
			if row.MatchesInputLabel("severity", "critical") {
				t.Fatal("resolved lifecycle exception weakened firing/native-clear identity")
			}
		})
	}
}
