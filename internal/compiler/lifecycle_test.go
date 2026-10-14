package compiler_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

const lifecycleScenario = `apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: named-flood-lifecycle
suite: reliability
pattern: flood
inputPath: alert-engine
maxDuration: 3m
cases:
- name: five-distinct
  code: P01
  polarity: positive
  window: 60s
  groupBy: [site]
  labels: {site: lab}
  events:
  - {role: interface_down, source: A, status: firing}
  - {role: interface_down, source: B, status: firing, delay: 1s}
  - {role: interface_down, source: C, status: firing, delay: 2s}
  - {role: interface_down, source: D, status: firing, delay: 3s}
  - {role: interface_down, source: E, status: firing, delay: 4s}
  - {role: interface_down, source: A, status: resolved, delay: 5s}
  - {role: interface_down, source: B, status: resolved, delay: 6s}
  - {role: interface_down, source: A, status: firing, delay: 7s}
  - {role: interface_down, source: A, status: resolved, delay: 8s}
  assertions: [{kind: audit-count, outcome: parent_emitted, equals: 1}]
- name: four-distinct
  code: N01
  polarity: negative
  role: interface_down
  repeat: 4
  window: 60s
  assertions: [{kind: audit-count, outcome: parent_emitted, equals: 0}]
`

func TestNamedFloodSourcesRecoverAndRefireTheirOwnIdentity(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(lifecycleScenario))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_lifecycle", ShortToken: "LIFE1234"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	alerts := plan.Cases[0].Alerts
	raw, _ := json.Marshal(alerts[0])
	if !strings.Contains(string(raw), `"source":"A"`) {
		t.Fatal("compiled evidence lost named source")
	}
	for _, pair := range [][2]int{{0, 5}, {1, 6}, {0, 7}, {0, 8}} {
		if !reflect.DeepEqual(alerts[pair[0]].Labels, alerts[pair[1]].Labels) {
			t.Fatalf("identity changed between %d and %d", pair[0], pair[1])
		}
	}
	if reflect.DeepEqual(alerts[0].Labels, alerts[1].Labels) {
		t.Fatal("distinct children share identity")
	}
	encoded, err := scenario.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "source: A") || !strings.Contains(string(encoded), "inputPath: alert-engine") {
		t.Fatal("round trip lost lifecycle contract")
	}
}

func TestReceiverAdmissionCannotCompileAsDownstreamIngress(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(strings.Replace(lifecycleScenario, "inputPath: alert-engine", "inputPath: receiver-admission", 1)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiler.Compile(domain.Run{ID: "crt_lifecycle", ShortToken: "LIFE1234"}, doc, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err == nil || !strings.Contains(err.Error(), "receiver-admission") {
		t.Fatalf("receiver path was not rejected: %v", err)
	}
}

func TestNamedSourceRejectsAmbiguousLifecycle(t *testing.T) {
	for name, change := range map[string][2]string{
		"unknown clear":         {"source: A, status: resolved, delay: 5s", "source: Z, status: resolved, delay: 5s"},
		"active refire":         {"source: B, status: firing, delay: 1s", "source: A, status: firing, delay: 1s"},
		"changed refire labels": {"source: A, status: firing, delay: 7s", "source: A, status: firing, labels: {site: changed}, delay: 7s"},
		"empty source":          {"source: A, status: firing}", "source: '', status: firing}"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scenario.Decode(strings.NewReader(strings.Replace(lifecycleScenario, change[0], change[1], 1))); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestNamedSourceEpisodeOffsetsAreStableUntilRefire(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(lifecycleScenario))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_lifecycle", ShortToken: "LIFE1234"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	alerts := plan.Cases[0].Alerts
	for index, want := range map[int]int64{0: 0, 1: 1000000000, 5: 0, 6: 1000000000, 7: 7000000000, 8: 7000000000} {
		if alerts[index].EpisodeOffset == nil || int64(*alerts[index].EpisodeOffset) != want {
			t.Fatalf("wrong episode for event %d: %v", index, alerts[index].EpisodeOffset)
		}
	}
}

func TestNamedRefireCannotReuseFiringEpisodeTime(t *testing.T) {
	doc := scenario.Builtin("flood")
	item := &doc.Cases[0]
	item.Role = ""
	item.Repeat = 0
	for _, source := range []string{"A", "B", "C", "D", "E"} {
		item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: source, Status: "firing"})
	}
	item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: "A", Status: "resolved"}, scenario.Event{Role: "interface_down", Source: "A", Status: "firing"})
	if err := scenario.Validate(doc); err == nil {
		t.Fatal("refire reused previous startsAt")
	}
}

func TestFloodRefireDoesNotInventFifthDistinctSource(t *testing.T) {
	doc := scenario.Builtin("flood")
	item := &doc.Cases[0]
	item.Role = ""
	item.Repeat = 0
	for _, source := range []string{"A", "B", "C", "D"} {
		item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: source, Status: "firing"})
	}
	item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: "A", Status: "resolved", Delay: time.Second}, scenario.Event{Role: "interface_down", Source: "A", Status: "firing", Delay: 2 * time.Second})
	if err := scenario.Validate(doc); err == nil {
		t.Fatal("refire counted as fifth distinct fingerprint")
	}
}

func TestCommittedFloodLifecycleFixtureCompiles(t *testing.T) {
	raw, err := os.ReadFile("../../scenarios/reliability/flood-lifecycle-downstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_lifecycle", ShortToken: "LIFE1234"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.MutationBudget.Alerts != 13 || plan.InputPath != "alert-engine" {
		t.Fatalf("wrong fixture plan: %+v", plan)
	}
}

func TestNamedSourceRequiresMillisecondOffsets(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(lifecycleScenario))
	if err != nil {
		t.Fatal(err)
	}
	doc.Cases[0].Events[0].Delay = time.Nanosecond
	if err := scenario.Validate(doc); err == nil {
		t.Fatal("sub-millisecond firing offset accepted")
	}
}

func TestNamedSourceAcceptsDistinctMillisecondRefire(t *testing.T) {
	doc := scenario.Builtin("flood")
	item := &doc.Cases[0]
	item.Role = ""
	item.Repeat = 0
	for _, source := range []string{"A", "B", "C", "D", "E"} {
		item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: source, Status: "firing"})
	}
	item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: "A", Status: "resolved"}, scenario.Event{Role: "interface_down", Source: "A", Status: "firing", Delay: time.Millisecond})
	plan, err := compiler.Compile(domain.Run{ID: "crt_lifecycle", ShortToken: "LIFE1234"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	if got := *plan.Cases[0].Alerts[6].EpisodeOffset; got != time.Millisecond {
		t.Fatalf("refire offset=%s", got)
	}
}
