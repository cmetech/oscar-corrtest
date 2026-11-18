package score

import (
	"fmt"
	"sort"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// RevisionAnnotation is the publication id OSCAR stamps on an alert whose
// fingerprint a managed policy publication computed.
const RevisionAnnotation = "oscar_fingerprint_policy_revision"

// FingerprintStats is the fingerprint-policy lane: Repeats variants differing
// only in the ignored label (merge device) and one differing in device (split
// control), read back from History. Skipped (policies not commissioned) is
// report-only: never PASS or FAIL.
type FingerprintStats struct {
	Skipped            string   `json:"skipped,omitempty"`
	Inconclusive       string   `json:"inconclusive,omitempty"`
	PublicationID      string   `json:"publicationId,omitempty"`
	Sent               int      `json:"sent"` // accepted posts
	MergedRows         int      `json:"mergedRows"`
	MergedFingerprints []string `json:"mergedFingerprints"`
	SplitRows          int      `json:"splitRows"`
	SplitFingerprints  []string `json:"splitFingerprints"`
	NotInForce         []string `json:"notInForce,omitempty"` // History ids without the publication's revision annotation
	Reverted           bool     `json:"reverted"`
}

// Observe reads the lane's History rows (alertname) by device.
func (s *FingerprintStats) Observe(rows []oscar.HistoryRecord, alertname, merged, split string) {
	m, sp := map[string]bool{}, map[string]bool{}
	s.MergedRows, s.SplitRows, s.NotInForce = 0, 0, nil
	for _, r := range rows {
		if r.AlertName != alertname {
			continue
		}
		switch r.Labels["device"] {
		case merged:
			s.MergedRows++
			m[r.Fingerprint] = true
		case split:
			s.SplitRows++
			sp[r.Fingerprint] = true
		default:
			continue
		}
		if r.Annotations[RevisionAnnotation] != s.PublicationID {
			s.NotInForce = append(s.NotInForce, r.ID)
		}
	}
	s.MergedFingerprints, s.SplitFingerprints = keys(m), keys(sp)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// JudgeFingerprint: FAIL when the merge variants are not one History row and
// fingerprint, or the split control shares their fingerprint; INCONCLUSIVE
// when the lane did not finish, a post was not accepted, a row is missing or
// the policy is not proven in force on every row. Skipped adds nothing.
func JudgeFingerprint(s FingerprintStats, repeats int) (fail, incon []string) {
	switch {
	case s.Skipped != "":
		return nil, nil
	case s.Inconclusive != "":
		return nil, []string{"fingerprint: " + s.Inconclusive}
	case s.Sent < repeats+1:
		return nil, []string{fmt.Sprintf("fingerprint: %d of %d posts accepted", s.Sent, repeats+1)}
	case s.MergedRows == 0:
		return nil, []string{"fingerprint: no History row for the merge variants"}
	case s.SplitRows == 0:
		return nil, []string{"fingerprint: no History row for the split control"}
	case len(s.NotInForce) > 0:
		return nil, []string{fmt.Sprintf("fingerprint: policy not proven in force: History %v lack %s=%s", s.NotInForce, RevisionAnnotation, s.PublicationID)}
	}
	if s.MergedRows > 1 || len(s.MergedFingerprints) > 1 {
		fail = append(fail, fmt.Sprintf("fingerprint merge: %d History rows, %d fingerprints for %d variants differing only in the ignored label, want 1",
			s.MergedRows, len(s.MergedFingerprints), repeats))
	}
	for _, f := range s.SplitFingerprints {
		for _, g := range s.MergedFingerprints {
			if f == g {
				fail = append(fail, "fingerprint split: the control differing in device shares fingerprint "+f)
			}
		}
	}
	return fail, nil
}
