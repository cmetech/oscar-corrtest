package oscar

import (
	"strings"
	"testing"
	"time"
)

func TestExistingFeedHistoryKeepsNativeAndOscarIdentityDistinct(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) {
			manifest, err := FreezeExistingFeedManifest(existingFeedSpec(t, profile))
			if err != nil {
				t.Fatal(err)
			}
			evidence, _, _ := manifest.Prepare(0)
			native := ExistingFeedNativeObservation{Labels: cloneStringMap(evidence.Expected.Labels), StartsAt: evidence.Expected.StartsAt, Fingerprint: "0123456789abcdef"}
			makeHistory := func() HistoryRecord {
				labels := cloneStringMap(native.Labels)
				delete(labels, "alertname")
				delete(labels, "severity")
				labels["am_fingerprint"], labels["oscar_fingerprint"] = native.Fingerprint, strings.Repeat("a", 64)
				labels["ordinary_enrichment"] = "allowed"
				severity := native.Labels["severity"]
				return HistoryRecord{ID: "101", AlertName: native.Labels["alertname"], StartsAt: native.StartsAt, Fingerprint: labels["oscar_fingerprint"], Status: "firing", Labels: labels, Severity: &severity}
			}
			valid := makeHistory()
			if err := manifest.VerifyHistory(0, native, []HistoryRecord{valid}); err != nil {
				t.Fatal(err)
			}
			for _, fault := range []string{"missing", "ambiguous", "native-label", "native-start", "native-fingerprint", "history-start", "history-id", "history-name", "history-status", "history-fingerprint", "am-fingerprint", "oscar-fingerprint", "label", "severity", "conflicting-severity", "wrong-index"} {
				t.Run(fault, func(t *testing.T) {
					row := makeHistory()
					n := native
					n.Labels = cloneStringMap(native.Labels)
					index := 0
					rows := []HistoryRecord{row}
					switch fault {
					case "missing":
						rows = nil
					case "ambiguous":
						rows = append(rows, row)
					case "native-label":
						n.Labels["foreign"] = "yes"
					case "native-start":
						n.StartsAt = n.StartsAt.Add(time.Second)
					case "native-fingerprint":
						n.Fingerprint = "foreign"
					case "history-start":
						rows[0].StartsAt = rows[0].StartsAt.Add(time.Second)
					case "history-id":
						rows[0].ID = ""
					case "history-name":
						rows[0].AlertName = "foreign"
					case "history-status":
						rows[0].Status = "resolved"
					case "history-fingerprint":
						rows[0].Fingerprint = ""
					case "am-fingerprint":
						rows[0].Labels["am_fingerprint"] = row.Fingerprint
					case "oscar-fingerprint":
						rows[0].Labels["oscar_fingerprint"] = n.Fingerprint
					case "label":
						rows[0].Labels["site"] = "foreign"
					case "severity":
						rows[0].Severity = nil
					case "conflicting-severity":
						rows[0].Labels["severity"] = "foreign"
					case "wrong-index":
						index = 1
					}
					if err := manifest.VerifyHistory(index, n, rows); err == nil {
						t.Fatal("ambiguous or changed identity accepted")
					}
				})
			}
		})
	}
}
