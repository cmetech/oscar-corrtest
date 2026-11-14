// Package resend models OSCAR's episode resend policy (spec "Operator
// contract") so the campaign can compare expected and observed actions.
package resend

import (
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

type Delivery struct {
	At    time.Duration
	Fresh bool
}

type Expected struct {
	InitialAt time.Duration
	Resends   []time.Duration
	Exhausted bool
}

// Expect replays deliveries (in time order) against the policy. The first
// delivery is the initial action; each later delivery yields at most one
// resend.
//
// Milestone choice: a resend by any trigger marks every multiple of Every up
// to the current count as handled ("reaches a previously unhandled
// multiple"). A multiple is only crossed on the fresh delivery that reaches
// it, so a time resend never leaves an earlier multiple pending, and a
// delivery where both triggers fire is still one resend.
func Expect(p scenario.PolicySpec, ds []Delivery) Expected {
	var e Expected
	if len(ds) == 0 {
		return e
	}
	e.InitialAt = ds[0].At
	last, count, handled := ds[0].At, 1, 0 // count includes the initial firing
	capped := func() bool { return p.MaxResends != nil && len(e.Resends) >= *p.MaxResends }
	byTime := p.Mode == "time" || p.Mode == "either"
	byOcc := p.Mode == "occurrences" || p.Mode == "either"
	for _, d := range ds[1:] {
		if d.Fresh {
			count++
		}
		if capped() {
			break
		}
		due := byTime && d.At-last >= p.Interval
		if byOcc && d.Fresh && p.Every > 0 && count%p.Every == 0 && count/p.Every > handled {
			due = true
		}
		if due {
			e.Resends = append(e.Resends, d.At)
			last = d.At
			if p.Every > 0 {
				handled = count / p.Every
			}
		}
	}
	e.Exhausted = capped()
	return e
}
