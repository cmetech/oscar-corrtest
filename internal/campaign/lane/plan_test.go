package lane

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

func spec() scenario.BaselineSpec { return scenario.AlertingDayV1().Baseline }

const two = 120 * time.Minute

func TestCompileCountsForTwoHours(t *testing.T) {
	p := Compile("r1", 1, spec(), two)
	if p.Firings != 25200 {
		t.Fatalf("firings=%d", p.Firings)
	}
	rs, last, inst := 0, map[string]time.Duration{}, map[string]bool{}
	for _, e := range p.Events {
		if e.Kind == "firing" {
			inst[e.Instance] = true
			if l, ok := last[e.Identity]; !ok || e.At-l >= spec().ResolveAfter {
				rs++ // episode start
			}
			last[e.Identity] = e.At
		}
	}
	if p.Resolves != rs || p.Resolves >= p.Firings {
		t.Fatalf("resolves=%d episodes=%d", p.Resolves, rs)
	}
	if len(inst) != 50 {
		t.Fatalf("instances=%d", len(inst))
	}
	at := map[int]time.Duration{}
	for _, e := range p.Events {
		if e.Kind == "firing" {
			at[e.Seq] = e.At
		}
	}
	for _, e := range p.Events {
		if e.Kind == "resolve" && e.At != at[e.FiringSeq]+spec().ResolveAfter {
			t.Fatalf("resolve %d at %v, firing at %v", e.Seq, e.At, at[e.FiringSeq])
		}
	}
	for i := 1; i < len(p.Events); i++ {
		if p.Events[i].At < p.Events[i-1].At {
			t.Fatal("unsorted")
		}
	}
}

func TestRefirePoolShare(t *testing.T) {
	p := Compile("r1", 1, spec(), two)
	pool := map[string]bool{}
	n := 0
	for _, e := range p.Events {
		if e.Kind == "firing" && strings.Contains(e.Identity, "-pool-") {
			pool[e.Identity] = true
			n++
		}
	}
	if share := float64(n) / float64(p.Firings); math.Abs(share-0.10) > 0.01 || len(pool) != 100 {
		t.Fatalf("share=%v distinct=%d", share, len(pool))
	}
}

func TestTicketsOnlyNewIdentitiesInWindow(t *testing.T) {
	p := Compile("r1", 1, spec(), two)
	n, newIdx, tickets := 0, 0, 0
	for _, e := range p.Events {
		if e.Kind != "firing" {
			continue
		}
		isNew := !strings.Contains(e.Identity, "-pool-")
		if isNew {
			newIdx++
		}
		if e.Ticket {
			tickets++
			if !isNew || e.At < 60*time.Minute || e.At >= two || newIdx%20 != 0 {
				t.Fatalf("bad ticket %+v", e)
			}
		} else if isNew && newIdx%20 == 0 && e.At >= 60*time.Minute {
			t.Fatalf("missed ticket %+v", e)
		}
		n++
	}
	if tickets != p.Tickets || math.Abs(float64(tickets)-567) > 10 {
		t.Fatalf("tickets=%d plan=%d", tickets, p.Tickets)
	}
}

func TestResolveTicketMatchesFiring(t *testing.T) {
	p := Compile("r1", 1, spec(), two)
	n := 0
	for _, e := range p.Events {
		if e.Kind == Resolve {
			if f := p.Events[e.FiringSeq]; e.Ticket != f.Ticket {
				t.Fatalf("resolve %d ticket=%v firing ticket=%v", e.Seq, e.Ticket, f.Ticket)
			}
			if e.Ticket {
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("no ticketed resolves")
	}
}

func TestDeterministicBySeed(t *testing.T) {
	a, b, c := Compile("r", 7, spec(), time.Hour), Compile("r", 7, spec(), time.Hour), Compile("r", 8, spec(), time.Hour)
	same := true
	for i := range a.Events {
		if a.Events[i] != b.Events[i] {
			t.Fatal("same seed differs")
		}
		if a.Events[i].Severity != c.Events[i].Severity {
			same = false
		}
	}
	if same {
		t.Fatal("different seed gave identical severities")
	}
}

func TestSeverityDistribution(t *testing.T) {
	p := Compile("r1", 1, spec(), two)
	cnt := map[string]int{}
	for _, e := range p.Events {
		if e.Kind == "firing" {
			cnt[e.Severity]++
		}
	}
	for k, w := range spec().Severity {
		if got := float64(cnt[k]) / float64(p.Firings); math.Abs(got-w) > 0.02 {
			t.Fatalf("%s share %v want %v", k, got, w)
		}
	}
}

func TestPoolEpisodeSingleResolve(t *testing.T) {
	p := Compile("r1", 1, spec(), two)
	ra := spec().ResolveAfter
	open := map[string]time.Duration{} // identity -> last firing At while episode open
	for _, e := range p.Events {
		switch e.Kind {
		case "firing":
			if l, ok := open[e.Identity]; ok && e.At-l >= ra {
				t.Fatalf("%s fired after episode should have resolved", e.Identity)
			}
			open[e.Identity] = e.At
		case "resolve":
			l, ok := open[e.Identity]
			if !ok || e.At != l+ra {
				t.Fatalf("%s resolve at %v, open=%v/%v", e.Identity, e.At, l, ok)
			}
			delete(open, e.Identity)
		}
	}
	if len(open) != 0 {
		t.Fatalf("%d unresolved episodes", len(open))
	}
}

func TestTieOrdersResolveBeforeFiring(t *testing.T) {
	// One identity firing every 1s with ResolveAfter 1s: each resolve ties the next firing.
	s := scenario.BaselineSpec{Rate: 1, PoolSize: 1, Instances: 1, Severity: map[string]float64{"warning": 1}, ResolveAfter: time.Second}
	p := Compile("t", 1, s, 5*time.Second)
	ties := 0
	for i := 1; i < len(p.Events); i++ {
		a, b := p.Events[i-1], p.Events[i]
		if a.At == b.At {
			ties++
			if a.Kind != Resolve || b.Kind != Firing {
				t.Fatalf("tie at %v ordered %s then %s", a.At, a.Kind, b.Kind)
			}
		}
	}
	if ties == 0 {
		t.Fatal("no ties produced")
	}
}

func TestPoolIdentityLabelsStable(t *testing.T) {
	p := Compile("r1", 1, spec(), 10*time.Minute)
	seen := map[string][2]string{}
	pool := 0
	for _, e := range p.Events {
		if !strings.Contains(e.Identity, "-pool-") {
			continue
		}
		pool++
		l := [2]string{e.Severity, e.Instance}
		if prev, ok := seen[e.Identity]; ok && prev != l {
			t.Fatalf("%s labels changed: %v vs %v", e.Identity, prev, l)
		}
		seen[e.Identity] = l
	}
	if pool == 0 {
		t.Fatal("no pool events")
	}
}

// TestBurstWindowRaisesTheFiringRate: inside Burst the lane fires at
// Burst.Rate, elsewhere at Rate (functional-burst: a 2-5 minute spike).
func TestBurstWindowRaisesTheFiringRate(t *testing.T) {
	spec := scenario.BaselineSpec{Rate: 5, UniqueRatio: 1, PoolSize: 10, Instances: 5, Severity: map[string]float64{"minor": 1},
		ResolveAfter: time.Hour, Burst: &scenario.BurstSpec{At: 10 * time.Minute, For: 5 * time.Minute, Rate: 25}}
	p := Compile("r", 1, spec, 20*time.Minute)
	in, out := 0, 0
	for _, e := range p.Events {
		if e.Kind != Firing {
			continue
		}
		if e.At >= 10*time.Minute && e.At < 15*time.Minute {
			in++
		} else {
			out++
		}
	}
	if in != 25*300 || out != 5*900 || p.Firings != in+out {
		t.Fatalf("burst firings %d (want 7500), outside %d (want 4500), total %d", in, out, p.Firings)
	}
	spec.Burst = nil
	if q := Compile("r", 1, spec, 20*time.Minute); q.Firings != 5*1200 {
		t.Fatalf("no burst: %d firings, want 6000", q.Firings)
	}
}

// TestTwoBurstsEachRaiseTheirWindow: v4 spikes at T+45 and T+105 for 5
// minutes each; the rate between and after them is the base rate.
func TestTwoBurstsEachRaiseTheirWindow(t *testing.T) {
	spec := scenario.AlertingDayV4().Baseline
	spec.ResolveAfter = time.Hour
	p := Compile("r", 1, spec, 150*time.Minute)
	perMin := map[int]int{}
	for _, e := range p.Events {
		if e.Kind == Firing {
			perMin[int(e.At/time.Minute)]++
		}
	}
	for m := 0; m < 150; m++ {
		want := 246 // 4.1/s
		if m >= 45 && m < 50 || m >= 105 && m < 110 {
			want = 546 // 9.1/s
		}
		if d := perMin[m] - want; d < -1 || d > 1 {
			t.Fatalf("minute %d: %d firings, want %d", m, perMin[m], want)
		}
	}
}

// TestV4PlannedOfferedRatePerMinute: the planned per-minute firing posts of
// every v4 lane (worker plan, paced cohorts, paced fingerprint rounds and the
// correlation budget) stay at 5/s, and at 10/s in the spike minutes.
func TestV4PlannedOfferedRatePerMinute(t *testing.T) {
	s := scenario.AlertingDayV4()
	load := s.Phase("drain").Start
	p := Compile("r", s.Seed, s.Baseline, load)
	perMin := make([]float64, int(load/time.Minute))
	for _, e := range p.Events {
		if e.Kind == Firing {
			perMin[int(e.At/time.Minute)]++
		}
	}
	ctl := s.ControllerFiringRate() * 60 // paced: spread evenly over each minute
	for m, n := range perMin {
		limit := scenario.V4TotalRate * 60
		for _, b := range s.Baseline.Bursts {
			if time.Duration(m)*time.Minute < b.At+b.For && b.At < time.Duration(m+1)*time.Minute {
				limit = scenario.V4SpikeRate * 60
			}
		}
		if n+ctl > limit+1 {
			t.Fatalf("minute %d: %.0f planned firing posts (worker %.0f + controller %.0f), budget %.0f", m, n+ctl, n, ctl, limit)
		}
	}
}
