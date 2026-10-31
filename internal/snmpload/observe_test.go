package snmpload_test

import (
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/snmpload"
	"testing"
)

func TestReconciliationRequiresExactOwnedResolvedHistory(t *testing.T) {
	e, _ := snmpload.Fixture("crt_owned", 1, "dhcp-limit")
	major := "major"
	row := oscar.HistoryRecord{ID: "history", Fingerprint: "fingerprint", AlertName: e.AlertName, Status: "resolved", OriginalSeverity: &major, Labels: map[string]string{"agent": e.Agent, "alert_group": e.AlertGroup, "alert_key": e.AlertKey, "correlation_key": "corr-owned"}}
	info := "info"
	clear := oscar.HistoryRecord{ID: "clear-history", Fingerprint: "clear-fingerprint", AlertName: e.AlertName, Status: "resolved", OriginalSeverity: &info, Labels: map[string]string{"agent": e.Agent, "alert_group": e.AlertGroup, "alert_key": e.AlertKey, "correlation_key": "corr-owned", "is_resolution": "true", "correlation_value": "corr-owned", "correlation_status": "success", "correlation_resolved_count": "1"}}
	duplicate := row
	duplicate.ID = "other-history"
	contradictory := row
	contradictory.Status = "firing"
	cases := []struct {
		name string
		rows []oscar.HistoryRecord
		want string
	}{{"resolved", []oscar.HistoryRecord{row, clear}, "resolved"}, {"missing", nil, "missing"}, {"duplicate", []oscar.HistoryRecord{row, duplicate}, "duplicate"}, {"pagination-overlap", []oscar.HistoryRecord{row, row, clear, clear}, "resolved"}, {"changed-during-pagination", []oscar.HistoryRecord{row, contradictory}, "unstable"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := snmpload.Reconcile([]snmpload.Episode{e}, c.rows)
			if got[0].State != c.want {
				t.Fatalf("%+v", got)
			}
		})
	}
	normalized := row
	normalized.Labels = map[string]string{"agent": e.Agent, "alert_group": "jnxjdhcplocalserverinterfacelimitstatus", "alert_key": e.AlertKey, "correlation_key": "corr-owned"}
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{normalized, clear}); got[0].State != "resolved" {
		t.Fatal("receiver-normalized group rejected", got)
	}
	normalized.Labels["alert_group"] = "different-group"
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{normalized}); got[0].State != "mismatch" {
		t.Fatal("unrelated group accepted")
	}
	wrong := row
	wrong.OriginalSeverity = nil
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{wrong}); got[0].State != "mismatch" {
		t.Fatal("missing firing proof accepted")
	}
	wrong = row
	wrong.Status = "firing"
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{wrong}); got[0].State != "firing" {
		t.Fatal("active source called resolved")
	}
	e2, _ := snmpload.Fixture("crt_foreign", 1, "dhcp-limit")
	if got := snmpload.Reconcile([]snmpload.Episode{e2}, []oscar.HistoryRecord{row}); got[0].State != "missing" {
		t.Fatal("foreign identity counted")
	}
}

func TestRecoveryEvidenceCannotHideMissingOrDuplicateProblems(t *testing.T) {
	e, _ := snmpload.Fixture("crt_clear", 0, "dhcp-limit")
	major, info := "major", "info"
	problem := oscar.HistoryRecord{ID: "problem", Fingerprint: "p", AlertName: e.AlertName, Status: "resolved", OriginalSeverity: &major, Labels: map[string]string{"agent": e.Agent, "alert_group": e.AlertGroup, "alert_key": e.AlertKey, "correlation_key": "corr"}}
	clear := oscar.HistoryRecord{ID: "clear", Fingerprint: "c", AlertName: e.AlertName, Status: "resolved", OriginalSeverity: &info, Labels: map[string]string{"agent": e.Agent, "alert_group": e.AlertGroup, "alert_key": e.AlertKey, "correlation_key": "corr", "is_resolution": "true", "correlation_value": "corr", "correlation_status": "success", "correlation_resolved_count": "1"}}
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{problem}); got[0].State != "clear-missing" {
		t.Fatal("auto-resolved problem accepted without recovery", got)
	}
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{clear}); got[0].State != "missing" {
		t.Fatal("recovery substituted for problem", got)
	}
	second := problem
	second.ID = "duplicate-problem"
	second.Fingerprint = "p2"
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{problem, second, clear}); got[0].State != "duplicate" {
		t.Fatal("duplicate problem hidden", got)
	}
	unstableClear := clear
	unstableClear.Status = "firing"
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{problem, second, clear, unstableClear}); got[0].State != "duplicate" {
		t.Fatal("unstable clear hid duplicate problems", got)
	}
	second = clear
	second.ID = "duplicate-clear"
	second.Fingerprint = "c2"
	if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{problem, clear, second}); got[0].State != "duplicate" {
		t.Fatal("duplicate recovery hidden", got)
	}
	for _, patch := range []map[string]string{{"correlation_status": "error"}, {"correlation_resolved_count": "2"}} {
		bad := clear
		bad.Labels = map[string]string{}
		for k, v := range clear.Labels {
			bad.Labels[k] = v
		}
		for k, v := range patch {
			bad.Labels[k] = v
		}
		if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{problem, bad}); got[0].State != "mismatch" {
			t.Fatal("failed/ambiguous resolution accepted", got)
		}
	}

	for key, value := range map[string]string{"correlation_key": "foreign", "correlation_value": "foreign", "correlation_status": "zero_match", "correlation_resolved_count": "0", "agent": "wrong", "alert_group": "wrong"} {
		bad := clear
		bad.Labels = map[string]string{}
		for k, v := range clear.Labels {
			bad.Labels[k] = v
		}
		bad.Labels[key] = value
		if got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{problem, bad}); got[0].State != "mismatch" {
			t.Fatalf("bad %s accepted: %+v", key, got)
		}
	}
}

func TestKnownFiringSurvivesDuplicateClassification(t *testing.T) {
	e, _ := snmpload.Fixture("crt_dirty", 0, "dhcp-limit")
	major := "major"
	r := oscar.HistoryRecord{ID: "one", Fingerprint: "p", AlertName: e.AlertName, Status: "firing", OriginalSeverity: &major, Labels: map[string]string{"agent": e.Agent, "alert_group": e.AlertGroup, "alert_key": e.AlertKey}}
	other := r
	other.ID = "two"
	got := snmpload.Reconcile([]snmpload.Episode{e}, []oscar.HistoryRecord{r, other})
	if got[0].State != "duplicate" || !got[0].ProblemFiring {
		t.Fatal("lost known active source", got)
	}
}
