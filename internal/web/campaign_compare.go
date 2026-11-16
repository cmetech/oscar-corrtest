package web

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type compareItem struct {
	ID     string
	RunBy  string
	Flags  []string // custom, other scenario, not comparable
	Deltas []score.Delta
}

type compareResult struct {
	Reference score.Scorecard
	Items     []compareItem
}

// compareCampaigns scores each id against the reference: the median of the first three
// comparable, clean, PASS completed campaigns (oldest first). Flags mark runs that
// should not be read against it; RunBy stays visible so agent and human runs are distinguishable.
func compareCampaigns(ctx context.Context, ctl campaignController, ids []string) (compareResult, error) {
	all, err := ctl.ListCampaigns(ctx, domain.CampaignFilter{Status: domain.CampaignCompleted})
	if err != nil {
		return compareResult{}, err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].StartedAt.Before(all[j].StartedAt) })
	var cards []score.Scorecard
	for _, c := range all {
		if card, ok := parseCard(c); ok {
			cards = append(cards, card)
		}
	}
	res := compareResult{Reference: score.Reference(cards)}
	for _, id := range ids {
		v, err := ctl.CampaignStatus(ctx, id)
		if err != nil {
			return compareResult{}, err
		}
		it := compareItem{ID: id, RunBy: v.Campaign.RunBy}
		card, ok := parseCard(v.Campaign)
		if v.Campaign.Custom {
			it.Flags = append(it.Flags, "custom scenario")
		}
		if !ok {
			it.Flags = append(it.Flags, "no scorecard")
		} else {
			if card.ScenarioHash != res.Reference.ScenarioHash {
				it.Flags = append(it.Flags, "other scenario version")
			}
			it.Deltas = score.CompareTo(card, res.Reference)
		}
		if !v.Campaign.Comparable || (ok && !card.Comparable) {
			it.Flags = append(it.Flags, "not comparable")
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

func parseCard(c domain.Campaign) (score.Scorecard, bool) {
	var card score.Scorecard
	if len(c.Scorecard) == 0 || json.Unmarshal(c.Scorecard, &card) != nil {
		return card, false
	}
	return card, true
}

func splitIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
