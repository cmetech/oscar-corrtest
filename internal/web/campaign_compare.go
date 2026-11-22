package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type campaignComparisonRequest struct {
	candidateIDs, referenceIDs []string
	scenarioHash               string
	mode                       score.ComparisonMode
}

func comparisonRequest(query url.Values) campaignComparisonRequest {
	return campaignComparisonRequest{
		candidateIDs: splitIDs(query.Get("ids")), referenceIDs: splitIDs(query.Get("referenceIds")),
		scenarioHash: strings.TrimSpace(query.Get("scenarioHash")), mode: score.ComparisonMode(query.Get("mode")),
	}
}

// compareCampaigns loads only the IDs in the request, then compares their stored evidence.
func compareCampaigns(ctx context.Context, ctl campaignController, req campaignComparisonRequest) (score.Comparison, error) {
	if len(req.referenceIDs) == 0 {
		return score.Comparison{}, score.ErrMissingBaseline
	}
	if len(req.referenceIDs) > score.MaxComparisonReferences || len(req.candidateIDs) == 0 {
		return score.Comparison{}, fmt.Errorf("%w: select one to three references and at least one candidate", score.ErrIncompatibleComparison)
	}
	load := func(ids []string) ([]score.IdentifiedScorecard, error) {
		runs := make([]score.IdentifiedScorecard, 0, len(ids))
		for _, id := range ids {
			view, err := ctl.CampaignStatus(ctx, id)
			if err != nil {
				return nil, err
			}
			c := view.Campaign
			card, ok := parseCard(c)
			if !ok || c.ID != id || card.Verdict != c.Verdict {
				return nil, fmt.Errorf("%w: run %q has no consistent stored scorecard", score.ErrIncompatibleComparison, id)
			}
			var report struct {
				FullLoadCompleted bool `json:"fullLoadCompleted"`
			}
			if err := json.Unmarshal(c.Report, &report); err != nil {
				return nil, fmt.Errorf("%w: run %q has no stored completion evidence", score.ErrIncompatibleComparison, id)
			}
			card.RunBy = c.RunBy
			run := score.IdentifiedScorecard{
				RunID: c.ID, ScenarioHash: c.ScenarioHash, Status: c.Status, Cleanup: c.Cleanup,
				Comparable: c.Comparable, Custom: c.Custom, FullLoadCompleted: report.FullLoadCompleted,
				StartedAt: c.StartedAt, EndedAt: c.EndedAt, Scorecard: card,
			}
			if view.Options != nil {
				run.Acknowledged, run.PrecheckOnly = view.Options.Acknowledged, view.Options.PrecheckOnly
				if view.Options.Provenance != nil {
					run.Provenance = *view.Options.Provenance
				}
			}
			runs = append(runs, run)
		}
		return runs, nil
	}
	references, err := load(req.referenceIDs)
	if err != nil {
		return score.Comparison{}, err
	}
	candidates, err := load(req.candidateIDs)
	if err != nil {
		return score.Comparison{}, err
	}
	return score.CompareCompatible(references, candidates, req.scenarioHash, req.mode)
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
