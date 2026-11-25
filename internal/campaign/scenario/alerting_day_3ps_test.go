package scenario

import (
	"math"
	"testing"
	"time"
)

// TestV4HashesUnchanged (OSC-184 AC-4) pins alerting-day@v4 and its smoke at
// d7f5a64: per-scenario offered totals must not change them.
func TestV4HashesUnchanged(t *testing.T) {
	for ref, want := range map[string]string{
		"alerting-day@v4":       "a7f2f3a4a4e3289ee623ce84fc05b93ce9db97f4afe559c1d76bb5148327072e",
		"alerting-day-smoke@v4": "72830959021dc3d377a292bd51ebd198e6968c7dcc3bbc2e996e284088e01cd3",
	} {
		if s, ok := Lookup(ref); !ok || s.Hash() != want {
			t.Errorf("%s hash %s want %s", ref, s.Hash(), want)
		}
	}
}

// TestAlertingDay3psIsV4AtThreePerSecond (OSC-184 AC-1, AC-3): v4's lanes,
// phases, task lane, report, front office, homelab gate, pre-check limits,
// limits and seed, at a total of 3/s with 6/s 1-minute spikes at v4's spike
// starts and v4's lane shares.
func TestAlertingDay3psIsV4AtThreePerSecond(t *testing.T) {
	s, ok := Lookup("alerting-day-3ps@v1")
	v4 := AlertingDayV4()
	if !ok || s.Custom || !s.Paced {
		t.Fatalf("3ps: %+v", s)
	}
	total, spike := s.OfferedTotals()
	if total != 3 || spike != 6 {
		t.Fatalf("offered totals %v/%v, want 3/6", total, spike)
	}
	if got := s.Baseline.Rate + s.ControllerFiringRate(); got > total+1e-9 || got < total-0.05 {
		t.Errorf("total %.3f/s (worker %.2f), want about 3/s", got, s.Baseline.Rate)
	}
	b := s.Baseline.Bursts
	if len(b) != 2 || b[0].At != 45*time.Minute || b[1].At != 105*time.Minute || b[0].For != time.Minute || b[1].For != time.Minute {
		t.Fatalf("spikes %+v, want 1 minute at T+45 and T+105", b)
	}
	for _, x := range b {
		if got := x.Rate + s.ControllerSpikeRate(); got > spike+1e-9 || got < spike-0.05 {
			t.Errorf("spike total %.3f/s, want about 6/s", got)
		}
	}
	// v4's shares of the total: correlation 20% (in and out of spikes),
	// fingerprint 5%, resend about 10%, tickets about 5%.
	c := s.Correlation
	resend := 0.0
	for _, p := range s.Resend {
		resend += float64(p.Cohort) / s.Redelivery.Seconds()
	}
	fp := float64(s.Fingerprint.Repeats+1) / s.Fingerprint.Every.Seconds()
	tickets := s.Baseline.Rate * s.Baseline.UniqueRatio / float64(s.Baseline.TicketEveryNth)
	if math.Abs(c.Rate/total-0.2) > 1e-9 || math.Abs(c.SpikeRate/spike-0.2) > 1e-9 || math.Abs(fp/total-0.05) > 1e-9 ||
		resend/total < 0.095 || resend/total > 0.105 || tickets/total < 0.045 || tickets/total > 0.055 {
		t.Errorf("shares of 3/s: correlation %.3f/%.3f fingerprint %.3f resend %.3f tickets %.3f", c.Rate/total, c.SpikeRate/spike, fp/total, resend/total, tickets/total)
	}
	// Everything else is v4's: put back the scaled values and the hash is v4's.
	back := s
	back.ID, back.Version, back.OfferedRate, back.OfferedSpikeRate = v4.ID, v4.Version, 0, 0
	back.Baseline.Rate, back.Baseline.Bursts = v4.Baseline.Rate, v4.Baseline.Bursts
	back.Correlation.Rate, back.Correlation.SpikeRate = v4.Correlation.Rate, v4.Correlation.SpikeRate
	back.Resend = append([]PolicySpec(nil), s.Resend...)
	for i := range back.Resend {
		back.Resend[i].Cohort = v4.Resend[i].Cohort
	}
	fpBack := *s.Fingerprint
	fpBack.Repeats = v4.Fingerprint.Repeats
	back.Fingerprint = &fpBack
	if back.Hash() != v4.Hash() {
		t.Errorf("3ps differs from v4 beyond the rate-scaled values")
	}
	if err := ValidateSkipLanes(s, []string{"report"}); err != nil {
		t.Errorf("--skip-lane report: %v", err)
	}
	// Each total is in the hash on its own.
	for _, mut := range []func(*Scenario){func(x *Scenario) { x.OfferedRate = 4 }, func(x *Scenario) { x.OfferedSpikeRate = 7 }} {
		m := s
		mut(&m)
		if m.Hash() == s.Hash() {
			t.Errorf("an offered total is missing from the hash: %+v", m)
		}
	}
	if total, spike := v4.OfferedTotals(); total != V4TotalRate || spike != V4SpikeRate {
		t.Errorf("v4 totals %v/%v, want 5/10", total, spike)
	}
	if total, spike := (Scenario{OfferedRate: 3}).OfferedTotals(); total != 3 || spike != V4SpikeRate {
		t.Errorf("per-field fallback: %v/%v", total, spike)
	}
}

// TestAlertingDay3psSmoke (OSC-184 AC-2): the v4 smoke at 3/s and 6/s
// 1-minute spikes; only the worker lane changes, and it is never comparable.
func TestAlertingDay3psSmoke(t *testing.T) {
	s, ok := Lookup("alerting-day-3ps-smoke@v1")
	v4 := AlertingDaySmokeV4()
	if !ok || !s.Custom {
		t.Fatalf("3ps smoke: %+v", s)
	}
	total, spike := s.OfferedTotals()
	if total != 3 || spike != 6 {
		t.Fatalf("offered totals %v/%v", total, spike)
	}
	if got := s.Baseline.Rate + s.ControllerFiringRate(); got > 3+1e-9 || got < 2.95 {
		t.Errorf("total %.3f/s", got)
	}
	for _, b := range s.Baseline.Bursts {
		if got := b.Rate + s.ControllerSpikeRate(); got > 6+1e-9 || got < 5.95 {
			t.Errorf("spike total %.3f/s", got)
		}
	}
	back := s
	back.ID, back.Version, back.OfferedRate, back.OfferedSpikeRate = v4.ID, v4.Version, 0, 0
	back.Baseline.Rate = v4.Baseline.Rate
	back.Baseline.Bursts = append([]BurstSpec(nil), s.Baseline.Bursts...)
	for i := range back.Baseline.Bursts {
		back.Baseline.Bursts[i].Rate = v4.Baseline.Bursts[i].Rate
	}
	if back.Hash() != v4.Hash() {
		t.Errorf("3ps smoke differs from smoke v4 beyond the worker rate")
	}
}
