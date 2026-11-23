package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// The workload hash fixes the relative schedule. Count actual sends against
// that plan; HTTP writes, accepted source identities and sink members differ.
func (r *run) nativeLoad(ctx context.Context, t *nativeTally, c *score.Scorecard, in score.Inputs, end time.Time, report map[string]any) {
	plan := lane.Compile(r.id, r.s.Seed, r.s.Baseline, r.s.LoadEnd)
	seen := make(map[int]bool, len(plan.Events))
	accepted := map[string]bool{}
	t.LoadedSeconds = r.s.LoadEnd.Seconds()
	if !r.normalEnd || end.Before(r.t0.Add(r.s.LoadEnd)) {
		t.miss("loaded interval incomplete")
	}
	offered := 0
	minutes := int(r.s.LoadEnd / time.Minute)
	plannedMinutes, acceptedMinutes := make([]int, minutes), make([]int, minutes)
	for _, e := range plan.Events {
		if i := int(e.At / time.Minute); i < minutes {
			plannedMinutes[i]++
		}
	}
	for _, row := range in.Ledger {
		if row.Seq < 0 || row.Seq >= len(plan.Events) || seen[row.Seq] {
			t.fail("duplicate or out-of-plan baseline row")
			continue
		}
		seen[row.Seq] = true
		if !row.Missed && !row.SentAt.IsZero() {
			t.offer("baseline", row.SentAt, row.Status)
		}
		p := plan.Events[row.Seq]
		if row.Identity != p.Identity || row.Kind != string(p.Kind) || row.Ticket != p.Ticket {
			t.fail("baseline plan member mismatch")
			continue
		}
		if row.Missed || row.SentAt.IsZero() || !row.Done || row.ErrorClass != "" || row.Status/100 != 2 {
			t.miss("scheduled baseline member not accepted")
			continue
		}
		if p.Kind == lane.Firing {
			accepted[p.Identity] = true
		}
		if !row.SentAt.Before(r.t0) && row.SentAt.Before(r.t0.Add(r.s.LoadEnd)) {
			t.AcceptedBaselineInLoad++
			acceptedMinutes[int(row.SentAt.Sub(r.t0)/time.Minute)]++
		}
	}
	for _, row := range in.Ledger {
		if !row.Missed && !row.SentAt.IsZero() && !row.SentAt.Before(r.t0) && row.SentAt.Before(r.t0.Add(r.s.LoadEnd)) {
			offered++
		}
	}
	short := 0
	for i, planned := range plannedMinutes {
		if acceptedMinutes[i]*100 < planned*98 {
			short++
		} else {
			short = 0
		}
		if short == 2 {
			t.fail("baseline throughput below 98% of scheduled work for consecutive minutes")
		}
	}
	report["baselineMinutes"] = map[string][]int{"planned": plannedMinutes, "accepted": acceptedMinutes}
	t.OfferedBaselinePerSecond = float64(offered) / t.LoadedSeconds
	if len(seen) != len(plan.Events) || c.Baseline.Accepted != len(plan.Events) {
		t.miss("incomplete seeded baseline workload")
	}
	if c.Latency.History.N != len(accepted) || c.Latency.Sink.N != len(accepted) {
		t.miss("accepted baseline members lack downstream History or captured sink")
	}
	if c.Tickets.Expected < plan.Tickets || c.Tickets.Unknown > 0 || c.Correlation.Unknown > 0 || c.Resend.Unknown > 0 {
		t.miss("independent ticket/correlation/resend observations incomplete")
	}
	if c.Tickets.Failed+c.Tickets.Duplicate+c.Resend.Failed > 0 {
		t.fail("ticket or ordinary policy recipient mismatch")
	}
	if len(r.s.Correlation.SetA) > 0 && c.Correlation.Expected < len(r.s.Correlation.SetA) {
		t.miss("missing planned correlation cases")
	}
	if len(r.s.Correlation.SetB) > 0 && r.corrTickets.Runs == 0 {
		t.miss("set B ticket cohort was not observed")
	}
	if st := r.s.Standing; st != nil {
		planned := int((st.Until*time.Duration(st.Size) + st.Interval - 1) / st.Interval)
		if c.ATT == nil || c.ATT.Standing.Posts["ok"] != planned || c.ATT.Standing.Resolves["ok"] != st.Size {
			t.miss("standing pool planned sends and resolves not all accepted")
		}
	}
	for _, h := range []struct {
		name string
		p    score.Percentiles
	}{{"history", c.Latency.History}, {"sink", c.Latency.Sink}, {"correlation", c.Latency.Correlation}} {
		if h.p.N > 0 {
			c.Headlines = append(c.Headlines, score.Headline{Name: "latency." + h.name + "_p99", Value: h.p.P99, Unit: "s", Pass: true})
		}
	}
	r.childOffers(ctx, t)
	r.ordinaryScore(t, in.Receipts)
	r.resources(ctx, t, end, report)
	if r.s.NativeWorkload.ManagedTL1 {
		var statusTimes []float64
		for _, v := range r.fo["managed_policy_status"] {
			if v.Err {
				t.miss("managed policy status read failed")
			} else {
				statusTimes = append(statusTimes, v.Seconds)
			}
		}
		expected := int(r.s.LoadEnd / (5 * time.Minute))
		if len(statusTimes) < expected {
			t.miss("policy status front-office samples incomplete")
		}
		for _, h := range percentiles("frontoffice.managed_policy_status", statusTimes) {
			limit := r.s.Limits.FrontOfficeP95.Seconds()
			if h.Name == "frontoffice.managed_policy_status.p99" {
				limit = r.s.Limits.FrontOfficeP99.Seconds()
			}
			if h.Value > limit {
				t.fail(h.Name + " over absolute limit")
			}
			c.Headlines = append(c.Headlines, h)
		}
	}
	for action, p := range c.FrontOffice {
		if p.ErrorRate > 0 {
			t.miss("front office action response unavailable: " + action)
		}
	}

	if !r.s.Custom {
		capture, err := r.costCapture(t, in.Receipts)
		if err != nil {
			t.miss("ordinary cost capture: " + err.Error())
		} else {
			c.ObserverConfig = capture.ObserverConfig
			report["ordinaryCost"] = capture
			for _, h := range capture.Headlines {
				c.Headlines = append(c.Headlines, h)
			}
		}
	}
}

func (r *run) ordinaryScore(t *nativeTally, rs []targetops.Receipt) {
	for i := 0; time.Duration(i)*5*time.Minute < r.s.LoadEnd; i++ {
		names := map[string]string{}
		for _, route := range []string{"direct", "canonical"} {
			id := fmt.Sprintf("Campaign-%s-native-%s-%d", r.id, route, i)
			first, repeat := "direct_no_policy", "direct_no_policy"
			if route == "canonical" {
				first, repeat = "canonical_first_no_policy", "canonical_repeat_no_policy"
			}
			expected := map[string]int{"source_resolve": 1}
			expected[first]++
			expected[repeat]++
			if i == 0 && route == "canonical" {
				expected["transport_duplicate"] = 1
			}
			for cohort, n := range expected {
				if t.wireOrdinary[[3]string{id, route, cohort}] != n {
					t.miss("ordinary cohort inputs: " + id + "/" + cohort)
				}
			}
			member, ok := t.ordinary[id]
			if !ok || len(member.Receivers) != 1 {
				t.miss("ordinary source/receiver observation: " + id)
				continue
			}
			names[route] = member.Receivers[0].Name
			// One group receipt may legitimately witness more than one owned member.
			firings, resolves := 0, 0
			ids := map[int64]bool{}
			for _, rc := range rs {
				for _, a := range rc.Alerts {
					if a.AlertName != id {
						continue
					}
					if route == "direct" && rc.Receiver != names[route] || route == "canonical" && rc.Notifier != r.e.d.CampaignNotifier {
						t.fail("unexpected ordinary capture recipient: " + id)
					}
					start, err := time.Parse(time.RFC3339Nano, a.StartsAt)
					if err != nil || !start.Equal(member.StartsAt) || a.Labels["campaign_lane"] != "native-"+route || rc.ID <= 0 || ids[rc.ID] || rc.ReceivedAt.Before(start) {
						t.fail("ordinary physical member mismatch")
						continue
					}
					ids[rc.ID] = true
					t.receipts([]targetops.Receipt{rc}, id)
					if a.Status == "firing" {
						firings++
					} else if a.Status == "resolved" {
						resolves++
					}
				}
			}
			if resolves > 1 {
				t.fail("duplicate ordinary resolve capture: " + id)
			}
			if firings < 2 || resolves < 1 {
				t.miss("initial, native repeat or resolved capture missing: " + id)
			}
		}
		if names["canonical"] != "" && names["canonical"] == names["direct"] {
			t.fail("direct and canonical native routes share receiver")
		}
	}
}

// Require measured resource samples across the loaded interval, independent of
// the q2 report-only per-alert accounting. Expose trend and the original series.
func (r *run) resources(ctx context.Context, t *nativeTally, end time.Time, report map[string]any) {
	type trend struct {
		Container                       string
		Samples                         int
		First, Last, Peak, SlopePerHour float64
	}
	trends := map[string][]trend{}
	to := r.t0.Add(r.s.LoadEnd)
	for name, q := range score.NativeResourceQueries {
		m, err := r.e.d.Metrics.QueryRange(ctx, q, r.t0, to, metricStep)
		if err != nil || len(m) == 0 {
			t.miss("missing measured resource " + name)
			continue
		}
		for _, s := range m {
			if !score.CoveredSeries(s.Values, r.t0, to) {
				t.miss("incomplete measured resource " + name)
				continue
			}
			peak := s.Values[0].Value
			for _, p := range s.Values {
				peak = max(peak, p.Value)
			}
			trends[name] = append(trends[name], trend{s.Metric["name"], len(s.Values), s.Values[0].Value, s.Values[len(s.Values)-1].Value, peak, score.Slope(s.Values)})
		}
	}
	report["loadResources"] = trends
	depth := r.series(ctx, queueDepthQuery, score.QueueLabel, end)
	for q, pts := range depth {
		if !score.CoveredSeries(pts, r.t0, to) {
			t.miss("load queue coverage " + q)
			continue
		}
		// Compare first and final five-minute medians. A persistently nonidle,
		// growing tail identifies backlog before final cleanup can empty it.
		var first, last []float64
		for _, p := range pts {
			if p.Time <= float64(r.t0.Add(5*time.Minute).Unix()) {
				first = append(first, p.Value)
			}
			if p.Time >= float64(to.Add(-5*time.Minute).Unix()) && p.Time <= float64(to.Unix()) {
				last = append(last, p.Value)
			}
		}
		a, b := score.Percentile(first, 50), score.Percentile(last, 50)
		if b > a && !r.s.Precheck.QueueIdle(b) {
			t.fail("queue backlog grew under load: " + q)
		}
	}
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func percentiles(name string, x []float64) []score.Headline {
	sort.Float64s(x)
	return []score.Headline{{Name: name + ".p95", Value: score.Percentile(x, 95), Unit: "s", Pass: true}, {Name: name + ".p99", Value: score.Percentile(x, 99), Unit: "s", Pass: true}}
}
