package snmpload

import (
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"reflect"
	"strings"
)

type Observation struct {
	Episode     int    `json:"episode"`
	State       string `json:"state"`
	HistoryID   string `json:"historyId,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
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
		matches := indexed[e.AlertName+"\x00"+e.AlertKey]
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
			if !owned || row.ID == "" || row.ID == "<nil>" || row.Fingerprint == "" || row.Labels["agent"] != e.Agent || row.Labels["alert_group"] != e.AlertGroup || row.OriginalSeverity == nil || *row.OriginalSeverity != e.Severity {
				o.State = "mismatch"
			} else if row.Status == "resolved" || row.Status == "firing" {
				o.State = row.Status
			} else {
				o.State = "mismatch"
			}
		}
		result[i] = o
	}
	return result
}
