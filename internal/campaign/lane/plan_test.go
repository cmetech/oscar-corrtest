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
	if p.Firings != 25200 || p.Resolves != p.Firings {
		t.Fatalf("firings=%d resolves=%d", p.Firings, p.Resolves)
	}
	at := map[int]time.Duration{}
	for _, e := range p.Events {
		if e.Kind == "firing" {
			at[e.Seq] = e.At
		}
	}
	for _, e := range p.Events {
		if e.Kind == "resolve" && e.At != at[e.FiringSeq]+15*time.Minute {
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
