package scenario_test

import (
	"bytes"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"os"
	"strings"
	"testing"
)

func TestSourceEpisodeAssertionWireRoundTripAndBounds(t *testing.T) {
	raw, err := os.ReadFile("../../scenarios/reliability/flood-lifecycle-downstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document, err := scenario.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := scenario.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	again, err := scenario.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Cases[0].Checkpoints) != 5 || again.Cases[0].Checkpoints[2].Assertions[0].Kind != "source-episode-state-count" {
		t.Fatal("lost episode assertions")
	}
	for name, fragment := range map[string]string{"missing outcome": "kind: source-episode-state-count, equals: 2", "unsupported state": "kind: source-episode-state-count, outcome: physically_cleared, equals: 2", "implicit zero": "kind: source-episode-state-count, outcome: resolved"} {
		t.Run(name, func(t *testing.T) {
			input := strings.Replace(string(raw), "kind: source-episode-state-count, outcome: resolved, equals: 2", fragment, 1)
			if _, err := scenario.Decode(strings.NewReader(input)); err == nil {
				t.Fatal("invalid assertion accepted")
			}
		})
	}
}
