package engine

import (
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// A planned resolve separates two episodes of the same pooled name. The
// existing worker sends startsAt from its clock inside the HTTP call. Use the
// accepted firing's measured send bracket, allowing millisecond DB precision.
type baselineEpisode struct {
	fires     []domain.LaneRow
	history   map[string]oscar.HistoryRecord
	delivered bool
}

func (e *baselineEpisode) contains(at time.Time) bool {
	for _, r := range e.fires {
		if !at.Before(r.SentAt.Truncate(time.Millisecond)) && at.Before(r.SentAt.Add(time.Duration(r.LatencyMillis+1)*time.Millisecond)) {
			return true
		}
	}
	return false
}
func baselineEpisodes(t *nativeTally, plan lane.Plan, in score.Inputs) {
	ledger := map[int]domain.LaneRow{}
	for _, r := range in.Ledger {
		if !r.Missed && r.Done && r.ErrorClass == "" && r.Status/100 == 2 {
			ledger[r.Seq] = r
		}
	}
	open := map[string]*baselineEpisode{}
	episodes := map[string][]*baselineEpisode{}
	for _, p := range plan.Events {
		if p.Kind == lane.Resolve {
			delete(open, p.Identity)
			continue
		}
		ep := open[p.Identity]
		if ep == nil {
			ep = &baselineEpisode{history: map[string]oscar.HistoryRecord{}}
			open[p.Identity] = ep
			episodes[p.Identity] = append(episodes[p.Identity], ep)
		}
		if r, ok := ledger[p.Seq]; ok {
			ep.fires = append(ep.fires, r)
		}
	}
	for _, h := range in.History {
		if h.ID == "" || h.Status != "resolved" {
			continue
		}
		for _, ep := range episodes[h.AlertName] {
			if ep.contains(h.StartsAt) {
				ep.history[h.ID] = h
			}
		}
	}
	for _, rc := range in.Receipts {
		for _, a := range rc.Alerts {
			eps := episodes[a.AlertName]
			if len(eps) == 0 {
				continue
			}
			t.receipts([]targetops.Receipt{{ID: rc.ID, Alerts: []targetops.SinkAlert{a}}}, a.AlertName)
			if a.Status != "firing" || rc.Notifier != t.run.e.d.CampaignNotifier {
				continue
			}
			start, err := time.Parse(time.RFC3339Nano, a.StartsAt)
			if err != nil || rc.ReceivedAt.Before(start) {
				continue
			}
			for _, ep := range eps {
				if !ep.contains(start) {
					continue
				}
				for _, h := range ep.history {
					if a.HistoryID != "" && a.HistoryID != h.ID {
						continue
					}
					if a.HistoryID == h.ID || start.Truncate(time.Millisecond).Equal(h.StartsAt.Truncate(time.Millisecond)) {
						ep.delivered = true
					}
				}
			}
		}
	}
	for _, eps := range episodes {
		for _, ep := range eps {
			t.Facets["baseline_planned_episodes"]++
			if len(ep.history) != 1 || !ep.delivered {
				t.miss("baseline episode lacks unique resolved History and attributable firing sink member")
				continue
			}
			t.Facets["baseline_delivered_episodes"]++
		}
	}
}
