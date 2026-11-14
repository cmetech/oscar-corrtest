package lane

import (
	"context"
	"errors"
	"net"
	"time"
)

type Clock interface {
	Now() time.Time
	SleepUntil(ctx context.Context, t time.Time) bool
}

type Ledger interface {
	Intent(ctx context.Context, laneID string, e Event, at time.Time) error
	Outcome(ctx context.Context, laneID string, seq int, o Outcome) error
}

// Outcome.ErrorClass is "", "http", "timeout", "network" or "missed".
type Outcome struct {
	SentAt        time.Time
	Status        int
	ErrorClass    string
	LatencyMillis int64
	Missed        bool
}

type Counters struct {
	Offered, Accepted, Failed, Unknown, Missed int
	MaxLagMillis                               int64
}

// Run sends plan events at start+At. Each event gets a ledger intent before its
// send. Sends of one kind are spaced at least the plan's firing period apart,
// and an event more than max(1s, 5% of elapsed) late is recorded Missed and
// never sent, so a stall is never followed by a catch-up burst. A ledger write
// failure stops the lane (it must not send what it cannot record).
func Run(ctx context.Context, laneID string, plan Plan, start time.Time, s Sender, l Ledger, c Clock, onCount func(Counters)) Counters {
	var cnt Counters
	stable := context.WithoutCancel(ctx)
	period := firingPeriod(plan)
	last := map[EventKind]time.Time{}
	for _, e := range plan.Events {
		sched := start.Add(e.At)
		due := sched
		if p := last[e.Kind].Add(period); p.After(due) {
			due = p
		}
		if ctx.Err() != nil || !c.SleepUntil(ctx, due) {
			break
		}
		if l.Intent(stable, laneID, e, sched) != nil {
			break
		}
		cnt.Offered++
		now := c.Now()
		lag := now.Sub(sched)
		if ms := lag.Milliseconds(); ms > cnt.MaxLagMillis {
			cnt.MaxLagMillis = ms
		}
		var o Outcome
		if lag > max(time.Second, e.At/20) {
			cnt.Missed++
			o = Outcome{SentAt: now, ErrorClass: "missed", Missed: true}
		} else {
			last[e.Kind] = now
			status, err := s.Send(ctx, e, plan.RunID, laneID)
			o = Outcome{SentAt: now, Status: status, LatencyMillis: c.Now().Sub(now).Milliseconds()}
			switch {
			case err != nil:
				cnt.Unknown++
				o.ErrorClass = "network"
				var ne net.Error
				if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
					o.ErrorClass = "timeout"
				}
			case status >= 200 && status < 300:
				cnt.Accepted++
			default:
				cnt.Failed++
				o.ErrorClass = "http"
			}
		}
		werr := l.Outcome(stable, laneID, e.Seq, o)
		if onCount != nil {
			onCount(cnt)
		}
		if werr != nil {
			break
		}
	}
	return cnt
}

// firingPeriod is the smallest positive gap between consecutive firings (1/Rate).
func firingPeriod(p Plan) time.Duration {
	var period, prev time.Duration
	seen := false
	for _, e := range p.Events {
		if e.Kind != Firing {
			continue
		}
		if g := e.At - prev; seen && g > 0 && (period == 0 || g < period) {
			period = g
		}
		prev, seen = e.At, true
	}
	return period
}
