// Package lane compiles deterministic, seeded campaign load-lane schedules.
package lane

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

type EventKind string

const (
	Firing  EventKind = "firing"
	Resolve EventKind = "resolve"
)

// Event is one scheduled send. FiringSeq is the Seq of the firing a resolve
// closes (and equals Seq on firings).
type Event struct {
	Seq       int
	At        time.Duration
	Kind      EventKind
	Identity  string
	Severity  string
	Instance  string
	Ticket    bool
	FiringSeq int
}

type Plan struct {
	RunID                      string
	Seed                       int64
	Events                     []Event
	Firings, Resolves, Tickets int
}

// Compile builds the baseline plan: firings every 1/Rate seconds for duration.
// Resolves follow the episode rule: an episode is a run of firings of one
// identity whose gaps are each < ResolveAfter, and it gets exactly one resolve
// at lastFiringAt + ResolveAfter (FiringSeq points at that last firing). New
// identities fire once, so they resolve once. Plan.Resolves counts episodes.
// Events are sorted by At, then Seq.
func Compile(runID string, seed int64, spec scenario.BaselineSpec, duration time.Duration) Plan {
	// #nosec G404 G115 -- deterministic seeded plan, not security-sensitive; int64->uint64 is a bit-preserving reinterpretation of the seed, any value is valid.
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	// Sorted keys so the severity pick is deterministic despite map order.
	keys := make([]string, 0, len(spec.Severity))
	for k := range spec.Severity {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	n := int(duration.Seconds() * spec.Rate)
	p := Plan{RunID: runID, Seed: seed}
	var fires []Event
	newCount := 0
	poolLabels := map[string][2]string{}
	for i := 0; i < n; i++ {
		e := Event{Kind: Firing, At: time.Duration(float64(i) * float64(time.Second) / spec.Rate)}
		if rng.Float64() < spec.UniqueRatio {
			newCount++
			e.Identity = fmt.Sprintf("Campaign-%s-%d", runID, newCount)
			e.Ticket = spec.TicketEveryNth > 0 && newCount%spec.TicketEveryNth == 0 &&
				e.At >= spec.TicketFrom && e.At < spec.TicketTo
		} else {
			e.Identity = fmt.Sprintf("Campaign-%s-pool-%d", runID, rng.IntN(spec.PoolSize))
		}
		r, acc := rng.Float64(), 0.0
		e.Severity = keys[len(keys)-1]
		for _, k := range keys {
			if acc += spec.Severity[k]; r < acc {
				e.Severity = k
				break
			}
		}
		e.Instance = InstanceName(rng.IntN(spec.Instances) + 1)
		// Pool identities keep the labels of their first draw (RNG use is unchanged),
		// so every firing and resolve of one identity is the same Alertmanager alert.
		if l, ok := poolLabels[e.Identity]; ok {
			e.Severity, e.Instance = l[0], l[1]
		} else if strings.Contains(e.Identity, "-pool-") {
			poolLabels[e.Identity] = [2]string{e.Severity, e.Instance}
		}
		fires = append(fires, e)
	}
	// A firing gets a resolve only if its identity does not fire again within
	// ResolveAfter, so each episode closes once, at its last firing + ResolveAfter.
	all := append([]Event(nil), fires...) // firings first; the sort below puts resolves ahead on At ties
	nextAt := map[string]time.Duration{}
	var resolves []Event
	for i := len(fires) - 1; i >= 0; i-- {
		f, at := fires[i], fires[i].At
		if nx, ok := nextAt[f.Identity]; !ok || nx >= f.At+spec.ResolveAfter {
			f.Kind, f.At, f.FiringSeq = Resolve, f.At+spec.ResolveAfter, i // Ticket is kept so the resolve matches the firing's labels
			resolves = append(resolves, f)
		}
		nextAt[f.Identity] = at
	}
	for i := len(resolves) - 1; i >= 0; i-- { // restore firing order before the stable sort
		all = append(all, resolves[i])
	}
	idx := make([]int, len(all))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		x, y := all[idx[a]], all[idx[b]]
		if x.At != y.At {
			return x.At < y.At
		}
		return x.Kind == Resolve && y.Kind == Firing // tie: resolve first so an old episode's resolve cannot close a new one
	})
	seqOf := make([]int, n) // firing index -> final Seq
	for s, o := range idx {
		if o < n {
			seqOf[o] = s
		}
	}
	p.Events = make([]Event, len(all))
	for s, o := range idx {
		e := all[o]
		e.Seq = s
		if e.Kind == Firing {
			e.FiringSeq = s
			p.Firings++
			if e.Ticket {
				p.Tickets++
			}
		} else {
			e.FiringSeq = seqOf[e.FiringSeq]
			p.Resolves++
		}
		p.Events[s] = e
	}
	return p
}

// InstanceName is the campaign host label for 1-based index i.
func InstanceName(i int) string { return fmt.Sprintf("campaign-host-%02d", i) }
