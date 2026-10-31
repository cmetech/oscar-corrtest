package snmpload_test

import (
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/snmpload"
	"testing"
)

func TestReconciliationRequiresExactOwnedResolvedHistory(t *testing.T) {
	e, _ := snmpload.Fixture("crt_owned", 1, "dhcp-limit")
	major := "major"
	row := oscar.HistoryRecord{ID: "history", Fingerprint: "fingerprint", AlertName: e.AlertName, Status: "resolved", OriginalSeverity: &major, Labels: map[string]string{"agent": e.Agent, "alert_group": e.AlertGroup, "alert_key": e.AlertKey}}
	duplicate := row
	duplicate.ID = "other-history"
	contradictory := row
	contradictory.Status = "firing"
	cases := []struct {
		name string
		rows []oscar.HistoryRecord
		want string
	}{{"resolved", []oscar.HistoryRecord{row}, "resolved"}, {"missing", nil, "missing"}, {"duplicate", []oscar.HistoryRecord{row, duplicate}, "duplicate"}, {"pagination-overlap", []oscar.HistoryRecord{row, row}, "resolved"}, {"changed-during-pagination", []oscar.HistoryRecord{row, contradictory}, "unstable"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := snmpload.Reconcile([]snmpload.Episode{e}, c.rows)
			if got[0].State != c.want {
				t.Fatalf("%+v", got)
			}
		})
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
