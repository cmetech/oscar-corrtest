package score

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

type Point = targetops.Point

// Rate is a success tally. Unknown and Duplicate count in the denominator.
type Rate struct {
	Expected, Succeeded, Failed, Unknown, Duplicate int
	Ratio                                           float64
}

func (r *Rate) finish() {
	// No evidence is never a pass: Ratio stays 0 and Compute decides from the plan.
	if r.Expected > 0 {
		r.Ratio = float64(r.Succeeded) / float64(r.Expected)
	}
}

// Percentile is nearest-rank over xs (not modified); 0 for empty input.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[min(max(i, 0), len(s)-1)]
}

// Slope is the least-squares slope of Value over Time (seconds), per hour.
func Slope(pts []Point) float64 {
	n := float64(len(pts))
	if n < 2 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	t0 := pts[0].Time // centre on the first sample: epoch-scale squares lose precision
	for _, p := range pts {
		x := p.Time - t0
		sx, sy, sxx, sxy = sx+x, sy+p.Value, sxx+x*x, sxy+x*p.Value
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return math.Round((n*sxy-sx*sy)/den*3600*1e6) / 1e6 // rounded so exact boundaries compare cleanly
}

// Baseline summarises the ledger rows. Accepted means the lane recorded a 2xx
// with no error class. Planned is the lane plan's events in the run window
// (seqs 0..Planned-1; 0 = unknown); Offered is those seqs actually sent.
type Baseline struct {
	Rows, Accepted, Missed, Errors int
	Planned, Offered               int
	Definite5xx                    int // definite 5xx answers (counted apart from unknown outcomes)
	ErrorsByClass                  map[string]int
}

func accepted(r domain.LaneRow) bool {
	return r.Done && !r.Missed && r.ErrorClass == "" && r.Status >= 200 && r.Status < 300
}

func reconcileBaseline(rows []domain.LaneRow, planned int) Baseline {
	b := Baseline{Planned: planned, ErrorsByClass: map[string]int{}}
	for _, r := range rows {
		b.Rows++
		if r.Seq < planned && !r.Missed && r.ErrorClass != lane.ErrClassMissed {
			b.Offered++
		}
		if r.Done && r.Status >= 500 && r.Status < 600 {
			b.Definite5xx++
		}
		switch {
		case accepted(r):
			b.Accepted++
		case r.Missed || r.ErrorClass == lane.ErrClassMissed:
			b.Missed++
		case r.ErrorClass != "":
			b.Errors++
			b.ErrorsByClass[r.ErrorClass]++
		case r.Done:
			b.Errors++
			b.ErrorsByClass[lane.ErrClassHTTP]++
		}
	}
	return b
}

// firstFiring maps identity to the first accepted firing row.
func firstFiring(rows []domain.LaneRow) map[string]domain.LaneRow {
	m := map[string]domain.LaneRow{}
	for _, r := range rows {
		if r.Kind != "firing" || !accepted(r) {
			continue
		}
		if o, ok := m[r.Identity]; !ok || r.SentAt.Before(o.SentAt) {
			m[r.Identity] = r
		}
	}
	return m
}

// historyLatencies is History created_at minus ledger SentAt for accepted
// firing rows; accepted identities absent from History are counted missing.
func historyLatencies(rows []domain.LaneRow, hist []oscar.HistoryRecord) (lat []float64, missing int) {
	first := map[string]time.Time{}
	for _, h := range hist {
		if t, ok := first[h.AlertName]; !ok || h.CreatedAt.Before(t) {
			first[h.AlertName] = h.CreatedAt
		}
	}
	for id, r := range firstFiring(rows) {
		t, ok := first[id]
		if !ok {
			missing++
			continue
		}
		lat = append(lat, t.Sub(r.SentAt).Seconds())
	}
	return lat, missing
}

// sinkLatencies is first non-resolved sink receipt minus SentAt, per identity.
func sinkLatencies(rows []domain.LaneRow, rcs []targetops.Receipt) []float64 {
	first := map[string]time.Time{}
	for _, rc := range rcs {
		for _, a := range rc.Alerts {
			if a.Status == "resolved" {
				continue
			}
			if t, ok := first[a.AlertName]; !ok || rc.ReceivedAt.Before(t) {
				first[a.AlertName] = rc.ReceivedAt
			}
		}
	}
	var lat []float64
	for id, r := range firstFiring(rows) {
		if t, ok := first[id]; ok {
			lat = append(lat, t.Sub(r.SentAt).Seconds())
		}
	}
	return lat
}

// TicketInputs: Expected alertnames; Incidents holds every incident found by
// short_description (an absent key means the lookup failed: unknown); Bound
// is whether History shows the ticket bound.
type TicketInputs struct {
	Expected  []string
	Incidents map[string][]targetops.Incident
	Bound     map[string]bool
}

func resolvedComment(i targetops.Incident) bool {
	for _, c := range i.Comments {
		if strings.Contains(c.Value, "[OSCAR AUTO-UPDATE") && strings.Contains(c.Value, "Alert has been resolved") {
			return true
		}
	}
	return false
}

func reconcileTickets(in TicketInputs) Rate {
	r := Rate{Expected: len(in.Expected)}
	for _, id := range in.Expected {
		incs, ok := in.Incidents[id]
		switch {
		case !ok:
			r.Unknown++
		case len(incs) > 1:
			r.Duplicate++
		case len(incs) == 1 && in.Bound[id] && resolvedComment(incs[0]):
			r.Succeeded++
		default:
			r.Failed++
		}
	}
	r.finish()
	return r
}

// ResendCase is one identity: the policy, its deliveries, and the observed
// resends from the status, sink or SNOW resend_comment evidence. Observed
// offsets use the same time frame as the deliveries (offset from the initial
// action). Exhausted is the status's sticky_exhausted.
type ResendCase struct {
	Policy     scenario.PolicySpec
	Deliveries []resend.Delivery
	Observed   []time.Duration
	Exhausted  bool
}

const resendTolerance = 60 * time.Second

// resendOK: the observed resend count equals the modelled one, exhaustion
// agrees, and each resend is within 60 s of its modelled time (either way).
// A case that models no resend is no evidence (Unknown), never a pass.
func resendOK(c ResendCase) (ok bool, err error) {
	exp, err := resend.Expect(c.Policy, c.Deliveries)
	if err != nil {
		return false, err
	}
	if len(exp.Resends) == 0 {
		return false, fmt.Errorf("resend %s: %d deliveries model no resend", c.Policy.Key, len(c.Deliveries))
	}
	if len(c.Observed) != len(exp.Resends) || c.Exhausted != exp.Exhausted {
		return false, nil
	}
	for i, o := range c.Observed {
		if d := o - exp.Resends[i]; d > resendTolerance || d < -resendTolerance {
			return false, nil
		}
	}
	return true, nil
}

func reconcileResend(cases []ResendCase) Rate {
	r := Rate{Expected: len(cases)}
	for _, c := range cases {
		switch ok, err := resendOK(c); {
		case err != nil:
			r.Unknown++
		case ok:
			r.Succeeded++
		default:
			r.Failed++
		}
	}
	r.finish()
	return r
}

// CorrRun is a correlation child run's verdict, cleanup and
// last-source-to-parent latency in seconds.
type CorrRun struct {
	Verdict        domain.Verdict
	Cleanup        domain.CleanupStatus
	LatencySeconds float64
}

func reconcileCorrelation(runs []CorrRun) (Rate, []float64) {
	r := Rate{Expected: len(runs)}
	var lat []float64
	for _, c := range runs {
		switch {
		case c.Verdict == domain.VerdictPass && c.Cleanup == domain.CleanupClean:
			r.Succeeded++
			lat = append(lat, c.LatencySeconds)
		case c.Verdict == domain.VerdictInconclusive: // unknown counts as failure
			r.Unknown++
		default:
			r.Failed++
		}
	}
	r.finish()
	return r, lat
}
