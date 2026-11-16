package lane

import (
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

// The smoke plans exactly 2 baseline tickets (ruling C39); the plan is
// deterministic, so this holds for every run id.
func TestSmokePlansTwoTickets(t *testing.T) {
	s := scenario.AlertingDaySmokeV1()
	if p := Compile("camp_any", s.Seed, s.Baseline, s.Phase("drain").Start); p.Tickets != 2 {
		t.Fatalf("smoke plans %d tickets, want 2", p.Tickets)
	}
}
