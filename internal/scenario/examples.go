package scenario

import "time"

type ExampleDefinition struct {
	ID       string
	Pattern  string
	Level    string
	Title    string
	Summary  string
	Scenario Scenario
}

// AllExamples returns the complete executable cookbook in supported-pattern order.
func AllExamples() []ExampleDefinition {
	metadata := map[string]struct{ title, summary string }{
		"flood":         {"Flood", "Count distinct source occurrences within a grouping window."},
		"co_occurrence": {"Co-occurrence", "Require several logical alert roles in one group."},
		"sequence":      {"Sequence", "Recognize login failure followed by a privileged command."},
		"persistence":   {"Persistence", "Require an alert identity to remain unresolved for 30 seconds."},
		"absence":       {"Absence", "Detect a completed 30-second heartbeat gap."},
		"parent_child":  {"Parent-child", "Link a child alert to an active parent with notifier policy."},
		"cross_source":  {"Cross-source", "Require one semantic alert from both SNMP and API sources."},
		"threshold":     {"Threshold", "Count three distinct device values within one group."},
	}
	result := make([]ExampleDefinition, 0, len(supportedPatterns)*2)
	for _, pattern := range supportedPatterns {
		meta := metadata[pattern]
		basic := Builtin(pattern)
		result = append(result,
			ExampleDefinition{ID: pattern + ":basic", Pattern: pattern, Level: "basic", Title: meta.title + " basic", Summary: meta.summary, Scenario: cloneScenario(basic)},
			ExampleDefinition{ID: pattern + ":advanced", Pattern: pattern, Level: "advanced", Title: meta.title + " advanced", Summary: "Advanced grouping and boundary control. " + meta.summary, Scenario: advancedExample(pattern)},
		)
	}
	return result
}

// LookupExample returns a defensive copy for an exact canonical pattern and level.
func LookupExample(pattern, level string) (ExampleDefinition, bool) {
	if !isSupportedPattern(pattern) || (level != "basic" && level != "advanced") {
		return ExampleDefinition{}, false
	}
	for _, example := range AllExamples() {
		if example.Pattern == pattern && example.Level == level {
			example.Scenario = cloneScenario(example.Scenario)
			return example, true
		}
	}
	return ExampleDefinition{}, false
}

func advancedExample(pattern string) Scenario {
	document := Builtin(pattern)
	document.Name = pattern + "-advanced"
	document.Suite = "cookbook-advanced"
	caseWithEvents := func(code string, events ...Event) {
		item := caseByCodeValue(&document, code)
		item.Role, item.Repeat = "", 0
		item.Events = events
	}
	event := func(role string, at time.Duration, labels map[string]string) Event {
		return Event{Role: role, Status: "firing", Delay: at, Labels: labels}
	}
	switch pattern {
	case "flood":
		for index := range document.Cases {
			document.Cases[index].GroupBy = []string{"site", "service"}
			document.Cases[index].Labels["service"] = "checkout"
		}
		caseWithEvents("P01",
			event("interface_down", 0, map[string]string{"identity": "link-1"}),
			event("interface_down", time.Second, map[string]string{"identity": "link-2"}),
			event("interface_down", 2*time.Second, map[string]string{"identity": "link-3"}),
			event("interface_down", 3*time.Second, map[string]string{"identity": "link-4"}),
			event("interface_down", 4*time.Second, map[string]string{"identity": "link-5"}),
		)
		caseWithEvents("N01",
			event("interface_down", 0, map[string]string{"identity": "link-1", "site": "edge-a"}),
			event("interface_down", time.Second, map[string]string{"identity": "link-2", "site": "edge-a"}),
			event("interface_down", 2*time.Second, map[string]string{"identity": "link-3", "site": "edge-a"}),
			event("interface_down", 3*time.Second, map[string]string{"identity": "link-4", "site": "edge-b"}),
			event("interface_down", 4*time.Second, map[string]string{"identity": "link-5", "site": "edge-b"}),
		)
	case "co_occurrence":
		caseWithEvents("P01", event("disk_full", 0, nil), event("cpu_high", time.Second, nil), event("memory_pressure", 2*time.Second, nil))
		caseWithEvents("N01", event("disk_full", 0, nil), event("cpu_high", time.Second, nil))
	case "sequence":
		for index := range document.Cases {
			document.Cases[index].GroupBy = []string{"site", "service"}
			document.Cases[index].Labels["service"] = "admin"
		}
		caseWithEvents("P01", event("login_failure", 2*time.Second, nil), event("privileged_command", 7*time.Second, nil))
		caseWithEvents("N01", event("privileged_command", 2*time.Second, nil), event("login_failure", 7*time.Second, nil))
	case "persistence":
		caseWithEvents("P01", event("service_down", 0, map[string]string{"service": "checkout"}))
		caseWithEvents("N01", event("service_down", 0, map[string]string{"service": "checkout"}), Event{Role: "service_down", Status: "resolved", Delay: 29 * time.Second})
	case "absence":
		caseWithEvents("P01", event("heartbeat", 0, nil))
		caseWithEvents("N01",
			event("heartbeat", 0, nil), event("heartbeat", 10*time.Second, nil), event("heartbeat", 20*time.Second, nil),
			event("heartbeat", 30*time.Second, nil), event("heartbeat", 40*time.Second, nil), event("heartbeat", 50*time.Second, nil),
		)
	case "parent_child":
		for index := range document.Cases {
			document.Cases[index].SuppressForNotifiers = []string{"email", "slack"}
			document.Cases[index].TagForNotifiers = []string{"pagerduty", "sms"}
		}
		caseWithEvents("P01", event("parent", 0, nil), event("child", 3*time.Second, nil))
		caseWithEvents("N01", event("child", 3*time.Second, nil))
	case "cross_source":
		for index := range document.Cases {
			document.Cases[index].GroupBy = []string{"site", "service"}
			document.Cases[index].Labels["service"] = "router"
		}
		caseWithEvents("P01",
			event("interface_down", 0, map[string]string{"oscar_source": "snmp"}),
			event("interface_down", time.Second, map[string]string{"oscar_source": "api"}),
		)
		caseWithEvents("N01",
			event("interface_down", 0, map[string]string{"oscar_source": "snmp", "site": "edge-a"}),
			event("interface_down", time.Second, map[string]string{"oscar_source": "api", "site": "edge-b"}),
		)
	case "threshold":
		for index := range document.Cases {
			document.Cases[index].GroupBy = []string{"site", "service"}
			document.Cases[index].Labels["service"] = "compute"
		}
		caseWithEvents("P01",
			event("cpu_high", 0, map[string]string{"device": "edge-1"}),
			event("cpu_high", time.Second, map[string]string{"device": "edge-2"}),
			event("cpu_high", 2*time.Second, map[string]string{"device": "edge-3"}),
		)
		caseWithEvents("N01",
			event("cpu_high", 0, map[string]string{"device": "edge-1", "site": "edge-a"}),
			event("cpu_high", time.Second, map[string]string{"device": "edge-2", "site": "edge-a"}),
			event("cpu_high", 2*time.Second, map[string]string{"device": "edge-3", "site": "edge-b"}),
		)
	}
	return document
}

func caseByCodeValue(document *Scenario, code string) *Case {
	for index := range document.Cases {
		if document.Cases[index].Code == code {
			return &document.Cases[index]
		}
	}
	return nil
}

func cloneScenario(input Scenario) Scenario {
	result := input
	result.Cases = make([]Case, len(input.Cases))
	for index, item := range input.Cases {
		result.Cases[index] = item
		result.Cases[index].GroupBy = append([]string(nil), item.GroupBy...)
		result.Cases[index].Labels = cloneStringMap(item.Labels)
		result.Cases[index].SuppressForNotifiers = append([]string(nil), item.SuppressForNotifiers...)
		result.Cases[index].TagForNotifiers = append([]string(nil), item.TagForNotifiers...)
		result.Cases[index].Assertions = append([]Assertion(nil), item.Assertions...)
		result.Cases[index].Events = make([]Event, len(item.Events))
		for eventIndex, event := range item.Events {
			result.Cases[index].Events[eventIndex] = event
			result.Cases[index].Events[eventIndex].Labels = cloneStringMap(event.Labels)
		}
	}
	return result
}
