package compiler_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// matchedNames collects every alertname a rule's match criteria refers to.
func matchedNames(t *testing.T, criteria map[string]any) []string {
	t.Helper()
	raw, err := json.Marshal(criteria)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(any) []string
	walk = func(v any) []string {
		var out []string
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if s, ok := val.(string); ok && k == "alertname" {
					out = append(out, s)
					continue
				}
				out = append(out, walk(val)...)
			}
		case []any:
			for _, val := range x {
				out = append(out, walk(val)...)
			}
		}
		return out
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return walk(v)
}

// TestParallelRunsAreIsolated is why alerting-day@v4 may run correlation
// documents concurrently: two runs of the same document share no alert,
// rule or parent name, and every rule matches only names carrying its own
// run's token (a negative case may name a role it deliberately never sends),
// so a parallel run's alerts can neither satisfy nor poison its expected
// parent. Each run's parents and sources carry their own run id.
func TestParallelRunsAreIsolated(t *testing.T) {
	a := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	b := domain.Run{ID: "crt_ZYXWVTSRQPNMKJHGFEDCBA9876", ShortToken: "3H8P5W2B"}
	docs := scenario.AllBuiltins()
	for _, e := range scenario.AllExamples() {
		docs = append(docs, e.Scenario)
	}
	for _, doc := range docs {
		pa, err := compiler.Compile(a, doc, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil {
			t.Fatalf("%s: %v", doc.Name, err)
		}
		pb, err := compiler.Compile(b, doc, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil {
			t.Fatalf("%s: %v", doc.Name, err)
		}
		names := func(p compiler.Plan) map[string]bool {
			out := map[string]bool{}
			for _, c := range p.Cases {
				out[c.Rule.Name] = true
				if c.Rule.EmitAlertName != "" {
					out[c.Rule.EmitAlertName] = true
				}
				for _, al := range c.Alerts {
					out[al.Name] = true
				}
			}
			return out
		}
		na, nb := names(pa), names(pb)
		for n := range na {
			if nb[n] {
				t.Errorf("%s: name %s is shared by two parallel runs", doc.Name, n)
			}
		}
		for _, p := range []struct {
			plan  compiler.Plan
			own   map[string]bool
			other map[string]bool
			run   domain.Run
		}{{pa, na, nb, a}, {pb, nb, na, b}} {
			for _, c := range p.plan.Cases {
				matched := matchedNames(t, c.Rule.MatchCriteria)
				if len(matched) == 0 {
					t.Errorf("%s/%s: rule %s matches no alertname, so it could match a parallel run", doc.Name, c.Code, c.Rule.Name)
				}
				for _, m := range matched {
					if p.other[m] || !strings.HasSuffix(m, "_"+strings.ToUpper(p.run.ShortToken)) {
						t.Errorf("%s/%s: rule %s matches %s, not a name of its own run", doc.Name, c.Code, c.Rule.Name, m)
					}
				}
				if c.Rule.EmitAlertName != "" && c.Rule.EmitLabels["oscar_test_run_id"] != p.run.ID {
					t.Errorf("%s/%s: parent labels %v lack run %s", doc.Name, c.Code, c.Rule.EmitLabels, p.run.ID)
				}
				for _, al := range c.Alerts {
					if al.Labels["oscar_test_run_id"] != p.run.ID {
						t.Errorf("%s/%s: alert %s lacks run %s", doc.Name, c.Code, al.Name, p.run.ID)
					}
				}
			}
		}
	}
}
