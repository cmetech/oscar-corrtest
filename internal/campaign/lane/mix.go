package lane

import (
	"errors"
	"math"
	"sort"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

// BaselineOffers counts scheduled source posts inside [T0,loadEnd), including
// resolves. Run identity changes labels but never this seeded schedule.
func BaselineOffers(s scenario.Scenario) int {
	plan := Compile("shape", s.Seed, s.Baseline, s.LoadEnd)
	return sort.Search(len(plan.Events), func(i int) bool { return plan.Events[i].At >= s.LoadEnd })
}

func ConfigureShare(s scenario.Scenario, share *float64) (scenario.Scenario, error) {
	if share == nil {
		return s, nil
	}
	w := s.NativeWorkload
	if w == nil || w.ManagedSegment <= 0 || math.IsNaN(*share) || !(*share > 0 && *share < 1) {
		return s, errors.New("managed share must be a fraction between 0 and 1 on a mixed V3 selector")
	}
	n := BaselineOffers(s)
	rate := float64(n) * *share / (1 - *share) / s.LoadEnd.Seconds()
	if rate > 100 || int(rate*s.LoadEnd.Seconds()) < 3 {
		return s, errors.New("managed share exceeds 100 PDUs/s or produces no complete lifecycle")
	}
	copy := *w
	copy.ManagedRate, copy.ManagedShare = rate, *share
	s.NativeWorkload = &copy
	return s, nil
}

// Validate the full saved workload against the versioned selector and its sole
// permitted override. Reject copied hashes and edited timing or policy fields.
func RegisteredWorkload(s scenario.Scenario, hash string) bool {
	base, ok := scenario.Lookup(s.ID + "@" + s.Version)
	if !ok || hash == "" || s.Hash() != hash {
		return false
	}
	if w := s.NativeWorkload; w != nil && w.ManagedShare != 0 {
		var err error
		base, err = ConfigureShare(base, &w.ManagedShare)
		if err != nil {
			return false
		}
	}
	return base.Hash() == hash
}
