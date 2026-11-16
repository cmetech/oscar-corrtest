package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// R4 is scored from OSCAR's "still firing" incident comments against the
// resend model at the scenario's own cadence (130 s redelivery, 590 s, max 2).
func TestTicketResendsFromIncidentComments(t *testing.T) {
	s := scenario.AlertingDayV1()
	var r4 scenario.PolicySpec
	for _, p := range s.Resend {
		if p.Key == "R4" {
			r4 = p
		}
	}
	origin := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	m := &cohortMember{alert: Alert{Name: "Campaign-c1-resend-ticket-1"}, policy: &policyState{spec: r4}, origin: origin}
	for at := time.Duration(0); at < 28*time.Minute; at += s.Redelivery {
		m.deliveries = append(m.deliveries, resend.Delivery{At: at})
	}
	exp, err := resend.Expect(r4, m.deliveries)
	if err != nil || len(exp.Resends) != 2 {
		t.Fatalf("model: %+v %v", exp, err)
	}
	stamp := func(d time.Duration) string { return origin.Add(d).Format("2006-01-02 15:04:05") }
	firing := func(d time.Duration) targetops.Comment {
		return targetops.Comment{Value: "OSCAR alert still firing: episode 1, occurrence 6.", CreatedOn: stamp(d)}
	}
	resolved := targetops.Comment{Value: "[OSCAR AUTO-UPDATE 2026-11-16] Alert has been resolved", CreatedOn: stamp(40 * time.Minute)}
	inc := func(cs ...targetops.Comment) []targetops.Incident {
		return []targetops.Incident{{Number: "INC1", ShortDescription: m.alert.Name, Comments: cs},
			{Number: "INC2", ShortDescription: m.alert.Name + "0", Comments: []targetops.Comment{firing(time.Minute)}}} // Campaign-...-10: not ours
	}
	ok := inc(targetops.Comment{Value: "created"}, firing(exp.Resends[0]+5*time.Second), firing(exp.Resends[1]+9*time.Second), resolved)
	for _, tc := range []struct {
		name              string
		incs              []targetops.Incident
		read              bool
		obs               int
		unread, violation string
	}{
		{"matches the model", ok, true, 2, "", ""},
		{"incidents never read", ok, false, 0, "not read", ""},
		{"no incident for the member", nil, true, 0, "no incident", ""},
		{"more than max_resends", inc(firing(exp.Resends[0]), firing(exp.Resends[1]), firing(exp.Resends[1]+10*time.Minute), resolved), true, 3, "", "max_resends 2"},
		{"resend after resolution", inc(firing(exp.Resends[0]), resolved, firing(41*time.Minute)), true, 2, "", "after resolution"},
	} {
		obs, unread, violation := ticketResends(m, tc.incs, tc.read)
		if len(obs) != tc.obs || !strings.Contains(unread, tc.unread) || (tc.unread == "") != (unread == "") ||
			!strings.Contains(violation, tc.violation) || (tc.violation == "") != (violation == "") {
			t.Errorf("%s: observed %v unread %q violation %q", tc.name, obs, unread, violation)
		}
	}
	obs, _, _ := ticketResends(m, ok, true)
	if obs[0] != exp.Resends[0]+5*time.Second || obs[1] != exp.Resends[1]+9*time.Second {
		t.Errorf("observed offsets %v, model %v", obs, exp.Resends)
	}
}
