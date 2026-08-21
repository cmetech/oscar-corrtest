package authoring

import (
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestCatalogIsCompleteAndInternallyLinked(t *testing.T) {
	catalog := DefaultCatalog()
	if got := len(catalog.Sections); got != 5 {
		t.Fatalf("sections=%d", got)
	}
	if got := len(catalog.Lessons); got != 5 {
		t.Fatalf("lessons=%d", got)
	}
	if got := len(catalog.Patterns); got != 8 {
		t.Fatalf("patterns=%d", got)
	}
	if got := len(catalog.Views); got != 4 {
		t.Fatalf("views=%d", got)
	}
	if err := catalog.Validate(scenario.PublicContract(), scenario.AllExamples()); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogValidateRejectsBrokenLinksAndNarrative(t *testing.T) {
	base := DefaultCatalog()
	tests := []struct {
		name   string
		mutate func(*Catalog, *[]scenario.ExampleDefinition)
		want   string
	}{
		{
			name:   "unknown lesson field",
			mutate: func(c *Catalog, _ *[]scenario.ExampleDefinition) { c.Lessons[0].FieldIDs = []string{"unknown.field"} },
			want:   "unknown field",
		},
		{
			name:   "unknown pattern",
			mutate: func(c *Catalog, _ *[]scenario.ExampleDefinition) { c.Patterns[0].ID = "unknown" },
			want:   "unknown pattern",
		},
		{
			name: "missing advanced example",
			mutate: func(_ *Catalog, examples *[]scenario.ExampleDefinition) {
				filtered := (*examples)[:0]
				for _, example := range *examples {
					if example.ID != "flood:advanced" {
						filtered = append(filtered, example)
					}
				}
				*examples = filtered
			},
			want: "missing advanced example",
		},
		{
			name:   "empty fragment",
			mutate: func(c *Catalog, _ *[]scenario.ExampleDefinition) { c.Lessons[0].Fragment = "" },
			want:   "empty fragment",
		},
		{
			name:   "duplicate section",
			mutate: func(c *Catalog, _ *[]scenario.ExampleDefinition) { c.Sections[1].ID = c.Sections[0].ID },
			want:   "duplicate section",
		},
		{
			name:   "duplicate lesson",
			mutate: func(c *Catalog, _ *[]scenario.ExampleDefinition) { c.Lessons[1].ID = c.Lessons[0].ID },
			want:   "duplicate lesson",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := base
			catalog.Sections = append([]Section(nil), base.Sections...)
			catalog.Lessons = append([]Lesson(nil), base.Lessons...)
			catalog.Patterns = append([]PatternGuide(nil), base.Patterns...)
			examples := scenario.AllExamples()
			test.mutate(&catalog, &examples)
			err := catalog.Validate(scenario.PublicContract(), examples)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want %q", err, test.want)
			}
		})
	}
}
