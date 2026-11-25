package scenario

import (
	"testing"
	"time"
)

// TestAlertingDay3psV1HashesUnchanged (OSC-189 AC-3) pins the released 3ps
// v1 scenarios (CorrTest 7ebf90c): smoothing ships as v2.
func TestAlertingDay3psV1HashesUnchanged(t *testing.T) {
	for ref, want := range map[string]string{
		"alerting-day-3ps@v1":       "1f1b3b00fb03246a9b49d19cf7156aebed7dcec3aef3693d5f7f85d75016cd87",
		"alerting-day-3ps-smoke@v1": "d5a46ddd3d445db88e4b37b228b4c3f5a2718f99d65e2778f1779b9f603d17b0",
	} {
		if s, ok := Lookup(ref); !ok || s.Hash() != want {
			t.Errorf("%s hash %s want %s", ref, s.Hash(), want)
		}
	}
}

// TestAlertingDay3psV2IsV1Smoothed (OSC-189 AC-1, AC-2): v2 is v1 with
// smoothed correlation pacing and the maintenance lane; the flag is in the hash.
func TestAlertingDay3psV2IsV1Smoothed(t *testing.T) {
	for _, tc := range []struct {
		v2, v1   string
		parallel int
	}{{"alerting-day-3ps@v2", "alerting-day-3ps@v1", 64}, {"alerting-day-3ps-smoke@v2", "alerting-day-3ps-smoke@v1", 8}} {
		v2, ok := Lookup(tc.v2)
		v1, _ := Lookup(tc.v1)
		if !ok || !v2.SmoothCorrelationOffers || v1.SmoothCorrelationOffers || v2.Correlation.Parallel != tc.parallel || v2.Custom != v1.Custom {
			t.Fatalf("%s: %+v", tc.v2, v2)
		}
		back := v2
		back.Version, back.SmoothCorrelationOffers = v1.Version, false
		back.Maintenance = nil
		if back.Hash() != v1.Hash() {
			t.Errorf("%s differs from %s beyond smoothing and the maintenance lane", tc.v2, tc.v1)
		}
	}
	for _, s := range Builtins() {
		if s.SmoothCorrelationOffers && s.ID != "alerting-day-3ps" && s.ID != "alerting-day-3ps-smoke" {
			t.Errorf("%s@%s smooths correlation", s.ID, s.Version)
		}
	}
}

// TestAlertingDay3psV2Maintenance (OSC-189 AC-5): only the 3ps v2 scenarios
// carry the maintenance lane; setup precedes the window, inside probes fit
// before the end, the after probes fit before the drain plus a minute, and
// neither the setup (its first 30 s) nor the window and probes overlap a spike.
func TestAlertingDay3psV2Maintenance(t *testing.T) {
	for _, s := range Builtins() {
		has := s.Maintenance != nil
		if want := s.Version == "v2" && (s.ID == "alerting-day-3ps" || s.ID == "alerting-day-3ps-smoke"); has != want {
			t.Errorf("%s@%s maintenance lane %v", s.ID, s.Version, has)
		}
		if !has {
			continue
		}
		m := s.Maintenance
		end := m.Start + m.For
		if m.At >= m.Start-30*time.Second || m.Start+5*time.Second > end-m.Lag || m.Every <= 0 || end+time.Minute+5*time.Second > s.Phase("drain").Start+time.Minute {
			t.Errorf("%s@%s maintenance %+v", s.ID, s.Version, m)
		}
		for _, b := range s.Baseline.Bursts {
			if b.At < end+70*time.Second && m.Start < b.At+b.For || b.At < m.At+30*time.Second && m.At < b.At+b.For {
				t.Errorf("%s@%s maintenance %+v overlaps spike %+v", s.ID, s.Version, m, b)
			}
		}
		if err := ValidateSkipLanes(s, []string{"maintenance", "report"}); err != nil {
			t.Error(err)
		}
	}
	if ValidateSkipLanes(AlertingDay3psV1(), []string{"maintenance"}) == nil {
		t.Error("v1 has no maintenance lane to skip")
	}
}
