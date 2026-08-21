package scenario

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type semanticEvent struct {
	Role     string
	Status   string
	Labels   map[string]string
	At       time.Duration
	Ordinal  int
	GroupKey string
	Identity string
}

type semanticContract struct {
	Pattern       string
	RequiredRoles []string
}

func validatePatternSemantics(document Scenario) error {
	contract := buildSemanticContract(document)
	for _, testCase := range document.Cases {
		events := expandSemanticEvents(testCase)
		if err := validateCaseSemantics(contract, testCase, events); err != nil {
			return fmt.Errorf("case %s: %w", testCase.Code, err)
		}
	}
	return nil
}

func buildSemanticContract(document Scenario) semanticContract {
	contract := semanticContract{Pattern: document.Pattern}
	if document.Pattern != "co_occurrence" {
		return contract
	}
	roles := map[string]bool{"disk_full": true, "cpu_high": true}
	for _, item := range document.Cases {
		if item.Role != "" {
			roles[item.Role] = true
		}
		for _, event := range item.Events {
			if event.Status == "firing" {
				roles[event.Role] = true
			}
		}
	}
	for role := range roles {
		contract.RequiredRoles = append(contract.RequiredRoles, role)
	}
	sort.Strings(contract.RequiredRoles)
	return contract
}

func expandSemanticEvents(testCase Case) []semanticEvent {
	source := testCase.Events
	if len(source) == 0 {
		for range testCase.Repeat {
			source = append(source, Event{Role: testCase.Role, Status: "firing"})
		}
	}
	result := make([]semanticEvent, 0, len(source))
	active := map[string]semanticEvent{}
	for index, event := range source {
		labels := cloneStringMap(testCase.Labels)
		for key, value := range event.Labels {
			labels[key] = value
		}
		observation := semanticEvent{Role: event.Role, Status: event.Status, Labels: labels, At: event.Delay, Ordinal: index}
		observation.GroupKey = semanticGroupKey(testCase.GroupBy, labels)
		if event.Status == "resolved" {
			if firing, found := active[event.Role]; found {
				observation.Labels = cloneStringMap(firing.Labels)
				observation.GroupKey = firing.GroupKey
				observation.Identity = firing.Identity
				delete(active, event.Role)
			}
		} else {
			observation.Identity = fmt.Sprintf("%s#%d", event.Role, index)
			active[event.Role] = observation
		}
		result = append(result, observation)
	}
	return result
}

func semanticGroupKey(groupBy []string, labels map[string]string) string {
	if len(groupBy) == 0 {
		return "*"
	}
	parts := make([]string, 0, len(groupBy))
	for _, key := range groupBy {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, "\x00")
}

func validateCaseSemantics(contract semanticContract, testCase Case, events []semanticEvent) error {
	switch contract.Pattern {
	case "flood":
		if err := validateSingleSourceRole(testCase, events); err != nil {
			return err
		}
		return validateFloodSemantics(testCase, events)
	case "co_occurrence":
		return validateCoOccurrenceSemantics(contract, testCase, events)
	case "sequence":
		return validateSequenceSemantics(testCase, events)
	case "persistence":
		if err := validateSingleSourceRole(testCase, events); err != nil {
			return err
		}
		return validatePersistenceSemantics(testCase, events)
	case "absence":
		if err := validateSingleSourceRole(testCase, events); err != nil {
			return err
		}
		return validateAbsenceSemantics(testCase, events)
	case "parent_child":
		return validateParentChildSemantics(testCase, events)
	case "cross_source":
		if err := validateSingleSourceRole(testCase, events); err != nil {
			return err
		}
		return validateCrossSourceSemantics(testCase, events)
	case "threshold":
		if err := validateSingleSourceRole(testCase, events); err != nil {
			return err
		}
		return validateThresholdSemantics(testCase, events)
	default:
		return fmt.Errorf("unsupported pattern %q", contract.Pattern)
	}
}

func validateSingleSourceRole(testCase Case, events []semanticEvent) error {
	roles := map[string]bool{}
	for _, event := range events {
		if event.Status == "firing" {
			roles[event.Role] = true
		}
	}
	if len(roles) != 1 {
		return fmt.Errorf("%s pattern requires exactly one firing role", testCase.Code)
	}
	for _, event := range events {
		if !roles[event.Role] {
			return fmt.Errorf("%s pattern resolution must reuse the firing role", testCase.Code)
		}
	}
	return nil
}

func validateFloodSemantics(testCase Case, events []semanticEvent) error {
	groups := map[string][]semanticEvent{}
	for _, event := range events {
		if event.Status == "firing" {
			groups[event.GroupKey] = append(groups[event.GroupKey], event)
		}
	}
	reachesFive := false
	for _, group := range groups {
		group = chronological(group)
		for start := range group {
			identities := map[string]bool{}
			for _, event := range group[start:] {
				if event.At-group[start].At > testCase.Window {
					break
				}
				identities[event.Identity] = true
			}
			if len(identities) >= 5 {
				reachesFive = true
			}
		}
	}
	if testCase.Code == "P01" && !reachesFive {
		return fmt.Errorf("P01 flood must reach five distinct firing occurrences in one group")
	}
	if testCase.Code == "N01" && reachesFive {
		return fmt.Errorf("N01 flood must remain below five distinct firing occurrences per group")
	}
	return nil
}

func validateCoOccurrenceSemantics(contract semanticContract, testCase Case, events []semanticEvent) error {
	groups := map[string][]semanticEvent{}
	for _, event := range events {
		if event.Status == "firing" {
			groups[event.GroupKey] = append(groups[event.GroupKey], event)
		}
	}
	complete := false
	for _, group := range groups {
		group = chronological(group)
		for start := range group {
			roles := map[string]bool{}
			for _, event := range group[start:] {
				if event.At-group[start].At > testCase.Window {
					break
				}
				roles[event.Role] = true
			}
			all := true
			for _, required := range contract.RequiredRoles {
				all = all && roles[required]
			}
			complete = complete || all
		}
	}
	if testCase.Code == "P01" && !complete {
		return fmt.Errorf("P01 co_occurrence must fire every required role in one group")
	}
	if testCase.Code == "N01" && complete {
		return fmt.Errorf("N01 co_occurrence must omit a required role from every group")
	}
	return nil
}

func validateSequenceSemantics(testCase Case, events []semanticEvent) error {
	valid := false
	for _, login := range events {
		if login.Status != "firing" || login.Role != "login_failure" {
			continue
		}
		for _, command := range events {
			if command.Status == "firing" && command.Role == "privileged_command" && command.GroupKey == login.GroupKey && precedes(login, command) && command.At-login.At <= testCase.Window {
				valid = true
			}
		}
	}
	if testCase.Code == "P01" && !valid {
		return fmt.Errorf("P01 sequence must order login_failure before privileged_command in one group")
	}
	if testCase.Code == "N01" && valid {
		return fmt.Errorf("N01 sequence must not contain the valid ordered pair in one group")
	}
	return nil
}

func validatePersistenceSemantics(testCase Case, events []semanticEvent) error {
	active := map[string]semanticEvent{}
	persists := false
	resolvesEarly := false
	for _, event := range chronological(events) {
		if event.Status == "firing" {
			active[event.Identity] = event
			continue
		}
		if firing, found := active[event.Identity]; found {
			if event.At-firing.At < 30*time.Second {
				resolvesEarly = true
			} else {
				persists = true
			}
			delete(active, event.Identity)
		}
	}
	for _, firing := range active {
		if firing.At+30*time.Second <= testCase.Window+15*time.Second {
			persists = true
		}
	}
	if testCase.Code == "P01" && !persists {
		return fmt.Errorf("P01 persistence must remain unresolved for 30s")
	}
	if testCase.Code == "N01" && (!resolvesEarly || persists) {
		return fmt.Errorf("N01 persistence must resolve before 30s")
	}
	return nil
}

func validateAbsenceSemantics(testCase Case, events []semanticEvent) error {
	groups := map[string][]time.Duration{}
	for _, event := range events {
		if event.Status == "firing" {
			groups[event.GroupKey] = append(groups[event.GroupKey], event.At)
		}
	}
	hasGap := false
	for _, times := range groups {
		sort.SliceStable(times, func(i, j int) bool { return times[i] < times[j] })
		for index, at := range times {
			next := 55 * time.Second
			if index+1 < len(times) {
				next = times[index+1]
			}
			if next-at >= 30*time.Second {
				hasGap = true
			}
		}
	}
	if testCase.Code == "P01" && !hasGap {
		return fmt.Errorf("P01 absence must contain a completed 30s heartbeat gap")
	}
	if testCase.Code == "N01" && hasGap {
		return fmt.Errorf("N01 absence must prevent a completed 30s heartbeat gap")
	}
	return nil
}

func validateParentChildSemantics(testCase Case, events []semanticEvent) error {
	activeParents := map[string]map[string]semanticEvent{}
	childCount, matchedChild := 0, false
	for _, event := range chronological(events) {
		switch {
		case event.Role == "parent" && event.Status == "firing":
			if activeParents[event.GroupKey] == nil {
				activeParents[event.GroupKey] = map[string]semanticEvent{}
			}
			activeParents[event.GroupKey][event.Identity] = event
		case event.Role == "parent" && event.Status == "resolved":
			delete(activeParents[event.GroupKey], event.Identity)
		case event.Role == "child" && event.Status == "firing":
			childCount++
			for _, parent := range activeParents[event.GroupKey] {
				matchedChild = matchedChild || (precedes(parent, event) && event.At-parent.At <= testCase.Window)
			}
		}
	}
	if testCase.Code == "P01" && !matchedChild {
		return fmt.Errorf("P01 parent_child requires an active parent before a child in one group")
	}
	if testCase.Code == "N01" && (childCount == 0 || matchedChild) {
		return fmt.Errorf("N01 parent_child requires an unmatched child")
	}
	return nil
}

func validateCrossSourceSemantics(testCase Case, events []semanticEvent) error {
	groups := map[string][]semanticEvent{}
	for _, event := range events {
		if event.Status == "firing" {
			groups[event.GroupKey] = append(groups[event.GroupKey], event)
		}
	}
	complete := false
	for _, group := range groups {
		group = chronological(group)
		for start := range group {
			sources := map[string]bool{}
			for _, event := range group[start:] {
				if event.At-group[start].At > testCase.Window {
					break
				}
				sources[event.Labels["oscar_source"]] = true
			}
			complete = complete || (sources["snmp"] && sources["api"])
		}
	}
	if testCase.Code == "P01" && !complete {
		return fmt.Errorf("P01 cross_source requires snmp and api in one group")
	}
	if testCase.Code == "N01" && complete {
		return fmt.Errorf("N01 cross_source must lack snmp or api in every group")
	}
	return nil
}

func validateThresholdSemantics(testCase Case, events []semanticEvent) error {
	groups := map[string][]semanticEvent{}
	for _, event := range events {
		if event.Status == "firing" {
			groups[event.GroupKey] = append(groups[event.GroupKey], event)
		}
	}
	reachesThree := false
	for _, group := range groups {
		group = chronological(group)
		for start := range group {
			devices := map[string]bool{}
			for _, event := range group[start:] {
				if event.At-group[start].At > testCase.Window {
					break
				}
				if device := event.Labels["device"]; device != "" {
					devices[device] = true
				}
			}
			reachesThree = reachesThree || len(devices) >= 3
		}
	}
	if testCase.Code == "P01" && !reachesThree {
		return fmt.Errorf("P01 threshold must reach three distinct device values in one group")
	}
	if testCase.Code == "N01" && reachesThree {
		return fmt.Errorf("N01 threshold must remain below three distinct device values per group")
	}
	return nil
}

func chronological(events []semanticEvent) []semanticEvent {
	result := append([]semanticEvent(nil), events...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].At < result[j].At })
	return result
}

func precedes(first, second semanticEvent) bool {
	return first.At < second.At || (first.At == second.At && first.Ordinal < second.Ordinal)
}
