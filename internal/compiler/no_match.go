package compiler

import "fmt"

// ValidateControlExclusion proves only disjointness from this compiled Flood fixture.
// A positive downstream audit is still required; this is not receiver selector proof.
func ValidateControlExclusion(plan Plan) error {
	for _, item := range plan.Cases {
		if item.Control == "" {
			continue
		}
		if item.Control != "downstream-unmatched" || plan.Pattern != "flood" || item.Code != "N01" {
			return fmt.Errorf("unsupported downstream control")
		}
		if len(item.Alerts) == 0 {
			return fmt.Errorf("downstream control has no inputs")
		}
		seen := map[string]bool{}
		seenSources := map[string]bool{}
		for _, alert := range item.Alerts {
			eventID := alert.Labels["oscar_test_event_id"]
			sourceKey := alert.Name + "\x00" + alert.Source
			if eventID == "" || seen[eventID] || seenSources[sourceKey] {
				return fmt.Errorf("downstream control requires unique input identities")
			}
			seen[eventID], seenSources[sourceKey] = true, true
			if alert.Name == "" || alert.Labels["alertname"] != alert.Name || alert.Source == "" || alert.EpisodeOffset == nil || alert.Status != "firing" {
				return fmt.Errorf("downstream control input identity is incomplete")
			}
			for _, fixture := range plan.Cases {
				if fixture.Rule.Pattern != "flood" || len(fixture.Rule.MatchCriteria) != 2 || fixture.Rule.MatchCriteria["min_count"] == nil {
					return fmt.Errorf("cannot prove control exclusion for fixture rule %q", fixture.Rule.Name)
				}
				name := ""
				switch match := fixture.Rule.MatchCriteria["match"].(type) {
				case map[string]string:
					if len(match) == 1 {
						name = match["alertname"]
					}
				case map[string]any:
					if len(match) == 1 {
						name, _ = match["alertname"].(string)
					}
				}
				if name == "" || alert.Name == name {
					return fmt.Errorf("control is not provably disjoint from fixture rule %q", fixture.Rule.Name)
				}
			}
		}
	}
	return nil
}
