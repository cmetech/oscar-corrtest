package lane

import (
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"math"
	"testing"
)

func TestConfiguredShareUsesInWindowWirePlan(t *testing.T) {
	s := scenario.AlertingDaySmokeV3()
	before := s.Hash()
	v := 0.1
	effective, err := ConfigureShare(s, &v)
	if err != nil {
		t.Fatal(err)
	}
	b := BaselineOffers(s)
	if b <= int(s.Baseline.Rate*s.LoadEnd.Seconds()) {
		t.Fatal("omitted clears")
	}
	n := effective.NativeWorkload.ManagedRate * s.LoadEnd.Seconds()
	if math.Abs(n/(n+float64(b))-v) > 1e-12 || s.Hash() != before || effective.Hash() == before || !RegisteredWorkload(effective, effective.Hash()) {
		t.Fatal("effective share/hash")
	}
	effective.LoadEnd--
	if RegisteredWorkload(effective, effective.Hash()) {
		t.Fatal("accepted altered workload")
	}
	for _, v := range []float64{-1, 0, 1, 0.9999999, 1e-12, math.NaN(), math.Inf(1)} {
		if _, err := ConfigureShare(s, &v); err == nil {
			t.Fatal("invalid share", v)
		}
	}
	for _, s := range []scenario.Scenario{scenario.AlertingDayControlV1(), scenario.ManagedAlertsSmokeV1(), scenario.AlertingDayV2()} {
		if _, err := ConfigureShare(s, &v); err == nil {
			t.Fatal("unexpected applicable share", s.ID)
		}
	}
}
