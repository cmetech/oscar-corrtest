package scenario_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDistributedJSONSchemaIsValidAndCoversEveryPattern(t *testing.T) {
	data, err := os.ReadFile("../../docs/schema/correlation-scenario.schema.json")
	if err != nil || !json.Valid(data) {
		t.Fatalf("schema invalid: %v", err)
	}
	for _, pattern := range []string{"flood", "co_occurrence", "sequence", "persistence", "absence", "parent_child", "cross_source", "threshold"} {
		if !strings.Contains(string(data), `"`+pattern+`"`) {
			t.Errorf("schema missing %s", pattern)
		}
	}
}
