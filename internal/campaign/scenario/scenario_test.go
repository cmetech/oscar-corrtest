package scenario

import (
	"testing"
	"time"

	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

const goldenHash = "6fe2fa26020caf0c86fe61991d4bee5c22b8c1a0722ae0f4e10cc54da6f24d93"

func TestAlertingDayV1Values(t *testing.T) {
	s := AlertingDayV1()
	if s.ID != "alerting-day" || s.Version != "v1" || s.Custom {
		t.Fatalf("identity: %+v", s)
	}
	b := s.Baseline
	if b.Rate != 3.5 || b.UniqueRatio != 0.9 || b.PoolSize != 100 || b.Instances != 50 || b.ResolveAfter != 15*time.Minute ||
		b.TicketEveryNth != 20 || b.TicketFrom != 60*time.Minute || b.TicketTo != 120*time.Minute {
		t.Fatalf("baseline: %+v", b)
	}
	if sev := b.Severity; sev["critical"] != 0.05 || sev["major"] != 0.15 || sev["minor"] != 0.40 || sev["warning"] != 0.40 || len(sev) != 4 {
		t.Fatalf("severity: %v", sev)
	}
	want := []Phase{{"preflight", -10 * time.Minute, 0}, {"baseline", 0, 30 * time.Minute}, {"correlation", 30 * time.Minute, 60 * time.Minute},
		{"ticketing", 60 * time.Minute, 90 * time.Minute}, {"resend", 90 * time.Minute, 120 * time.Minute}, {"drain", 120 * time.Minute, 150 * time.Minute}}
	if len(s.Phases) != len(want) {
		t.Fatalf("phases: %+v", s.Phases)
	}
	for i, p := range want {
		if s.Phases[i] != p {
			t.Errorf("phase %d: %+v want %+v", i, s.Phases[i], p)
		}
	}
	if r := s.Phase("resend"); r.Start != 90*time.Minute || r.End != 120*time.Minute {
		t.Fatalf("resend: %+v", r)
	}
	if s.Redelivery != time.Minute || s.ResendUntil != 118*time.Minute {
		t.Fatal("redelivery")
	}
	f := s.FrontOffice
	if f.Users != 4 || f.Interval != 15*time.Second || f.SearchEvery != 2*time.Minute || f.Jitter <= 0 {
		t.Fatalf("front office: %+v", f)
	}
	if len(s.Resend) != 4 {
		t.Fatal("resend policies")
	}
	for _, p := range s.Resend {
		if p.Cohort != 10 {
			t.Errorf("%s cohort %d", p.Key, p.Cohort)
		}
	}
	r1, r2, r3, r4 := s.Resend[0], s.Resend[1], s.Resend[2], s.Resend[3]
	if r1.Key != "R1" || r1.Notifier != "CAMPAIGN_SINK" || r1.Action != "notification" || r1.Mode != "time" || r1.Interval != 110*time.Second || *r1.MaxResends != 5 || r1.Fresh {
		t.Errorf("R1 %+v", r1)
	}
	if r2.Key != "R2" || r2.Mode != "occurrences" || r2.Every != 3 || r2.MaxResends != nil || !r2.Fresh || r2.Notifier != "CAMPAIGN_SINK" || r2.Action != "notification" {
		t.Errorf("R2 %+v", r2)
	}
	if r3.Key != "R3" || r3.Mode != "either" || r3.Interval != 300*time.Second || r3.Every != 5 || *r3.MaxResends != 2 || !r3.Fresh || r3.Notifier != "CAMPAIGN_SINK" {
		t.Errorf("R3 %+v", r3)
	}
	if r4.Key != "R4" || r4.Notifier != "ALERTHISTORY" || r4.Action != "ticket" || r4.Mode != "time" || r4.Interval != 590*time.Second || *r4.MaxResends != 2 || r4.Fresh {
		t.Errorf("R4 %+v", r4)
	}
	l := s.Limits
	if l.HistoryP95 != 40*time.Second || l.Drain != 15*time.Minute || l.ContainerSlopeMiBPerHour != 100 ||
		l.FrontOfficeP95 != 2*time.Second || l.FrontOfficeP99 != 5*time.Second || l.CorrelationSuccess != 1 || l.TicketSuccess != 1 || l.ResendSuccess != 1 ||
		l.RegressionPct != 20 || l.QuerySetVersion != "q1" {
		t.Errorf("limits %+v", l)
	}
	p := s.Precheck
	if p.IdleQueueDepth != 50 || p.IdleQueueAge != 30*time.Second || p.ClockSkew != 250*time.Millisecond || p.TargetDiskFreePct != 20 ||
		p.TargetMemAvailPct != 25 || p.RunnerDiskFreeGiB != 10 || p.MetricsFreshness != 60*time.Second || p.RestartWindow != 30*time.Minute {
		t.Errorf("precheck %+v", p)
	}
}

func TestHashStable(t *testing.T) {
	if h := AlertingDayV1().Hash(); h != goldenHash {
		t.Fatalf("hash %s want %s", h, goldenHash)
	}
	mut := []func(*Scenario){
		func(s *Scenario) { s.Baseline.Rate = 4 },
		func(s *Scenario) { s.Limits.QuerySetVersion = "q2" },
		func(s *Scenario) { s.Resend[2].Fresh = false },
		func(s *Scenario) { s.Resend[1].MaxResends = new(int) },
		func(s *Scenario) { s.Seed++ },
		func(s *Scenario) { s.Phases[0].End = time.Second },
		func(s *Scenario) { s.Baseline.Severity["major"] = 0.2 },
		func(s *Scenario) { s.Correlation.SetB = s.Correlation.SetB[1:] },
	}
	for i, m := range mut {
		s := AlertingDayV1()
		m(&s)
		if s.Hash() == goldenHash {
			t.Errorf("mutation %d did not change hash", i)
		}
	}
}

func TestSetAContainsAllBuiltinsAndExamples(t *testing.T) {
	s := AlertingDayV1()
	a := s.Correlation.SetA
	if len(a) != 24 {
		t.Fatalf("SetA %d", len(a))
	}
	for i, b := range corr.AllBuiltins() {
		if a[i] != (CaseRef{Pattern: b.Pattern, Level: "builtin"}) {
			t.Errorf("builtin %d: %+v", i, a[i])
		}
	}
	for i, e := range corr.AllExamples() {
		if a[8+i] != (CaseRef{Pattern: e.Pattern, Level: e.Level}) {
			t.Errorf("example %d: %+v", i, a[8+i])
		}
	}
	if len(s.Correlation.SetB) != 8 {
		t.Fatalf("SetB %d", len(s.Correlation.SetB))
	}
	for i, b := range corr.AllBuiltins() {
		if c := s.Correlation.SetB[i]; c.Pattern != b.Pattern || c.Level != "builtin" || c.Case != "P01" {
			t.Errorf("setB %d: %+v", i, c)
		}
	}
	if tp := s.Correlation.TicketedPatterns; len(tp) != 2 || tp[0] != "flood" || tp[1] != "threshold" {
		t.Fatalf("ticketed %v", tp)
	}
}
