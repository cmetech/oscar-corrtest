package scenario

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var semanticMutations = []struct {
	name    string
	pattern string
	mutate  func(*Scenario)
	want    string
}{
	{"flood positive below five", "flood", setP01Repeat(4), "P01 flood must reach five"},
	{"flood negative reaches five", "flood", setN01Repeat(5), "N01 flood must remain below five"},
	{"co occurrence missing P01 member", "co_occurrence", removeP01Role("disk_full"), "P01 co_occurrence"},
	{"sequence valid in N01", "sequence", orderN01("login_failure", "privileged_command"), "N01 sequence"},
	{"persistence resolves late in N01", "persistence", setN01ResolveDelay(30 * time.Second), "N01 persistence must resolve before 30s"},
	{"absence gap missing in P01", "absence", sustainP01Heartbeats(10 * time.Second), "P01 absence"},
	{"parent child parent missing in P01", "parent_child", removeP01Role("parent"), "P01 parent_child"},
	{"cross source P01 missing api", "cross_source", removeP01Source("api"), "P01 cross_source"},
	{"threshold N01 reaches three devices", "threshold", addN01Devices("edge-1", "edge-2", "edge-3"), "N01 threshold"},
}

func TestPatternSemanticValidation(t *testing.T) {
	for _, pattern := range SupportedPatterns() {
		t.Run(pattern+" valid", func(t *testing.T) {
			raw := encodeWireWithoutValidation(t, Builtin(pattern))
			if _, err := Decode(bytes.NewReader(raw)); err != nil {
				t.Fatalf("valid %s built-in rejected: %v", pattern, err)
			}
		})
	}
	for _, test := range semanticMutations {
		t.Run(test.name, func(t *testing.T) {
			requireSemanticDecodeError(t, test.pattern, test.want, test.mutate)
		})
	}
}

func TestPatternSemanticValidationRejectsConditionsOutsideCaseWindow(t *testing.T) {
	tests := []struct {
		pattern string
		mutate  func(*Case)
	}{
		{"flood", func(item *Case) {
			item.Role, item.Repeat = "", 0
			item.Events = []Event{
				{Role: "interface_down", Status: "firing"},
				{Role: "interface_down", Status: "firing", Delay: time.Second},
				{Role: "interface_down", Status: "firing", Delay: 2 * time.Second},
				{Role: "interface_down", Status: "firing", Delay: 3 * time.Second},
				{Role: "interface_down", Status: "firing", Delay: 31 * time.Second},
			}
		}},
		{"co_occurrence", func(item *Case) { item.Events[1].Delay = 31 * time.Second }},
		{"sequence", func(item *Case) { item.Events[1].Delay = 31 * time.Second }},
		{"parent_child", func(item *Case) { item.Events[1].Delay = 31 * time.Second }},
		{"cross_source", func(item *Case) { item.Events[1].Delay = 31 * time.Second }},
		{"threshold", func(item *Case) { item.Events[2].Delay = 31 * time.Second }},
	}
	for _, test := range tests {
		t.Run(test.pattern, func(t *testing.T) {
			document := Builtin(test.pattern)
			test.mutate(caseByCode(&document, "P01"))
			raw := encodeWireWithoutValidation(t, document)
			if _, err := Decode(bytes.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "P01 "+test.pattern) {
				t.Fatalf("error=%v, want P01 %s window failure", err, test.pattern)
			}
		})
	}
}

func requireSemanticDecodeError(t *testing.T, pattern, contains string, mutate func(*Scenario)) {
	t.Helper()
	document := Builtin(pattern)
	mutate(&document)
	raw := encodeWireWithoutValidation(t, document)
	_, err := Decode(bytes.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error=%v, want substring %q", err, contains)
	}
}

func encodeWireWithoutValidation(t *testing.T, document Scenario) []byte {
	t.Helper()
	wire := wireScenario{
		APIVersion: document.APIVersion, Kind: document.Kind, Name: document.Name,
		Suite: document.Suite, Pattern: document.Pattern, MaxDuration: document.MaxDuration.String(),
	}
	for _, item := range document.Cases {
		converted := wireCase{
			Name: item.Name, Code: item.Code, Polarity: item.Polarity, Window: item.Window.String(),
			GroupBy: append([]string(nil), item.GroupBy...), Labels: cloneStringMap(item.Labels),
			SuppressForNotifiers: append([]string(nil), item.SuppressForNotifiers...),
			TagForNotifiers:      append([]string(nil), item.TagForNotifiers...),
		}
		if item.Role != "" {
			role := item.Role
			converted.Role = &role
		}
		if item.Repeat != 0 {
			repeat := item.Repeat
			converted.Repeat = &repeat
		}
		for _, assertion := range item.Assertions {
			encoded := wireAssertion{Kind: assertion.Kind, Equals: assertion.Equals}
			if assertion.Outcome != "" {
				outcome := assertion.Outcome
				encoded.Outcome = &outcome
			}
			converted.Assertions = append(converted.Assertions, encoded)
		}
		for _, event := range item.Events {
			delay := ""
			if event.Delay != 0 {
				delay = event.Delay.String()
			}
			converted.Events = append(converted.Events, wireEvent{Role: event.Role, Status: event.Status, Labels: cloneStringMap(event.Labels), Delay: delay})
		}
		wire.Cases = append(wire.Cases, converted)
	}
	raw, err := yaml.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func setP01Repeat(repeat int) func(*Scenario) {
	return func(document *Scenario) { caseByCode(document, "P01").Repeat = repeat }
}

func setN01Repeat(repeat int) func(*Scenario) {
	return func(document *Scenario) { caseByCode(document, "N01").Repeat = repeat }
}

func removeP01Role(role string) func(*Scenario) {
	return func(document *Scenario) {
		item := caseByCode(document, "P01")
		events := item.Events[:0]
		for _, event := range item.Events {
			if event.Role != role {
				events = append(events, event)
			}
		}
		item.Events = events
	}
}

func orderN01(first, second string) func(*Scenario) {
	return func(document *Scenario) {
		item := caseByCode(document, "N01")
		item.Events = []Event{{Role: first, Status: "firing"}, {Role: second, Status: "firing", Delay: time.Second}}
	}
}

func setN01ResolveDelay(delay time.Duration) func(*Scenario) {
	return func(document *Scenario) {
		item := caseByCode(document, "N01")
		item.Events[1].Delay = delay
	}
}

func sustainP01Heartbeats(interval time.Duration) func(*Scenario) {
	return func(document *Scenario) {
		item := caseByCode(document, "P01")
		item.Events = nil
		for at := time.Duration(0); at <= 50*time.Second; at += interval {
			item.Events = append(item.Events, Event{Role: "heartbeat", Status: "firing", Delay: at})
		}
	}
}

func removeP01Source(source string) func(*Scenario) {
	return func(document *Scenario) {
		item := caseByCode(document, "P01")
		events := item.Events[:0]
		for _, event := range item.Events {
			if event.Labels["oscar_source"] != source {
				events = append(events, event)
			}
		}
		item.Events = events
	}
}

func addN01Devices(devices ...string) func(*Scenario) {
	return func(document *Scenario) {
		item := caseByCode(document, "N01")
		item.Events = nil
		for index, device := range devices {
			item.Events = append(item.Events, Event{Role: "cpu_high", Status: "firing", Labels: map[string]string{"device": device}, Delay: time.Duration(index) * time.Second})
		}
	}
}

func caseByCode(document *Scenario, code string) *Case {
	for index := range document.Cases {
		if document.Cases[index].Code == code {
			return &document.Cases[index]
		}
	}
	panic("missing case " + code)
}
