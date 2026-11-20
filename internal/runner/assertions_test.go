package runner

import (
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// Rows as OSCAR wrote them for P01 in camp_5ba5dd2750e7a869: one enriched row
// for the child's link and one for the parent registering itself.
func TestParentLinkCountIgnoresTheParentsOwnRow(t *testing.T) {
	child := oscar.AuditRecord{ID: 1, AlertFingerprint: "7f5695c5039ba7cc", Outcome: "enriched", ParentFingerprint: "ffe2b2d6285d401d",
		AddedLabels: map[string]string{"oscar_correlation_role": "child", "oscar_correlation_parent_fingerprint": "ffe2b2d6285d401d"},
		Reason:      "parent_child_active_parent_present tag_only parent_fp=ffe2b2d6285d401d"}
	parent := oscar.AuditRecord{ID: 2, AlertFingerprint: "ffe2b2d6285d401d", Outcome: "enriched", ParentFingerprint: "ffe2b2d6285d401d",
		AddedLabels: map[string]string{"oscar_correlation_role": "parent"}, Reason: "parent_registered"}
	unlabelledChild := oscar.AuditRecord{ID: 3, AlertFingerprint: "aa", Outcome: "enriched", ParentFingerprint: "ffe2b2d6285d401d"}
	unlabelledParent := oscar.AuditRecord{ID: 4, AlertFingerprint: "ffe2b2d6285d401d", Outcome: "enriched", ParentFingerprint: "ffe2b2d6285d401d"}

	for name, tc := range map[string]struct {
		audits []oscar.AuditRecord
		kind   string
		want   int
	}{
		"P01 role labels":         {[]oscar.AuditRecord{child, parent}, "parent-link-count", 1},
		"fingerprint fallback":    {[]oscar.AuditRecord{unlabelledChild, unlabelledParent}, "parent-link-count", 1},
		"audit-count counts both": {[]oscar.AuditRecord{child, parent}, "audit-count", 2},
	} {
		results, err := evaluateAssertions([]scenario.Assertion{{Kind: tc.kind, Outcome: "enriched", Equals: tc.want}}, observationSnapshot{Audits: tc.audits}, false)
		if err != nil || results[0].Observed != tc.want || results[0].Verdict != domain.VerdictPass {
			t.Errorf("%s: %+v %v", name, results, err)
		}
	}
}
