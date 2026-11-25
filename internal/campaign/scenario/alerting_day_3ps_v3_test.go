package scenario

import (
	"testing"
	"time"
)

// TestReleasedHashesUnchanged (OSC-207 AC-9) pins every scenario released
// before v3 (CorrTest 2e3a9f3): the v3 facets are omitempty fields.
func TestReleasedHashesUnchanged(t *testing.T) {
	for ref, want := range map[string]string{
		"alerting-day@v1":           "c37c420dcef4f6d3f5c72e77d3fca9536e1dbb711a7fe7819b8d696f4063a212",
		"alerting-day@v2":           "54cdc896e4c48e3b1deb136c9c611cf2f71671889083d340f8e9655b56a67779",
		"alerting-day-smoke@v1":     "290086b9a03cff29787dcd90d91d9c9d068f2cc70a047ee5e6a7c0adee6ea7c4",
		"alerting-day-smoke@v2":     "ac083024d4eab0fdcb15b8d3ecc4a523d45deef0e5556afcf12a18cb8d718ddb",
		"delivery-retry@v1":         "e83410253bdab9181e8281b8c5a4582b1a197de54b4d40131e7c827db83d1214",
		"resend-only@v1":            "00446b930573d0bcb37d754b9155828b9453464bcb964a90400c5fa45bf9927b",
		"functional@v1":             "7eab7a3cf772a77beee199b92cd3631ce54d0606a45ac46fad0dc68074a9c8c2",
		"functional-burst@v1":       "a7b95af5e8a965c469352aa0005bae1fcad42369569753fd711572df6bbfd5b9",
		"functional-burst-full@v1":  "002cc0a03a39702c69618db01e193e09fb236c164c2a9beed93501d9c0b2781b",
		"alerting-day-control@v1":   "b6355431bc90f1f29ec705e0d06d4f718b4d0509b60a0dadc25fcb14bb1346dd",
		"alerting-day@v3":           "c300e0322006c919ce963c7d610e11cba1df58fbdfc75b55639471f86f4515d5",
		"alerting-day-smoke@v3":     "fcfe1bf9685846e2875373dffc848d73e7f3725d52a0844e66c60191559d3b9b",
		"managed-alerts-smoke@v1":   "bb0e784d710767e48bf49dc3f7dcb4a92c7ffcd23aed3fe134bd7999c2fcd304",
		"alerting-day@v4":           "a7f2f3a4a4e3289ee623ce84fc05b93ce9db97f4afe559c1d76bb5148327072e",
		"alerting-day-smoke@v4":     "72830959021dc3d377a292bd51ebd198e6968c7dcc3bbc2e996e284088e01cd3",
		"alerting-day-3ps@v1":       "1f1b3b00fb03246a9b49d19cf7156aebed7dcec3aef3693d5f7f85d75016cd87",
		"alerting-day-3ps-smoke@v1": "d5a46ddd3d445db88e4b37b228b4c3f5a2718f99d65e2778f1779b9f603d17b0",
		"alerting-day-3ps@v2":       "6bd5fc68b9227b92041da51b3f49bada3a351d5cdc5917891466343b696dee4c",
		"alerting-day-3ps-smoke@v2": "34f8dc339d1110448c1074753e5a0ab4c2adf2dccc834942f0ec93c7acdff041",
	} {
		if s, ok := Lookup(ref); !ok || s.Hash() != want {
			t.Errorf("%s hash %s want %s", ref, s.Hash(), want)
		}
	}
}

// TestAlertingDay3psV3IsV2WithSourcesAndReceivers (OSC-207 AC-1, AC-2, AC-5,
// AC-8): v3 is v2 plus the server labels, the source filter actions and the
// receivers lane; nothing else differs, and no other scenario has them.
func TestAlertingDay3psV3IsV2WithSourcesAndReceivers(t *testing.T) {
	for _, tc := range []struct{ v3, v2 string }{{"alerting-day-3ps@v3", "alerting-day-3ps@v2"}, {"alerting-day-3ps-smoke@v3", "alerting-day-3ps-smoke@v2"}} {
		v3, ok := Lookup(tc.v3)
		v2, _ := Lookup(tc.v2)
		if !ok || v3.Baseline.Servers != 32 || v3.Correlation.Servers != 8 || !v3.FrontOffice.SourceFilters || v3.Receivers == nil || v3.Custom != v2.Custom {
			t.Fatalf("%s: %+v", tc.v3, v3)
		}
		back := v3
		back.Version = v2.Version
		back.Baseline.Servers, back.Correlation.Servers, back.FrontOffice.SourceFilters, back.Receivers = 0, 0, false, nil
		if back.Hash() != v2.Hash() {
			t.Errorf("%s differs from %s beyond the v3 facets", tc.v3, tc.v2)
		}
		if err := ValidateSkipLanes(v3, []string{"receivers", "maintenance", "report"}); err != nil {
			t.Error(err)
		}
	}
	for _, s := range Builtins() {
		v3 := s.Version == "v3" && (s.ID == "alerting-day-3ps" || s.ID == "alerting-day-3ps-smoke")
		if has := s.Baseline.Servers > 0 || s.Correlation.Servers > 0 || s.FrontOffice.SourceFilters || s.Receivers != nil; has != v3 {
			t.Errorf("%s@%s v3 facets %v", s.ID, s.Version, has)
		}
	}
	if ValidateSkipLanes(AlertingDay3psV2(), []string{"receivers"}) == nil {
		t.Error("v2 has no receivers lane to skip")
	}
}

// TestAlertingDay3psV3Receivers (AC-5): probes start after the lane setup,
// the read-back (Until + Bound + 30 s) ends before the drain, Every is a few
// probes a minute, and the 40-server scheme stays inside 198.51.100.0/24.
func TestAlertingDay3psV3Receivers(t *testing.T) {
	for _, ref := range []string{"alerting-day-3ps@v3", "alerting-day-3ps-smoke@v3"} {
		s, _ := Lookup(ref)
		rx := s.Receivers
		if rx.From <= 0 || rx.Until <= rx.From || rx.Every < 10*time.Second || rx.Every > time.Minute || rx.Bound != time.Minute ||
			rx.Until+rx.Bound+30*time.Second > s.Phase("drain").Start {
			t.Errorf("%s receivers %+v (drain %v)", ref, rx, s.Phase("drain").Start)
		}
		if n := s.Baseline.Servers + s.Correlation.Servers; n != 40 || 10+n > 254 {
			t.Errorf("%s servers %d", ref, n)
		}
	}
}
