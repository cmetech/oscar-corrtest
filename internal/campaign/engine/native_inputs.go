package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type sourceOffers struct {
	Offered, Accepted, Unknown int
	InLoad                     int
}

func (s *sourceOffers) add(at, from, to time.Time, status int) {
	s.Offered++
	if status/100 == 2 {
		s.Accepted++
	} else {
		s.Unknown++
	}
	if !at.Before(from) && at.Before(to) {
		s.InLoad++
	}
}

// Child reports carry final outcomes. Keep their cleanup clears separately.
// The child run records the send time of each data attempt and every cleanup
// clear, including legacy cleanup paths without exact body capture.
func (r *run) childOffers(ctx context.Context, t *nativeTally) {
	links, err := r.store().CampaignRuns(ctx, r.id)
	if err != nil {
		t.miss("correlation child links")
		return
	}
	db, ok := r.store().(interface {
		GetRun(context.Context, string) (domain.Run, error)
		ListRunEvents(context.Context, string) ([]domain.RunEvent, error)
	})
	if !ok {
		t.miss("child run evidence reader")
		return
	}
	for _, link := range links {
		child, err := db.GetRun(ctx, link.RunID)
		if err != nil {
			t.miss("correlation child report")
			continue
		}
		var rep struct {
			RunID       string
			Attempts    []domain.AlertAttemptFact
			Resolutions []domain.AlertResolutionFact
		}
		if json.Unmarshal(child.CanonicalReportJSON, &rep) != nil || rep.RunID != link.RunID || len(rep.Attempts) == 0 {
			t.miss("child canonical input evidence")
			continue
		}
		events, err := db.ListRunEvents(ctx, link.RunID)
		if err != nil {
			t.miss("child attempt timeline")
			continue
		}
		var attempts []time.Time
		for _, ev := range events {
			switch ev.Type {
			case "alert.attempting":
				attempts = append(attempts, ev.OccurredAt)
			}
		}
		if len(attempts) != len(rep.Attempts) {
			t.miss("child timeline differs from source outcomes")
			continue
		}
		for i, a := range rep.Attempts {
			t.offer("correlation", attempts[i], a.StatusCode)
		}
		for _, a := range rep.Resolutions {
			if a.OfferedAt.IsZero() {
				t.miss("child cleanup timing unavailable")
				continue
			}
			t.offer("correlation_clear", a.OfferedAt, a.StatusCode)
		}
	}
}
func (t *nativeTally) offer(cohort string, at time.Time, status int) {
	s := t.SourceOffers[cohort]
	s.add(at, t.run.t0, t.run.t0.Add(t.run.s.LoadEnd), status)
	t.SourceOffers[cohort] = s
}

func (r *run) sourceOffer(cohort string, at time.Time, status int) {
	if r.s.NativeWorkload == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sourceTotals == nil {
		r.sourceTotals = map[string]sourceOffers{}
	}
	v := r.sourceTotals[cohort]
	v.add(at, r.t0, r.t0.Add(r.s.LoadEnd), status)
	r.sourceTotals[cohort] = v
}
