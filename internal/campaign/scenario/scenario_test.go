package scenario

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// goldenHash pins alerting-day@v1. Re-baselined 2026-11-20 when the OSCAR owner
// raised the memory slope to 512 MiB/h and front-office p95 to 5s; runs before
// that carry 5cf7f1a0ff4d6dd43567fa440c7dc80183368ba9b6662dc79b8ab4004f8ff54e.
const goldenHash = "c37c420dcef4f6d3f5c72e77d3fca9536e1dbb711a7fe7819b8d696f4063a212"

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
	if s.Redelivery != 130*time.Second || s.ResendUntil != 118*time.Minute {
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
	if l.HistoryP95 != 40*time.Second || l.Drain != 15*time.Minute || l.ContainerSlopeMiBPerHour != 512 ||
		l.FrontOfficeP95 != 5*time.Second || l.FrontOfficeP99 != 5*time.Second || l.CorrelationSuccess != 1 || l.TicketSuccess != 1 || l.ResendSuccess != 1 ||
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
		if c := s.Correlation.SetB[i]; c.Pattern != b.Pattern || c.Level != "builtin" {
			t.Errorf("setB %d: %+v", i, c)
		}
	}
	if tp := s.Correlation.TicketedPatterns; len(tp) != 2 || tp[0] != "flood" || tp[1] != "threshold" {
		t.Fatalf("ticketed %v", tp)
	}
}

// Ruling C39: the bounded live smoke (spec 13.11, plan C19 step 6).
func TestAlertingDaySmokeV1(t *testing.T) {
	s := AlertingDaySmokeV1()
	if s.ID != "alerting-day-smoke" || s.Version != "v1" || !s.Custom {
		t.Fatalf("identity %s@%s custom=%v (the smoke must be custom: never comparable)", s.ID, s.Version, s.Custom)
	}
	if len(s.Phases) != 6 || s.Phase("preflight").Start != -2*time.Minute || s.Phase("drain").End != 12*time.Minute {
		t.Fatalf("phases %+v", s.Phases)
	}
	for _, p := range s.Phases {
		want := 2 * time.Minute
		if p.Name == "resend" { // activation waits ~90s for OSCAR cutover, unscaled
			want = 4 * time.Minute
		}
		if p.End-p.Start != want {
			t.Errorf("phase %s is %s, want %s", p.Name, p.End-p.Start, want)
		}
	}
	if s.Baseline.Rate != 1 || len(s.Resend) != 1 || len(s.Correlation.SetA) != 1 || len(s.Correlation.SetB) != 0 {
		t.Fatalf("scale: rate %v resend %d setA %d setB %d", s.Baseline.Rate, len(s.Resend), len(s.Correlation.SetA), len(s.Correlation.SetB))
	}
	if doc := corr.Builtin(s.Correlation.SetA[0].Pattern); len(doc.Cases) != 2 {
		t.Fatalf("set A case %+v must hold 2 correlation cases", s.Correlation.SetA[0])
	}
	if s.Hash() == goldenHash || AlertingDayV1().Hash() != goldenHash {
		t.Fatal("the smoke must not share or change the v1 hash")
	}
}

func TestLookup(t *testing.T) {
	for _, ref := range []string{"alerting-day@v1", "alerting-day@v2", "alerting-day-smoke@v1", "alerting-day-smoke@v2"} {
		s, ok := Lookup(ref)
		if !ok || s.ID+"@"+s.Version != ref {
			t.Errorf("%s: %v %s@%s", ref, ok, s.ID, s.Version)
		}
		if h, ok := ByHash(s.Hash()); !ok || h.Hash() != s.Hash() {
			t.Errorf("%s: by hash %v", ref, ok)
		}
	}
	if _, ok := Lookup("alerting-day@v9"); ok {
		t.Error("unknown scenario resolved")
	}
	if _, ok := ByHash(""); ok {
		t.Error("empty hash resolved")
	}
}

func TestAlertingDayV2AddsTheATTShapeWithoutTouchingV1(t *testing.T) {
	v1, v2 := AlertingDayV1(), AlertingDayV2()
	if v1.Hash() != goldenHash || v1.Standing != nil || v1.Breaker != nil {
		t.Fatal("v1 must stay frozen")
	}
	if v2.ID != "alerting-day" || v2.Version != "v2" || v2.Custom || v2.Hash() == goldenHash {
		t.Fatalf("v2 identity %s@%s", v2.ID, v2.Version)
	}
	if b := v2.Phase("breaker"); b.Start != 120*time.Minute || b.End != 130*time.Minute {
		t.Fatalf("breaker phase %+v", b)
	}
	if d := v2.Phase("drain"); d.Start != 130*time.Minute || d.End != 150*time.Minute {
		t.Fatalf("drain %+v", d)
	}
	st, br := v2.Standing, v2.Breaker
	if st == nil || st.Size != 2000 || st.Interval != 10*time.Minute || st.Until != 120*time.Minute || st.Instances != 500 || st.Concurrency < 1 {
		t.Fatalf("standing %+v", st)
	}
	// ~3.3 repeats/s on top of the 3.5/s baseline: close to AT&T's 7.8 webhooks/s.
	if rate := float64(st.Size)/st.Interval.Seconds() + v2.Baseline.Rate; rate < 6.5 || rate > 7.8 {
		t.Fatalf("offered %.1f/s", rate)
	}
	if br == nil || br.QueueHigh != 100 || br.QueueLow != 10 || br.ResolveProbes < 1 || br.BurstConcurrency < 1 {
		t.Fatalf("breaker %+v", br)
	}
	if v2.Limits.QuerySetVersion != "q2" || v2.Baseline.Rate != v1.Baseline.Rate || len(v2.Resend) != len(v1.Resend) {
		t.Fatal("v2 keeps v1's facets and adds q2")
	}
}

func TestAlertingDaySmokeV2IsShortCustomAndCarriesBothFacets(t *testing.T) {
	s := AlertingDaySmokeV2()
	if !s.Custom || s.Standing == nil || s.Breaker == nil || s.Phase("drain").End != 14*time.Minute {
		t.Fatalf("smoke v2 %+v", s)
	}
	if b := s.Phase("breaker"); b.End-b.Start != 2*time.Minute || s.Standing.Until > b.Start {
		t.Fatalf("breaker %+v standing until %s", b, s.Standing.Until)
	}
}

// resend-only@v1 isolates the four v1 resend policies against a 5/s lane, with
// no correlation or baseline ticketing, and keeps v1's limits.
func TestResendOnlyV1IsolatesResendAtFivePerSecond(t *testing.T) {
	s, ok := Lookup("resend-only@v1")
	if !ok || !s.Custom || s.Baseline.Rate != 5 || s.Baseline.TicketEveryNth != 0 {
		t.Fatalf("resend-only %v %+v", ok, s.Baseline)
	}
	v1 := AlertingDayV1()
	if !reflect.DeepEqual(s.Resend, v1.Resend) || s.Redelivery != v1.Redelivery || !reflect.DeepEqual(s.Limits, v1.Limits) {
		t.Fatal("resend-only must reuse v1's policies, redelivery and limits")
	}
	if len(s.Correlation.SetA)+len(s.Correlation.SetB) != 0 || s.Standing != nil || s.Breaker != nil || s.DeadTarget != nil {
		t.Fatal("resend-only runs only the lane and the resend cohorts")
	}
	r, d := s.Phase("resend"), s.Phase("drain")
	if r.End-r.Start != 30*time.Minute || d.Start != r.End || s.ResendUntil <= r.Start || s.ResendUntil > r.End-s.Redelivery+10*time.Second {
		t.Fatalf("resend %+v drain %+v until %s", r, d, s.ResendUntil)
	}
}

// The fingerprint lane is opt-in: no comparable built-in before v4 carries it
// (hashes, and so comparability, are unchanged; custom functional@v1 does), and a
// scenario that adds it hashes apart.
func TestFingerprintFacetIsOptional(t *testing.T) {
	for _, s := range Builtins() {
		if s.Fingerprint != nil && !s.Custom && s.Version != "v4" {
			t.Errorf("%s@%s carries the fingerprint lane", s.ID, s.Version)
		}
	}
	s := AlertingDayV1()
	s.Fingerprint = &FingerprintSpec{At: time.Minute, Repeats: 3, Converge: 2 * time.Minute, Verify: time.Minute}
	if s.Hash() == goldenHash || AlertingDayV1().Hash() != goldenHash {
		t.Fatal("the fingerprint facet must change the hash only of a scenario that sets it")
	}
}

// functional@v1 overlaps every functional lane under a 5/s base load and keeps
// v1's policies and limits.
func TestFunctionalV1OverlapsEveryLaneAtFivePerSecond(t *testing.T) {
	s, ok := Lookup("functional@v1")
	if !ok || !s.Custom || s.Baseline.Rate != 5 || s.Baseline.TicketEveryNth == 0 {
		t.Fatalf("functional %v %+v", ok, s.Baseline)
	}
	v1 := AlertingDayV1()
	if !reflect.DeepEqual(s.Resend, v1.Resend) || !reflect.DeepEqual(s.Correlation, v1.Correlation) || !reflect.DeepEqual(s.Limits, v1.Limits) {
		t.Fatal("functional must reuse v1's resend policies, correlation sets and limits")
	}
	if s.Standing != nil || s.Breaker != nil || s.DeadTarget != nil || s.Fingerprint == nil {
		t.Fatal("functional runs the functional lanes only, fingerprint included")
	}
	drain := s.Phase("drain").Start
	for _, name := range []string{"correlation", "ticketing", "resend"} {
		if p := s.Phase(name); p.Start != 5*time.Minute || p.End > drain {
			t.Fatalf("%s %+v must start at T+5 and end by the drain %s", name, p, drain)
		}
	}
	if s.Baseline.TicketFrom >= s.Phase("resend").End || s.Baseline.TicketTo > drain || s.ResendUntil > s.Phase("resend").End {
		t.Fatalf("tickets %s-%s, resend until %s", s.Baseline.TicketFrom, s.Baseline.TicketTo, s.ResendUntil)
	}
	if f := s.Fingerprint; f.At+f.Converge*4+f.Verify > drain {
		t.Fatalf("fingerprint lane %+v cannot finish before the drain %s", f, drain)
	}
}

func TestV3LoadsFor150MinutesBeforeDrain(t *testing.T) {
	for _, ref := range []string{"alerting-day-control@v1", "alerting-day@v3"} {
		s, ok := Lookup(ref)
		if !ok || s.LoadEnd != 150*time.Minute || s.Phase("drain").Start != s.LoadEnd ||
			s.Phase("drain").End-s.LoadEnd != 30*time.Minute || s.Standing == nil ||
			s.Standing.Until != s.LoadEnd || s.Baseline.Rate != 3.5 || s.Breaker != nil {
			t.Fatalf("%s: invalid load/drain plan: %+v", ref, s)
		}
		if s.NativeWorkload == nil || !reflect.DeepEqual(s.NativeWorkload.Cohorts,
			[]NativeCohort{NativeDirectNoPolicy, NativeCanonicalFirst, NativeCanonicalRepeat}) ||
			s.NativeWorkload.ConfiguredPolicy != (ref == "alerting-day@v3") {
			t.Fatalf("%s: native cohort plan %+v", ref, s.NativeWorkload)
		}
	}
	smoke, ok := Lookup("alerting-day-smoke@v3")
	if !ok || !smoke.Custom || smoke.LoadEnd >= 150*time.Minute || smoke.Phase("drain").Start != smoke.LoadEnd ||
		smoke.NativeWorkload == nil || !smoke.NativeWorkload.ConfiguredPolicy {
		t.Fatalf("V3 smoke: %+v", smoke)
	}
	control, v3 := AlertingDayControlV1(), AlertingDayV3()
	if len(control.Resend) != 0 || !reflect.DeepEqual(v3.Resend, AlertingDayV2().Resend) || len(smoke.Resend) != 1 {
		t.Fatal("V3 must retain ordinary resend coverage outside the before/after control")
	}
	if control.Limits.RegressionPct != 5 || v3.Limits.RegressionPct != 10 || control.Limits.FrontOfficeP95 != 2*time.Second {
		t.Fatalf("qualification budgets: control %+v, configured %+v", control.Limits, v3.Limits)
	}
}

func TestManagedAlertsSmokeScenarioIsIndependent(t *testing.T) {
	s, ok := Lookup("managed-alerts-smoke@v1")
	if !ok || !s.Custom || s.RequiresComparisonProvenance() || s.Baseline.Rate != 0 || s.Standing != nil ||
		s.LoadEnd != 20*time.Minute || s.Phase("drain").Start != s.LoadEnd || s.Phase("drain").End != 25*time.Minute ||
		s.NativeWorkload == nil || !s.NativeWorkload.ManagedTL1 || !s.NativeWorkload.ConfiguredPolicy ||
		s.Hash() == AlertingDaySmokeV3().Hash() {
		t.Fatalf("managed functional smoke: %+v", s)
	}
}

func TestOldScenarioHashesUnchanged(t *testing.T) {
	// Canonical JSON digests from the frozen pre-D1 selector definitions.
	for ref, want := range map[string]string{
		"alerting-day@v1":          goldenHash,
		"alerting-day@v2":          "54cdc896e4c48e3b1deb136c9c611cf2f71671889083d340f8e9655b56a67779",
		"alerting-day-smoke@v1":    "290086b9a03cff29787dcd90d91d9c9d068f2cc70a047ee5e6a7c0adee6ea7c4",
		"alerting-day-smoke@v2":    "ac083024d4eab0fdcb15b8d3ecc4a523d45deef0e5556afcf12a18cb8d718ddb",
		"delivery-retry@v1":        "e83410253bdab9181e8281b8c5a4582b1a197de54b4d40131e7c827db83d1214",
		"resend-only@v1":           "00446b930573d0bcb37d754b9155828b9453464bcb964a90400c5fa45bf9927b",
		"functional@v1":            "7eab7a3cf772a77beee199b92cd3631ce54d0606a45ac46fad0dc68074a9c8c2",
		"functional-burst@v1":      "a7b95af5e8a965c469352aa0005bae1fcad42369569753fd711572df6bbfd5b9",
		"functional-burst-full@v1": "002cc0a03a39702c69618db01e193e09fb236c164c2a9beed93501d9c0b2781b",
	} {
		t.Run(ref, func(t *testing.T) {
			s, ok := Lookup(ref)
			if !ok || s.Hash() != want {
				t.Fatalf("hash %s want %s", s.Hash(), want)
			}
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["loadEnd"] != nil || fields["nativeWorkload"] != nil {
				t.Fatal("optional qualification fields leaked into a frozen scenario")
			}
		})
	}
}

// TestV4OfferedRateBudget: v4 is the single-node gate (3-5/s sustained,
// spikes to 10/s). Every lane counts: the worker lane plus the controller
// lanes must offer about 5/s, never more, and at most 10/s in a spike.
func TestV4OfferedRateBudget(t *testing.T) {
	for _, s := range []Scenario{AlertingDayV4(), AlertingDaySmokeV4()} {
		ctl := s.ControllerFiringRate()
		total := s.Baseline.Rate + ctl
		if total > V4TotalRate+1e-9 || s.ID == "alerting-day" && total < V4TotalRate-0.05 {
			t.Errorf("%s@%s: total %.3f/s (worker %.2f + controller %.3f), want about %.0f/s", s.ID, s.Version, total, s.Baseline.Rate, ctl, V4TotalRate)
		}
		if len(s.Baseline.Bursts) != 2 || s.Standing != nil || s.Breaker != nil || s.DeadTarget != nil {
			t.Errorf("%s@%s: two spikes and no standing pool, breaker or dead target", s.ID, s.Version)
		}
		for _, b := range s.Baseline.Bursts {
			if spike := b.Rate + s.ControllerSpikeRate(); spike > V4SpikeRate+1e-9 || spike < V4SpikeRate-0.05 {
				t.Errorf("%s@%s: spike total %.3f/s, want 10/s", s.ID, s.Version, spike)
			}
		}
	}
	s := AlertingDayV4()
	if b := s.Baseline.Bursts; b[0].At != 45*time.Minute || b[1].At != 105*time.Minute || b[0].For != 5*time.Minute || b[1].For != 5*time.Minute {
		t.Fatalf("spikes %+v", b)
	}
	if c := s.Correlation; c.Rate/V4TotalRate != 0.2 || c.SpikeRate/V4SpikeRate != 0.2 || c.Parallel < 2 {
		t.Fatalf("correlation must be 20%% of the total in and out of spikes: %+v", c)
	}
	// Shares of the 5/s: resend about 10%, fingerprint 5%, tickets about 5%.
	resend := 0.0
	for _, p := range s.Resend {
		resend += float64(p.Cohort) / s.Redelivery.Seconds()
	}
	fp := float64(s.Fingerprint.Repeats+1) / s.Fingerprint.Every.Seconds()
	tickets := s.Baseline.Rate * s.Baseline.UniqueRatio / float64(s.Baseline.TicketEveryNth)
	if resend < 0.45 || resend > 0.55 || fp != 0.25 || tickets < 0.22 || tickets > 0.27 {
		t.Fatalf("shares of 5/s: resend %.3f fingerprint %.3f tickets %.3f", resend, fp, tickets)
	}
}

// TestV4RunsEveryLaneConcurrently: one 150-minute load window with every lane
// inside it, a 30-minute drain, comparable, with a compressed smoke.
func TestV4RunsEveryLaneConcurrently(t *testing.T) {
	s, ok := Lookup("alerting-day@v4")
	if !ok || s.Custom || s.RequiresComparisonProvenance() || s.Seed != AlertingDayV1().Seed || s.Baseline.Instances != 50 {
		t.Fatalf("v4: %+v", s)
	}
	drain := s.Phase("drain")
	if drain.Start != 150*time.Minute || drain.End != 180*time.Minute {
		t.Fatalf("drain %+v", drain)
	}
	for _, name := range []string{"baseline", "correlation", "ticketing", "resend"} {
		if p := s.Phase(name); p.End-p.Start < 140*time.Minute || p.End > drain.Start {
			t.Errorf("%s %+v must span the load window", name, p)
		}
	}
	if !s.Correlation.Cycle || !s.Correlation.ParentTickets || len(s.Correlation.SetA) != len(AlertingDayV1().Correlation.SetA) {
		t.Errorf("correlation %+v", s.Correlation)
	}
	if f := s.Fingerprint; f.Every <= f.Verify || f.At+f.Converge+f.Every > 10*time.Minute {
		t.Errorf("fingerprint %+v", f)
	}
	var esc *PolicySpec
	for i, p := range s.Resend {
		if p.Escalation != nil {
			esc = &s.Resend[i]
		}
	}
	if esc == nil || esc.Escalation.TargetSeverity != "critical" || esc.Escalation.After > 5*time.Minute ||
		esc.Episode < esc.Escalation.After+3*time.Minute || int((s.ResendUntil-s.Phase("resend").Start)/esc.Episode) < 5 || len(s.Resend) != 5 {
		t.Errorf("escalation policy %+v must fire several times per member", esc)
	}
	if s.Tasks == nil || s.Tasks.Every != 5*time.Minute || s.Tasks.Timeout >= s.Tasks.Every || s.Limits.TaskSuccess != 1 || s.Limits.TaskP95 == 0 {
		t.Errorf("tasks %+v limits %+v", s.Tasks, s.Limits)
	}
	if s.Report == nil || len(s.Report.At) != 2 || s.Report.At[0] != 60*time.Minute || s.Report.At[1] != 120*time.Minute ||
		s.Report.At[1]-s.Report.At[0] <= s.Report.Timeout {
		t.Errorf("report %+v", s.Report)
	}
	if s.Limits.HistoryP95 != AlertingDayV3().Limits.HistoryP95 || s.Limits.FrontOfficeP95 != AlertingDayV3().Limits.FrontOfficeP95 || s.FrontOffice != AlertingDayV1().FrontOffice {
		t.Errorf("limits and front office follow v3/v1: %+v", s.Limits)
	}
	smoke, ok := Lookup("alerting-day-smoke@v4")
	if !ok || !smoke.Custom || smoke.Phase("drain").End > 16*time.Minute || smoke.Phase("drain").End < 14*time.Minute ||
		smoke.Hash() == s.Hash() || smoke.Tasks == nil || smoke.Report == nil || smoke.Fingerprint == nil {
		t.Fatalf("v4 smoke: %+v", smoke)
	}
	if r := smoke.Report; r.At[0]+r.Timeout > smoke.Phase("drain").End {
		t.Errorf("smoke report %+v cannot finish", r)
	}
}

func TestValidateSkipLanes(t *testing.T) {
	if err := ValidateSkipLanes(AlertingDayV4(), []string{"report"}); err != nil {
		t.Fatal(err)
	}
	if ValidateSkipLanes(AlertingDayV4(), []string{"fingerprint"}) == nil || ValidateSkipLanes(AlertingDayV3(), []string{"report"}) == nil {
		t.Fatal("only a lane the scenario has, and that may be skipped, is accepted")
	}
	if ValidateSkipLanes(AlertingDayV4(), nil) != nil {
		t.Fatal("no skip is the default")
	}
}
