package score

import (
	"fmt"
	"sort"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// RevisionAnnotation is the publication id OSCAR stamps on an alert whose
// fingerprint a managed policy publication computed.
const RevisionAnnotation = "oscar_fingerprint_policy_revision"

// FingerprintRound is one verification: the merge variants and the split
// control of that round, read back from History.
type FingerprintRound struct {
	Sent               int      `json:"sent,omitempty"` // this round's accepted posts (multi-round lanes)
	MergedRows         int      `json:"mergedRows"`
	MergedFingerprints []string `json:"mergedFingerprints"`
	SplitRows          int      `json:"splitRows"`
	SplitFingerprints  []string `json:"splitFingerprints"`
}

// FingerprintStats is the fingerprint-policy lane: per round, Repeats variants
// differing only in the ignored label (merge device) and one differing in
// device (split control), read back from History. The merged and split
// counts and fingerprints are totals over Rounds. Skipped (policies not
// commissioned) is report-only: never PASS or FAIL.
type FingerprintStats struct {
	Skipped            string             `json:"skipped,omitempty"`
	Inconclusive       string             `json:"inconclusive,omitempty"`
	PublicationID      string             `json:"publicationId,omitempty"`
	Sent               int                `json:"sent"` // accepted posts
	MergedRows         int                `json:"mergedRows"`
	MergedFingerprints []string           `json:"mergedFingerprints"`
	SplitRows          int                `json:"splitRows"`
	SplitFingerprints  []string           `json:"splitFingerprints"`
	NotInForce         []string           `json:"notInForce,omitempty"` // History ids without the publication's revision annotation
	Rounds             []FingerprintRound `json:"rounds,omitempty"`
	Reverted           bool               `json:"reverted"`
}

// Observe adds one round: the lane's History rows (alertname) by device.
func (s *FingerprintStats) Observe(rows []oscar.HistoryRecord, alertname, merged, split string) {
	m, sp := map[string]bool{}, map[string]bool{}
	var rd FingerprintRound
	for _, r := range rows {
		if r.AlertName != alertname {
			continue
		}
		switch r.Labels["device"] {
		case merged:
			rd.MergedRows++
			m[r.Fingerprint] = true
		case split:
			rd.SplitRows++
			sp[r.Fingerprint] = true
		default:
			continue
		}
		if r.Annotations[RevisionAnnotation] != s.PublicationID {
			s.NotInForce = append(s.NotInForce, r.ID)
		}
	}
	rd.MergedFingerprints, rd.SplitFingerprints = keys(m), keys(sp)
	s.Rounds = append(s.Rounds, rd)
	s.MergedRows += rd.MergedRows
	s.SplitRows += rd.SplitRows
	s.MergedFingerprints = append(s.MergedFingerprints, rd.MergedFingerprints...)
	s.SplitFingerprints = append(s.SplitFingerprints, rd.SplitFingerprints...)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// JudgeFingerprint: FAIL when a round's merge variants are not one History
// row and fingerprint, or its split control shares their fingerprint;
// INCONCLUSIVE when the lane did not finish, no round was verified, a post
// was not accepted, a row is missing or the policy is not proven in force on
// every row. Skipped adds nothing.
func JudgeFingerprint(s FingerprintStats, repeats int) (fail, incon []string) {
	switch {
	case s.Skipped != "":
		return nil, nil
	case s.Inconclusive != "":
		return nil, []string{"fingerprint: " + s.Inconclusive}
	case len(s.Rounds) == 0:
		return nil, []string{"fingerprint: no round verified"}
	case s.Sent < len(s.Rounds)*(repeats+1):
		return nil, []string{fmt.Sprintf("fingerprint: %d of %d posts accepted", s.Sent, len(s.Rounds)*(repeats+1))}
	}
	round := func(i int) string {
		if len(s.Rounds) > 1 {
			return fmt.Sprintf(" (round %d)", i+1)
		}
		return ""
	}
	for i, rd := range s.Rounds {
		if rd.Sent != 0 && rd.Sent < repeats+1 {
			return nil, []string{fmt.Sprintf("fingerprint: %d of %d posts accepted%s", rd.Sent, repeats+1, round(i))}
		}
		switch {
		case rd.MergedRows == 0:
			return nil, []string{"fingerprint: no History row for the merge variants" + round(i)}
		case rd.SplitRows == 0:
			return nil, []string{"fingerprint: no History row for the split control" + round(i)}
		}
	}
	if len(s.NotInForce) > 0 {
		return nil, []string{fmt.Sprintf("fingerprint: policy not proven in force: History %v lack %s=%s", s.NotInForce, RevisionAnnotation, s.PublicationID)}
	}
	for i, rd := range s.Rounds {
		if rd.MergedRows > 1 || len(rd.MergedFingerprints) > 1 {
			fail = append(fail, fmt.Sprintf("fingerprint merge: %d History rows, %d fingerprints for %d variants differing only in the ignored label, want 1%s",
				rd.MergedRows, len(rd.MergedFingerprints), repeats, round(i)))
		}
		for _, f := range rd.SplitFingerprints {
			for _, g := range rd.MergedFingerprints {
				if f == g {
					fail = append(fail, "fingerprint split: the control differing in device shares fingerprint "+f+round(i))
				}
			}
		}
	}
	return fail, nil
}
