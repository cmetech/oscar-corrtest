package scenario

import "time"

var builtinOrder = []string{"co_occurrence", "flood", "sequence", "persistence", "absence", "parent_child", "cross_source", "threshold"}

// AllBuiltins returns fresh positive/negative scenario definitions in stable order.
func AllBuiltins() []Scenario {
	result := make([]Scenario, 0, len(builtinOrder))
	for _, pattern := range builtinOrder {
		result = append(result, Builtin(pattern))
	}
	return result
}

// Builtin returns a fresh built-in scenario.
func Builtin(pattern string) Scenario {
	base := Scenario{APIVersion: "corrtest.oscar/v1alpha1", Kind: "CorrelationScenario", Name: pattern + "-basic",
		Suite: "builtin-all", Pattern: pattern, MaxDuration: 90 * time.Second}
	positive := func(name string, events ...Event) Case {
		return Case{Name: name, Code: "P01", Polarity: "positive", Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-p01"}, Events: events,
			Assertions: []Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 1}, {Kind: "synthetic-alert-count", Equals: 1}}}
	}
	negative := func(name string, events ...Event) Case {
		return Case{Name: name, Code: "N01", Polarity: "negative", Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-n01"}, Events: events,
			Assertions: []Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 0}, {Kind: "synthetic-alert-count", Equals: 0}}}
	}
	event := func(role string) Event { return Event{Role: role, Status: "firing"} }
	switch pattern {
	case "flood":
		base.Name = "flood-basic"
		base.Cases = []Case{
			{Name: "emits-one-parent-at-threshold", Code: "P01", Polarity: "positive", Role: "interface_down", Repeat: 5, Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-p01"}, Assertions: positive("").Assertions},
			{Name: "does-not-fire-below-threshold", Code: "N01", Polarity: "negative", Role: "interface_down", Repeat: 4, Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-n01"}, Assertions: negative("").Assertions},
		}
	case "co_occurrence":
		base.Cases = []Case{positive("emits-when-required-types-co-occur", event("disk_full"), Event{Role: "cpu_high", Status: "firing", Delay: time.Second}), negative("does-not-emit-when-type-missing", event("disk_full"))}
	case "sequence":
		base.Cases = []Case{positive("emits-for-required-order", event("login_failure"), Event{Role: "privileged_command", Status: "firing", Delay: time.Second}), negative("does-not-emit-for-invalid-order", event("privileged_command"), Event{Role: "login_failure", Status: "firing", Delay: time.Second})}
	case "cross_source":
		base.Cases = []Case{positive("emits-for-required-source-mix", Event{Role: "interface_down", Status: "firing", Labels: map[string]string{"oscar_source": "snmp"}}, Event{Role: "interface_down", Status: "firing", Labels: map[string]string{"oscar_source": "api"}, Delay: time.Second}), negative("does-not-emit-for-invalid-source-mix", Event{Role: "interface_down", Status: "firing", Labels: map[string]string{"oscar_source": "snmp"}}, Event{Role: "interface_down", Status: "firing", Labels: map[string]string{"oscar_source": "snmp"}, Delay: time.Second})}
	case "threshold":
		base.Cases = []Case{positive("emits-at-distinct-threshold", Event{Role: "cpu_high", Status: "firing", Labels: map[string]string{"device": "d1"}}, Event{Role: "cpu_high", Status: "firing", Labels: map[string]string{"device": "d2"}, Delay: time.Second}, Event{Role: "cpu_high", Status: "firing", Labels: map[string]string{"device": "d3"}, Delay: 2 * time.Second}), negative("does-not-emit-for-repeated-value", Event{Role: "cpu_high", Status: "firing", Labels: map[string]string{"device": "d1"}}, Event{Role: "cpu_high", Status: "firing", Labels: map[string]string{"device": "d1"}, Delay: time.Second})}
	case "persistence":
		base.Cases = []Case{positive("emits-after-unresolved-duration", event("service_down")), negative("resolution-cancels-persistence", event("service_down"), Event{Role: "service_down", Status: "resolved", Delay: 10 * time.Second})}
	case "absence":
		base.Cases = []Case{positive("emits-when-heartbeat-absent", event("heartbeat")), negative("continued-heartbeat-prevents-parent", event("heartbeat"), Event{Role: "heartbeat", Status: "firing", Delay: 15 * time.Second})}
	case "parent_child":
		base.Cases = []Case{
			{Name: "links-child-to-active-parent", Code: "P01", Polarity: "positive", Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-p01"}, SuppressForNotifiers: []string{"email"}, Events: []Event{event("parent"), {Role: "child", Status: "firing", Delay: time.Second}}, Assertions: []Assertion{{Kind: "parent-link-count", Outcome: "suppressed_per_notifier", Equals: 1}}},
			{Name: "releases-child-without-parent", Code: "N01", Polarity: "negative", Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-n01"}, SuppressForNotifiers: []string{"email"}, Events: []Event{event("child")}, Assertions: []Assertion{{Kind: "audit-count", Outcome: "released_no_trigger", Equals: 1}}},
		}
	default:
		return Scenario{APIVersion: base.APIVersion, Kind: base.Kind, Pattern: pattern}
	}
	return base
}
