package runner

import (
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// This is History visibility, not an episode application or physical-clear receipt.
type sourceEpisodeEvidence struct {
	Source           string                `json:"source"`
	EventID          string                `json:"eventId"`
	ExpectedStartsAt time.Time             `json:"expectedStartsAt"`
	Rows             []oscar.HistoryRecord `json:"rows"`
	Ready            bool                  `json:"ready"`
	Status           string                `json:"status,omitempty"`
}

func usesSourceEpisodes(assertions []scenario.Assertion) bool {
	for _, assertion := range assertions {
		if assertion.Kind == "source-episode-state-count" || assertion.Kind == "downstream-no-match-count" {
			return true
		}
	}
	return false
}

func onlySourceEpisodes(assertions []scenario.Assertion) bool {
	if len(assertions) == 0 {
		return false
	}
	for _, assertion := range assertions {
		if assertion.Kind != "source-episode-state-count" {
			return false
		}
	}
	return true
}

func bindSourceEpisodes(item compiler.CasePlan, rows []oscar.HistoryRecord) ([]sourceEpisodeEvidence, bool) {
	// Preserve input order while replacing a named source with its latest sent episode.
	latest := map[string]compiler.AlertPlan{}
	var keys []string
	for _, alert := range item.Alerts {
		key := alert.Labels["oscar_test_event_id"]
		if _, found := latest[key]; !found {
			keys = append(keys, key)
		}
		latest[key] = alert
	}
	evidence := make([]sourceEpisodeEvidence, 0, len(keys))
	allReady := len(keys) > 0
	for _, key := range keys {
		alert := latest[key]
		proof := sourceEpisodeEvidence{Source: alert.Source, EventID: key}
		valid := key != "" && alert.Source != "" && alert.EpisodeOffset != nil && !item.EpisodeBase.IsZero()
		if valid {
			proof.ExpectedStartsAt = item.EpisodeBase.Add(*alert.EpisodeOffset)
		}
		fingerprint := ""
		for _, row := range rows {
			if row.AlertName != alert.Name || row.Labels["oscar_test_event_id"] != key {
				continue
			}
			proof.Rows = append(proof.Rows, row)
			if row.StartsAt.IsZero() || row.StartsAt.After(proof.ExpectedStartsAt) {
				valid = false
				continue
			}
			if row.StartsAt.Before(proof.ExpectedStartsAt) {
				continue
			}
			if row.Fingerprint == "" || (row.Status != "firing" && row.Status != "resolved") {
				valid = false
			}
			for label, value := range alert.Labels {
				if !row.MatchesInputLabel(label, value) {
					valid = false
				}
			}
			if fingerprint != "" && (fingerprint != row.Fingerprint || proof.Status != row.Status) {
				valid = false
			}
			fingerprint, proof.Status = row.Fingerprint, row.Status
		}
		proof.Ready = valid && fingerprint != ""
		allReady = allReady && proof.Ready
		evidence = append(evidence, proof)
	}
	return evidence, allReady
}
