package oscar

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func showcaseParentFixture(t *testing.T, pattern string) (ShowcaseParentExpectation, InstalledPackHistoryObservation) {
	t.Helper()
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	want := ShowcaseParentExpectation{Rule: Rule{ID: 7, Name: "corrtest-showcase-v2-" + pattern, Pattern: pattern}, QualificationRun: strings.Repeat("a", 32), Positive: true, Start: start, End: start.Add(90 * time.Second), SourceFingerprints: []string{"source-fp"}, WindowID: "7:012345678abc:1792497600000", ParentFingerprint: "history-parent-fp"}
	identity := sha256.Sum256([]byte(fmt.Sprintf(`["7",%q]`, want.WindowID)))
	labels := map[string]string{"alertname": "CorrTestShowcaseV2_" + pattern + "_synthetic_parent", "qualification_pack": "corrtest_showcase_v2", "qualification_run": want.QualificationRun, "site": pattern + "-positive", "severity": "critical", "oscar_synthetic_source": "correlator", "oscar_correlation_rule_id": "7", "oscar_correlation_rule_name": want.Rule.Name, "oscar_correlation_window_id": want.WindowID, "correlation_parent_identity": fmt.Sprintf("%x", identity)[:32], "am_fingerprint": want.ParentFingerprint, "oscar_fingerprint": "history-parent-fp"}
	pairs := []any{}
	for key, value := range labels {
		pairs = append(pairs, map[string]any{"Label": key, "Value": value})
	}
	age := 10 * time.Second
	if pattern == "persistence" {
		age = 30 * time.Second
	}
	row := map[string]any{"ID": "00000000-0000-4000-8000-000000000001", "alertname": labels["alertname"], "fingerprint": "history-parent-fp", "status": "firing", "severity": "critical", "startsAt": start.Add(age).Format(time.RFC3339), "createdAt": start.Add(age + 2*time.Second).Format(time.RFC3339), "labels": pairs, "annotations": []any{}}
	return want, showcaseHistory(t, []any{row})
}

func showcaseHistory(t *testing.T, rows []any) InstalledPackHistoryObservation {
	t.Helper()
	raw := oracleJSON(t, map[string]any{"records": rows, "total_records": len(rows), "total_pages": (len(rows) + 99) / 100, "page": 1, "per_page": 100})
	var page struct {
		Records []historyWire `json:"records"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	result := InstalledPackHistoryObservation{Pages: []json.RawMessage{raw}, Records: []HistoryRecord{}}
	for _, row := range page.Records {
		result.Records = append(result.Records, row.normalized())
	}
	return result
}

func showcaseParentChange(t *testing.T, observation InstalledPackHistoryObservation, change func(map[string]any)) InstalledPackHistoryObservation {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal(observation.Pages[0], &page); err != nil {
		t.Fatal(err)
	}
	rows := page["records"].([]any)
	change(rows[0].(map[string]any))
	return showcaseHistory(t, rows)
}

func TestShowcaseParentHistoryRequiresPhysicalParentForEachPattern(t *testing.T) {
	for _, pattern := range []string{"flood", "co_occurrence", "sequence", "threshold", "cross_source", "persistence"} {
		t.Run(pattern, func(t *testing.T) {
			want, observation := showcaseParentFixture(t, pattern)
			if got := EvaluateShowcaseParentHistory(want, observation); got.Disposition != InstalledFloodEligible {
				t.Fatalf("%+v", got)
			}
			if got := EvaluateShowcaseParentHistory(want, showcaseHistory(t, []any{})); got.Disposition != InstalledFloodIncomplete {
				t.Fatalf("audit expectation alone passed: %+v", got)
			}
			want.Positive = false
			want.ParentFingerprint, want.WindowID = "", ""
			if got := EvaluateShowcaseParentHistory(want, showcaseHistory(t, []any{})); got.Disposition != InstalledFloodEligible {
				t.Fatalf("complete empty read: %+v", got)
			}
			if got := EvaluateShowcaseParentHistory(want, InstalledPackHistoryObservation{}); got.Disposition == InstalledFloodEligible {
				t.Fatal("missing read passed as no parent")
			}
			if got := EvaluateShowcaseParentHistory(want, observation); got.Disposition == InstalledFloodEligible {
				t.Fatal("unexpected parent passed negative")
			}
		})
	}
}

func TestShowcaseParentRejectsWrongIdentitySeverityAndTruncation(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"severity": func(row map[string]any) { row["severity"] = "warning" },
		"resolved": func(row map[string]any) { row["status"] = "resolved" },
		"old":      func(row map[string]any) { row["createdAt"] = "2026-01-01T00:00:00Z" },
		"name":     func(row map[string]any) { row["alertname"] = "wrong" },
	}
	for _, key := range []string{"qualification_run", "site", "oscar_synthetic_source", "oscar_correlation_rule_id", "oscar_correlation_rule_name", "oscar_correlation_window_id", "correlation_parent_identity", "am_fingerprint", "oscar_fingerprint"} {
		mutations[key] = func(row map[string]any) {
			for _, raw := range row["labels"].([]any) {
				label := raw.(map[string]any)
				if label["Label"] == key {
					label["Value"] = "wrong"
				}
			}
		}
		mutations[key+"-truncated"] = func(row map[string]any) {
			for _, raw := range row["labels"].([]any) {
				label := raw.(map[string]any)
				if label["Label"] == key {
					label["value_truncated"] = true
				}
			}
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			want, observation := showcaseParentFixture(t, "flood")
			got := EvaluateShowcaseParentHistory(want, showcaseParentChange(t, observation, mutate))
			if got.Disposition == InstalledFloodEligible {
				t.Fatalf("mutation passed: %+v", got)
			}
		})
	}
}

func TestShowcaseParentRejectsMissingAuditBindingSourceReuseAndTypedSubstitution(t *testing.T) {
	for _, mutation := range []string{"window", "parent", "source", "typed", "duplicate", "unsupported"} {
		t.Run(mutation, func(t *testing.T) {
			want, observation := showcaseParentFixture(t, "flood")
			switch mutation {
			case "window":
				want.WindowID = ""
			case "parent":
				want.ParentFingerprint = ""
			case "source":
				want.SourceFingerprints = append(want.SourceFingerprints, "history-parent-fp")
			case "typed":
				observation.Records[0].Fingerprint = "substitute"
			case "duplicate":
				observation.Records = append(observation.Records, observation.Records[0])
			case "unsupported":
				want.Rule.Pattern = "absence"
			}
			if got := EvaluateShowcaseParentHistory(want, observation); got.Disposition == InstalledFloodEligible {
				t.Fatalf("%s passed", mutation)
			}
		})
	}
}

func TestShowcaseParentBindsCanonicalOSCARIdentityAndPersistenceDeadline(t *testing.T) {
	for _, pattern := range []string{"flood", "persistence"} {
		want, observed := showcaseParentFixture(t, pattern)
		observed = showcaseParentChange(t, observed, func(row map[string]any) {
			if pattern == "persistence" {
				row["startsAt"] = want.Start.Add(time.Second).Format(time.RFC3339)
				row["createdAt"] = row["startsAt"]
				return
			}
			row["fingerprint"] = "different-history"
			for _, raw := range row["labels"].([]any) {
				label := raw.(map[string]any)
				if label["Label"] == "oscar_fingerprint" {
					label["Value"] = "different-history"
				}
			}
		})
		if got := EvaluateShowcaseParentHistory(want, observed); got.Disposition == InstalledFloodEligible {
			t.Fatalf("%s mismatch accepted", pattern)
		}
	}
}
