package engine

import (
	"context"
	"sync"
	"time"
)

// Smoothed correlation pacing (Scenario.SmoothCorrelationOffers, OSC-189).
const (
	// smoothWindow is how many upcoming cycle documents the pacer may choose
	// from to land each minute near its plan.
	smoothWindow = 12
	// smoothDebt bounds how far a minute's target follows the lane's backlog
	// (plan so far minus offered so far), so a stall never becomes a burst.
	smoothDebt = 2.0
)

// smoothItem is a cycle document queued in the window, with its pass and
// when it was queued.
type smoothItem struct {
	corrItem
	pass   int
	queued time.Time
}

// smoothAge is how long the oldest window document may wait before it starts
// as soon as it fits, whatever smoothPick prefers (no document starves, and
// the smoke's two documents both keep running).
const smoothAge = time.Minute

// smoothPick chooses the window document to start with room alerts left in
// the minute: the one that, with the best single document after it, fills
// the most of the room without exceeding it (so 6+6 beats 10 in 12); ties go
// to the older document. -1 when none fits.
func smoothPick(window []smoothItem, room float64) int {
	pick, best := -1, -1.0
	for i, it := range window {
		if it.alerts > room {
			continue
		}
		fill := it.alerts
		for j, nx := range window {
			if j != i && fill < it.alerts+nx.alerts && it.alerts+nx.alerts <= room {
				fill = it.alerts + nx.alerts
			}
		}
		if fill > best {
			pick, best = i, fill
		}
	}
	return pick
}

// smoothCorrelation cycles set A then set B like cycleCorrelation, but aims
// every minute (aligned to T0, like the offered-rate buckets) at its plan:
// the integral of correlationRate over the minute, plus the backlog clamped
// to +-smoothDebt. Starts are spread across the minute, chosen among the
// next smoothWindow documents by smoothPick. Documents are still counted when they
// start; the pacer keeps its own count of what it started.
func (r *run) smoothCorrelation(ctx context.Context) {
	c := r.s.Correlation
	items := corrItems(c)
	slots := make(chan struct{}, max(c.Parallel, 1))
	var inFlight sync.WaitGroup
	defer inFlight.Wait() // the lane ends when its last run does
	drain := r.at("drain", false)
	laterStop := drain.Add(-cycleMargin) // later passes never run into the drain

	var window []smoothItem
	next := 0 // cycle index of the next document to queue
	fill := func(now time.Time) {
		for len(window) < smoothWindow {
			pass := next / len(items)
			if pass > 0 && !now.Before(laterStop) {
				return
			}
			window = append(window, smoothItem{items[next%len(items)], pass, now})
			next++
		}
	}
	notRun := func() { // pass-0 set A documents that never started
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, it := range window {
			if it.pass == 0 && it.kind == "setA" {
				r.notRun = append(r.notRun, "setA "+refName(it.ref))
			}
		}
		for i := next; i < len(c.SetA); i++ {
			r.notRun = append(r.notRun, "setA "+refName(items[i].ref))
		}
	}

	var offered, planned, minuteStart, target float64
	minute := -1
	for {
		now := r.now()
		if ctx.Err() != nil || !now.Before(drain) {
			notRun()
			return
		}
		if !now.Before(laterStop) { // only pass-0 documents may still start
			kept := window[:0]
			for _, it := range window {
				if it.pass == 0 {
					kept = append(kept, it)
				}
			}
			window = kept
		}
		fill(now)
		if len(window) == 0 {
			return
		}
		if m := int(now.Sub(r.t0) / time.Minute); m != minute {
			minute = m
			end := r.t0.Add(time.Duration(m+1) * time.Minute)
			plan := 0.0
			for t := now; t.Before(end); t = t.Add(time.Second) {
				plan += r.correlationRate(t) * min(time.Second, end.Sub(t)).Seconds()
			}
			debt := min(max(planned-offered, -smoothDebt), smoothDebt)
			planned += plan
			minuteStart, target = offered, offered+plan+debt
		}
		minuteEnd := r.t0.Add(time.Duration(minute+1) * time.Minute)
		frac := 1 - minuteEnd.Sub(now).Seconds()/time.Minute.Seconds() + correlationTick.Seconds()/time.Minute.Seconds()
		for len(slots) < cap(slots) && offered < minuteStart+(target-minuteStart)*min(frac, 1) {
			pick := smoothPick(window, target-offered)
			if w := window[0]; now.Sub(w.queued) >= smoothAge && offered+w.alerts <= target {
				pick = 0
			}
			if pick < 0 {
				break
			}
			it := window[pick]
			window = append(window[:pick], window[pick+1:]...)
			offered += it.alerts
			if it.pass > 0 && it.kind == "setA" {
				r.mu.Lock()
				r.plannedCorr++
				r.mu.Unlock()
			}
			slots <- struct{}{}
			inFlight.Add(1)
			go func() {
				defer inFlight.Done()
				defer func() { <-slots }()
				if !r.correlate(ctx, it.ref, it.kind, it.ticket) {
					r.e.clock.SleepUntil(ctx, r.now().Add(setBBackoff)) // a run that did not start must not spin
				}
			}()
			fill(now)
			if len(window) == 0 {
				break
			}
		}
		r.e.clock.SleepUntil(ctx, now.Add(correlationTick))
	}
}
