package scenario

import "fmt"

func validateDownstreamControl(pattern string, item Case) error {
	assertions := append([]Assertion{}, item.Assertions...)
	for _, point := range item.Checkpoints {
		assertions = append(assertions, point.Assertions...)
	}
	if item.Control == "" {
		for _, assertion := range assertions {
			if assertion.Kind == "downstream-no-match-count" {
				return fmt.Errorf("case %q no-match witness requires downstream-unmatched control", item.Name)
			}
		}
		return nil
	}
	if item.Control != "downstream-unmatched" || pattern != "flood" || item.Code != "N01" {
		return fmt.Errorf("downstream-unmatched control is available only for Flood N01")
	}
	if len(item.Events) == 0 {
		return fmt.Errorf("downstream control requires explicitly named firing events")
	}
	seen := map[string]bool{}
	for _, event := range item.Events {
		if event.Source == "" || event.Status != "firing" || seen[event.IdentityKey()] {
			return fmt.Errorf("downstream control requires unique named single firing episodes; no recovery/refire audit binding is available")
		}
		seen[event.IdentityKey()] = true
	}
	for _, assertion := range assertions {
		if assertion.Kind != "downstream-no-match-count" || assertion.Equals <= 0 {
			return fmt.Errorf("downstream control requires positive downstream-no-match-count assertions")
		}
	}
	return nil
}
