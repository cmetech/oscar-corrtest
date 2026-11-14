// Package lane compiles deterministic, seeded campaign load-lane schedules.
package lane

import (
	"fmt"
	"math/rand/v2"
	"sort"
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

// Compile builds the baseline plan: firings every 1/Rate seconds for duration,
// each resolved ResolveAfter later. Events are sorted by At, then Seq.
func Compile(runID string, seed int64, spec scenario.BaselineSpec, duration time.Duration) Plan {
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
		e.Instance = fmt.Sprintf("campaign-host-%02d", rng.IntN(spec.Instances)+1)
		fires = append(fires, e)
	}
	// Firings precede resolves; the stable sort keeps that order on ties.
	all := append([]Event(nil), fires...)
	for i, f := range fires {
		f.Kind, f.At, f.Ticket, f.FiringSeq = Resolve, f.At+spec.ResolveAfter, false, i
		all = append(all, f)
	}
	for i := range all {
		all[i].FiringSeq = map[bool]int{true: i, false: all[i].FiringSeq}[all[i].Kind == Firing]
	}
	idx := make([]int, len(all)) // original index = firing index for firings
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return all[idx[a]].At < all[idx[b]].At })
	pos := make(map[int]int, n) // firing index -> final Seq
	for s, o := range idx {
		if o < n {
			pos[o] = s
		}
	}
	p.Events = make([]Event, len(all))
	for s, o := range idx {
		e := all[o]
		e.Seq = s
		e.FiringSeq = pos[e.FiringSeq]
		p.Events[s] = e
		switch {
		case e.Kind == Resolve:
			p.Resolves++
		default:
			p.Firings++
			if e.Ticket {
				p.Tickets++
			}
		}
	}
	return p
}
