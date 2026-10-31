package snmpload

import (
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"reflect"
	"strings"
)

type Observation struct {
	ProblemFiring    bool   `json:"problemFiring"`
	Episode          int    `json:"episode"`
	State            string `json:"state"`
	HistoryID        string `json:"historyId,omitempty"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	ClearHistoryID   string `json:"clearHistoryId,omitempty"`
	ClearFingerprint string `json:"clearFingerprint,omitempty"`
}

func Reconcile(episodes []Episode, rows []oscar.HistoryRecord) []Observation {
	indexed := map[string][]oscar.HistoryRecord{}
	for _, r := range rows {
		k := r.AlertName + "\x00" + r.Labels["alert_key"]
		overlap := false
		for _, previous := range indexed[k] {
			if r.ID != "" && r.ID != "<nil>" && reflect.DeepEqual(previous, r) {
				overlap = true
				break
			}
		}
		if !overlap {
			indexed[k] = append(indexed[k], r)
		}
	}
	result := make([]Observation, len(episodes))
	for i, e := range episodes {
		o := Observation{Episode: i, State: "missing"}
		matches, clears := []oscar.HistoryRecord{}, []oscar.HistoryRecord{}
		for _, row := range indexed[e.AlertName+"\x00"+e.AlertKey] {
			if row.Labels["is_resolution"] != "true" && row.Status == "firing" {
				o.ProblemFiring = true
			}
			if row.Labels["is_resolution"] == "true" {
				clears = append(clears, row)
			} else {
				matches = append(matches, row)
			}
		}
		if len(matches) > 1 {
			o.State = "duplicate"
			sameID := matches[0].ID != "" && matches[0].ID != "<nil>"
			for _, row := range matches[1:] {
				sameID = sameID && row.ID == matches[0].ID
			}
			if sameID {
				o.State = "unstable"
			}
		} else if len(matches) == 1 {
			row := matches[0]
			o.HistoryID = row.ID
			o.Fingerprint = row.Fingerprint
			owned := false
			for _, v := range row.Labels {
				owned = owned || strings.Contains(v, e.Token)
			}
			for _, v := range row.Annotations {
				owned = owned || strings.Contains(v, e.Token)
			}
			if !owned || row.ID == "" || row.ID == "<nil>" || row.Fingerprint == "" || row.Labels["agent"] != e.Agent || !strings.EqualFold(row.Labels["alert_group"], e.AlertGroup) || row.OriginalSeverity == nil || *row.OriginalSeverity != e.Severity {
				o.State = "mismatch"
			} else if row.Status == "resolved" || row.Status == "firing" {
				o.State = row.Status
			} else {
				o.State = "mismatch"
			}
		}
		if len(clears) > 1 {
			clearState := "duplicate"
			sameID := clears[0].ID != "" && clears[0].ID != "<nil>"
			for _, row := range clears[1:] {
				sameID = sameID && row.ID == clears[0].ID
			}
			if sameID {
				clearState = "unstable"
			}
			if clearState == "duplicate" || (o.State != "duplicate" && o.State != "mismatch") {
				o.State = clearState
			}
		} else if len(matches) == 1 && len(clears) == 1 && (o.State == "resolved" || o.State == "firing") {
			problem, clear := matches[0], clears[0]
			o.ClearHistoryID = clear.ID
			o.ClearFingerprint = clear.Fingerprint
			key := problem.Labels["correlation_key"]
			// shortcut: fast-clear evidence only; add independent late-clear attribution before accepting zero-match recoveries.
			if clear.ID == "" || clear.ID == "<nil>" || clear.ID == problem.ID || clear.Fingerprint == "" || clear.Fingerprint == problem.Fingerprint || clear.OriginalSeverity == nil || *clear.OriginalSeverity != "info" || clear.Status != "resolved" || clear.Labels["agent"] != e.Agent || !strings.EqualFold(clear.Labels["alert_group"], e.AlertGroup) || key == "" || clear.Labels["correlation_key"] != key || clear.Labels["correlation_value"] != key || clear.Labels["correlation_status"] != "success" || clear.Labels["correlation_resolved_count"] != "1" {
				o.State = "mismatch"
			}
		} else if o.State == "resolved" && len(clears) == 0 {
			o.State = "clear-missing"
		}
		result[i] = o
	}
	return result
}
