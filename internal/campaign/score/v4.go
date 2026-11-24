package score

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Offer is one alert post by any lane: Resolve marks a resolve post.
type Offer struct {
	At      time.Time
	Lane    string
	Resolve bool
}

// OfferedMinute is minute Minute (from T0) of the offered load: firing posts,
// resolve posts and the firing posts per lane.
type OfferedMinute struct {
	Minute          int
	Firing, Resolve int
	Lanes           map[string]int
}

// OfferedRate is the offered load per minute over every lane, and the peak
// and mean firing posts per second over the load window's whole minutes.
type OfferedRate struct {
	PerMinute                    []OfferedMinute
	PeakPerSecond, MeanPerSecond float64
}

// OfferedPerMinute buckets offers in [t0, loadEnd) by minute from T0.
func OfferedPerMinute(t0, loadEnd time.Time, offers []Offer) OfferedRate {
	n := int(loadEnd.Sub(t0) / time.Minute)
	if n <= 0 {
		return OfferedRate{}
	}
	out := OfferedRate{PerMinute: make([]OfferedMinute, n)}
	for i := range out.PerMinute {
		out.PerMinute[i] = OfferedMinute{Minute: i, Lanes: map[string]int{}}
	}
	for _, o := range offers {
		i := int(o.At.Sub(t0) / time.Minute)
		if o.At.Before(t0) || i >= n {
			continue
		}
		m := &out.PerMinute[i]
		if o.Resolve {
			m.Resolve++
			continue
		}
		m.Firing++
		m.Lanes[o.Lane]++
	}
	total := 0
	for _, m := range out.PerMinute {
		total += m.Firing
		out.PeakPerSecond = max(out.PeakPerSecond, float64(m.Firing)/60)
	}
	out.MeanPerSecond = float64(total) / float64(n*60)
	return out
}

// offeredSlack is how far a minute may exceed the budget (rounding, retries).
const offeredSlack = 1.05

// OverBudget names the minutes whose firing posts exceed the plan: spike per
// second in a minute that overlaps a burst of b, total elsewhere. A run that
// offered more than the gate's rate measured something else, so these are
// INCONCLUSIVE reasons.
func (o OfferedRate) OverBudget(b scenario.BaselineSpec, total, spike float64) []string {
	bursts := append([]scenario.BurstSpec(nil), b.Bursts...)
	if b.Burst != nil {
		bursts = append(bursts, *b.Burst)
	}
	var over []string
	for _, m := range o.PerMinute {
		from, to := time.Duration(m.Minute)*time.Minute, time.Duration(m.Minute+1)*time.Minute
		limit := total
		for _, x := range bursts {
			if from < x.At+x.For && x.At < to {
				limit = spike
			}
		}
		if rate := float64(m.Firing) / 60; rate > limit*offeredSlack {
			over = append(over, fmt.Sprintf("T+%d (%.2f/s, budget %.0f/s)", m.Minute, rate, limit))
		}
	}
	if len(over) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("offered rate over budget in %d minutes: %s", len(over), strings.Join(over[:min(len(over), 5)], ", "))}
}

// LedgerOffers are the worker lane's attempted posts.
func LedgerOffers(rows []domain.LaneRow, lane string) []Offer {
	var out []Offer
	for _, r := range rows {
		if !r.SentAt.IsZero() {
			out = append(out, Offer{At: r.SentAt, Lane: lane, Resolve: r.Kind == "resolve"})
		}
	}
	return out
}

// EscalationEpisode is one episode of an escalation cohort member: its
// startsAt, its first accepted post and when it stopped (the next episode or
// the drain).
type EscalationEpisode struct {
	Alert             string
	StartsAt, FirstAt time.Time
	End               time.Time
}

// The rise is due After past the first accepted post: native admits the post
// at once and OSCAR counts After from native active_since. escalationLate
// allows the 10 s schedule tick plus native callback and notifier delivery
// under load (History p95 alone may reach 40 s); escalationEarly allows clock
// and admission jitter.
const (
	escalationLate  = 2 * time.Minute
	escalationEarly = 10 * time.Second
)

// JudgeEscalation scores each episode long enough to escalate (its first
// post plus After plus escalationLate before End) from the sink receipts of
// that alert and startsAt: the severity must rise to target exactly once,
// After past the first post (at most escalationEarly before, escalationLate after), and
// stay there. No receipt at all is Unknown.
func JudgeEscalation(eps []EscalationEpisode, receipts []targetops.Receipt, target string, after time.Duration) (Rate, []string) {
	var r Rate
	var why []string
	bad := func(f string, a ...any) {
		if len(why) < 5 {
			why = append(why, fmt.Sprintf(f, a...))
		}
	}
	for _, ep := range eps {
		if ep.FirstAt.IsZero() || ep.End.Sub(ep.FirstAt) < after+escalationLate {
			continue
		}
		r.Expected++
		type seen struct {
			at  time.Time
			sev string
		}
		var got []seen
		for _, rc := range receipts {
			for _, a := range rc.Alerts {
				st, err := time.Parse(time.RFC3339Nano, a.StartsAt)
				if a.AlertName != ep.Alert || a.Status == "resolved" || err != nil || st.Sub(ep.StartsAt).Abs() >= time.Second {
					continue
				}
				got = append(got, seen{rc.ReceivedAt, a.Labels["severity"]})
			}
		}
		sort.Slice(got, func(i, j int) bool { return got[i].at.Before(got[j].at) })
		rise := -1
		for i, g := range got {
			if strings.EqualFold(g.sev, target) {
				rise = i
				break
			}
		}
		due := ep.FirstAt.Add(after)
		switch {
		case len(got) == 0:
			r.Unknown++
			bad("escalation %s@%s: no sink receipt", ep.Alert, ep.StartsAt.Format(time.RFC3339))
			continue
		case rise < 0:
			r.Failed++
			bad("escalation %s@%s: never reached %s", ep.Alert, ep.StartsAt.Format(time.RFC3339), target)
			continue
		case rise == 0 || got[rise].at.Before(due.Add(-escalationEarly)) || got[rise].at.After(due.Add(escalationLate)):
			r.Failed++
			bad("escalation %s@%s: rose to %s %s after the first post, want %s (-%s +%s)", ep.Alert, ep.StartsAt.Format(time.RFC3339), target,
				got[rise].at.Sub(ep.FirstAt).Round(time.Second), after, escalationEarly, escalationLate)
			continue
		}
		ok := true
		for _, g := range got[rise:] {
			ok = ok && strings.EqualFold(g.sev, target)
		}
		if !ok {
			r.Failed++
			bad("escalation %s@%s: severity fell back after rising", ep.Alert, ep.StartsAt.Format(time.RFC3339))
			continue
		}
		r.Succeeded++
	}
	r.finish()
	return r, why
}

// TaskExecRun is one on-demand task run: requested At, OSCAR's run id, its
// terminal status (or error/timeout) and request-to-terminal seconds.
type TaskExecRun struct {
	At      time.Time
	ID      string `json:",omitempty"`
	Status  string
	Seconds float64
	Err     string `json:",omitempty"`
}

// TaskExecStats is the v4 on-demand task lane.
type TaskExecStats struct {
	Job               string
	Runs              []TaskExecRun
	Succeeded, Failed int
	SuccessRatio, P95 float64
	NotRun            string `json:",omitempty"`
}

// Judge gates the lane: success ratio and p95 against their limits; no run
// is INCONCLUSIVE.
func (s *TaskExecStats) Judge(minSuccess float64, maxP95 time.Duration) (h []Headline, fails, incon []string) {
	var secs []float64
	s.Succeeded, s.Failed = 0, 0
	for _, r := range s.Runs {
		if r.Status == "SUCCESS" {
			s.Succeeded++
			secs = append(secs, r.Seconds)
		} else {
			s.Failed++
		}
	}
	if len(s.Runs) == 0 {
		why := s.NotRun
		if why == "" {
			why = "no run"
		}
		return nil, nil, []string{"task executions: " + why}
	}
	s.SuccessRatio, s.P95 = float64(s.Succeeded)/float64(len(s.Runs)), Percentile(secs, 95)
	h = []Headline{limitHeadline("tasks.exec_success", "ratio", s.SuccessRatio, minSuccess, true),
		limitHeadline("tasks.exec_p95", "s", s.P95, maxP95.Seconds(), false)}
	for _, x := range h {
		if !x.Pass {
			fails = append(fails, fmt.Sprintf("%s %.3g %s breaches limit %.3g", x.Name, x.Value, x.Unit, *x.Limit))
		}
	}
	return h, fails, nil
}

// ReportRun is one alert report: requested At, its run id and final state,
// request-to-ready seconds, the artifact manifest read back from object
// storage, and History latency of the worker alerts sent while it ran.
type ReportRun struct {
	At            time.Time
	RunID, State  string
	Seconds       float64
	ManifestBytes int64
	Parts         int
	Verified      bool
	HistoryP95    float64
	HistoryN      int
	Err           string `json:",omitempty"`
	End           time.Time
}

// ReportStats is the v4 alert-report lane.
type ReportStats struct {
	Planned int
	Runs    []ReportRun
}

// HistoryWithin fills each report's History p95 from the worker firings sent
// between its request and its end.
func (s *ReportStats) HistoryWithin(rows []domain.LaneRow, hist []oscar.HistoryRecord) {
	for i := range s.Runs {
		rr := &s.Runs[i]
		var in []domain.LaneRow
		for _, r := range rows {
			if !r.SentAt.Before(rr.At) && !r.SentAt.After(rr.End) {
				in = append(in, r)
			}
		}
		lat, _ := historyLatencies(in, hist)
		rr.HistoryP95, rr.HistoryN = Percentile(lat, 95), len(lat)
	}
}

// Judge: a report not ready (failed, expired or past the timeout) or History
// p95 above the limit while it ran FAILs; a report not run, unreadable or
// with an artifact not read back from storage is INCONCLUSIVE.
func (s ReportStats) Judge(historyP95 time.Duration) (fails, incon []string) {
	if len(s.Runs) < s.Planned {
		incon = append(incon, fmt.Sprintf("alert report: %d of %d planned runs requested", len(s.Runs), s.Planned))
	}
	for _, r := range s.Runs {
		name := "alert report " + r.RunID
		switch {
		case r.State == "artifact_ready" && !r.Verified:
			incon = append(incon, name+": artifact not read back from object storage: "+r.Err)
		case r.State == "artifact_ready":
		case r.State == "" || r.State == "unknown":
			incon = append(incon, name+": outcome unknown: "+r.Err)
		default:
			fails = append(fails, fmt.Sprintf("%s: %s after %.0fs %s", name, r.State, r.Seconds, r.Err))
		}
		if r.HistoryN == 0 {
			incon = append(incon, name+": no History latency samples while it ran")
		} else if r.HistoryP95 > historyP95.Seconds() {
			fails = append(fails, fmt.Sprintf("%s: History p95 %.3gs while it ran breaches limit %.3gs", name, r.HistoryP95, historyP95.Seconds()))
		}
	}
	return fails, incon
}
