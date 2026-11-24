package precheck

import (
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// NeedsEscalation reports whether s has an escalation cohort (v4).
func NeedsEscalation(s scenario.Scenario) bool {
	for _, p := range s.Resend {
		if p.Escalation != nil && p.Episode > 0 {
			return true
		}
	}
	return false
}

// EscalationSchedule is B12 (v4, blocking): escalations fire only from the
// resend schedule, so resend policies must be enabled (RESEND_POLICY_ENABLED)
// and the resend status must report schedule operations, which it does only
// with RESEND_SCHEDULE_ENABLED=true. st is the CAMPAIGN_SINK notification status.
func EscalationSchedule(st targetops.PolicyStatus, err error) Result {
	if err != nil {
		return errRes("B12", Blocking, "resend policy status", err)
	}
	var probs []string
	if !st.ActivationEnabled {
		probs = append(probs, "resend policies disabled (activation_enabled=false; RESEND_POLICY_ENABLED)")
	}
	if s := string(st.ScheduleOperations); s == "" || s == "null" {
		probs = append(probs, "escalation scheduling off (schedule_operations=null; set RESEND_SCHEDULE_ENABLED=true)")
	}
	return res("B12", Blocking, true, "schedule_operations="+string(st.ScheduleOperations), probs)
}

// ReportSetup is B13 (v4, blocking): the alert report exists, the first
// front-office user (a user account) can read it, and it is approved for
// manual artifact runs.
func ReportSetup(key string, info targetops.ReportInfo, err error) Result {
	if err != nil {
		return errRes("B13", Blocking, "alert report "+key, err)
	}
	var probs []string
	if info.SetupState != "approved" {
		probs = append(probs, "report "+key+" setup_state "+info.SetupState+", want approved (POST /api/v1/reports/{id}/approve)")
	}
	return res("B13", Blocking, true, "report "+info.ID+" setup_state "+info.SetupState, probs)
}
