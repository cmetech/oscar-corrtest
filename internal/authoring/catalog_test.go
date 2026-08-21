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

func TestCatalogValidateRequiresExactStableCatalogSets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Catalog)
	}{
		{"missing section", func(c *Catalog) { c.Sections = c.Sections[1:] }},
		{"extra section", func(c *Catalog) {
			c.Sections = append(c.Sections, Section{ID: "extra", Title: "Extra", Summary: "Extra"})
		}},
		{"unknown section", func(c *Catalog) { c.Sections[0].ID = "unknown" }},
		{"missing lesson", func(c *Catalog) { c.Lessons = c.Lessons[1:] }},
		{"extra lesson", func(c *Catalog) {
			c.Lessons = append(c.Lessons, Lesson{ID: "extra", Title: "Extra", Concept: "Concept", Effect: "Effect", Fragment: "# fragment", CommonMistake: "Mistake"})
		}},
		{"unknown lesson", func(c *Catalog) { c.Lessons[0].ID = "unknown" }},
		{"missing view", func(c *Catalog) { c.Views = c.Views[1:] }},
		{"extra view", func(c *Catalog) { c.Views = append(c.Views, View{ID: "extra", Title: "Extra", Summary: "Extra"}) }},
		{"unknown view", func(c *Catalog) { c.Views[0].ID = "unknown" }},
		{"missing pattern guide", func(c *Catalog) { c.Patterns = c.Patterns[1:] }},
		{"extra pattern guide", func(c *Catalog) {
			c.Patterns = append(c.Patterns, PatternGuide{ID: "extra", Title: "Extra", Behavior: "Behavior", ExpectedEvidence: "Evidence"})
		}},
		{"empty lesson concept", func(c *Catalog) { c.Lessons[0].Concept = "" }},
		{"empty section summary", func(c *Catalog) { c.Sections[0].Summary = "" }},
		{"empty view summary", func(c *Catalog) { c.Views[0].Summary = "" }},
		{"empty assertion note", func(c *Catalog) { c.AssertionNotes[0].Content = "" }},
		{"empty validation note", func(c *Catalog) { c.ValidationNotes[0].Content = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := cloneCatalog(DefaultCatalog())
			test.mutate(&catalog)
			if err := catalog.Validate(scenario.PublicContract(), scenario.AllExamples()); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}

func TestCatalogValidateRequiresExactExampleRegistry(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*[]scenario.ExampleDefinition)
	}{
		{"missing example", func(examples *[]scenario.ExampleDefinition) { *examples = (*examples)[1:] }},
		{"extra unknown example", func(examples *[]scenario.ExampleDefinition) {
			*examples = append(*examples, scenario.ExampleDefinition{ID: "unknown:basic", Pattern: "unknown", Level: "basic", Scenario: scenario.Builtin("flood")})
		}},
		{"duplicate ID", func(examples *[]scenario.ExampleDefinition) { *examples = append(*examples, (*examples)[0]) }},
		{"duplicate slot", func(examples *[]scenario.ExampleDefinition) {
			duplicate := (*examples)[0]
			duplicate.ID = "another-id"
			*examples = append(*examples, duplicate)
		}},
		{"malformed ID", func(examples *[]scenario.ExampleDefinition) { (*examples)[0].ID = "wrong" }},
		{"unknown level", func(examples *[]scenario.ExampleDefinition) { (*examples)[0].Level = "expert" }},
		{"unknown pattern", func(examples *[]scenario.ExampleDefinition) { (*examples)[0].Pattern = "unknown" }},
		{"scenario pattern mismatch", func(examples *[]scenario.ExampleDefinition) { (*examples)[0].Scenario.Pattern = "threshold" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			examples := scenario.AllExamples()
			test.mutate(&examples)
			if err := DefaultCatalog().Validate(scenario.PublicContract(), examples); err == nil {
				t.Fatal("invalid example registry accepted")
			}
		})
	}
}

func TestCatalogValidateIdentifiesDuplicateExampleSlots(t *testing.T) {
	examples := scenario.AllExamples()
	duplicate := examples[0]
	duplicate.ID = "another-id"
	examples = append(examples, duplicate)
	err := DefaultCatalog().Validate(scenario.PublicContract(), examples)
	if err == nil || !strings.Contains(err.Error(), "duplicate example slot") {
		t.Fatalf("err=%v", err)
	}
}

func cloneCatalog(input Catalog) Catalog {
	result := input
	result.Sections = append([]Section(nil), input.Sections...)
	result.Lessons = append([]Lesson(nil), input.Lessons...)
	result.Patterns = append([]PatternGuide(nil), input.Patterns...)
	result.Views = append([]View(nil), input.Views...)
	result.AssertionNotes = append([]Note(nil), input.AssertionNotes...)
	result.ValidationNotes = append([]Note(nil), input.ValidationNotes...)
	return result
}
