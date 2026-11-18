package score

import (
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func fpRow(id, device, fp, rev string) oscar.HistoryRecord {
	return oscar.HistoryRecord{ID: id, AlertName: "FP", Fingerprint: fp,
		Labels:      map[string]string{"alertname": "FP", "device": device},
		Annotations: map[string]string{"oscar_fingerprint_policy_revision": rev}}
}

func TestJudgeFingerprint(t *testing.T) {
	merged, split := fpRow("1", "a", "f1", "pub-2"), fpRow("2", "b", "f2", "pub-2")
	other := fpRow("9", "a", "fx", "pub-2")
	other.AlertName = "OTHER"
	for _, tc := range []struct {
		name       string
		st         FingerprintStats
		rows       []oscar.HistoryRecord
		fail, inco string
	}{
		{name: "merge and split", rows: []oscar.HistoryRecord{merged, split, other}},
		{name: "skipped is neither pass nor fail", st: FingerprintStats{Skipped: "fingerprint policies unpublished"}},
		{name: "repeats not merged", rows: []oscar.HistoryRecord{merged, fpRow("3", "a", "f3", "pub-2"), split}, fail: "fingerprint merge: 2 History rows, 2 fingerprints"},
		{name: "control merged too", rows: []oscar.HistoryRecord{merged, fpRow("2", "b", "f1", "pub-2")}, fail: "fingerprint split"},
		{name: "policy not in force", rows: []oscar.HistoryRecord{merged, fpRow("3", "a", "f3", ""), split}, inco: "not proven in force"},
		{name: "no control row", rows: []oscar.HistoryRecord{merged}, inco: "no History row for the split control"},
		{name: "posts not accepted", st: FingerprintStats{Sent: 2}, rows: []oscar.HistoryRecord{merged, split}, inco: "2 of 4 posts accepted"},
		{name: "lane failed", st: FingerprintStats{Inconclusive: "enable: boom"}, inco: "fingerprint: enable: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st
			if st.Skipped == "" && st.Inconclusive == "" {
				st.PublicationID = "pub-2"
				if st.Sent == 0 {
					st.Sent = 4
				}
				st.Observe(tc.rows, "FP", "a", "b")
			}
			fail, inco := JudgeFingerprint(st, 3)
			got := func(xs []string, want string) bool {
				if want == "" {
					return len(xs) == 0
				}
				return len(xs) > 0 && strings.Contains(strings.Join(xs, "\n"), want)
			}
			if !got(fail, tc.fail) || !got(inco, tc.inco) {
				t.Fatalf("fail %v incon %v (stats %+v)", fail, inco, st)
			}
		})
	}
}
