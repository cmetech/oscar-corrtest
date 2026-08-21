// Package authoring composes the public scenario language into target-free
// teaching material and deterministic preview data.
package authoring

import (
	"fmt"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// Section is a stable top-level authoring workspace section.
type Section struct {
	ID      string
	Title   string
	Summary string
}

// View is one server-rendered representation of an executable example.
type View struct {
	ID      string
	Title   string
	Summary string
}

// Note is concise narrative guidance for assertions or validation.
type Note struct {
	ID      string
	Title   string
	Content string
}

// Lesson teaches one increment of the quickstart scenario.
type Lesson struct {
	ID, Title, Concept, Effect, Fragment, CommonMistake string
	FieldIDs                                            []string
}

// PatternGuide adds compiler and evidence context to a supported pattern.
type PatternGuide struct {
	ID, Title, Behavior, ExpectedEvidence  string
	FixedSemantics, Configurable, Mistakes []string
}

// Catalog is the stable narrative layer over public scenario contracts.
type Catalog struct {
	Sections                        []Section
	Lessons                         []Lesson
	Patterns                        []PatternGuide
	AssertionNotes, ValidationNotes []Note
	Views                           []View
}

// DefaultCatalog returns the complete, immutable-by-convention authoring guide.
func DefaultCatalog() Catalog {
	return Catalog{
		Sections: []Section{
			{ID: "quickstart", Title: "Quickstart", Summary: "Build one valid flood scenario in five steps."},
			{ID: "schema", Title: "Schema", Summary: "Closed public YAML fields, limits, and compiler effects."},
			{ID: "patterns", Title: "Patterns", Summary: "Executable P01 and N01 cookbook examples."},
			{ID: "assertions", Title: "Assertions", Summary: "Exact evidence counts and outcome proof."},
			{ID: "validation", Title: "Validation", Summary: "Strict syntax, semantic, and safe-budget checks."},
		},
		Views: []View{
			{ID: "yaml", Title: "YAML", Summary: "Canonical executable scenario source."},
			{ID: "contract", Title: "Compiled contract", Summary: "P01 and N01 compiler output."},
			{ID: "api", Title: "OSCAR API", Summary: "Credential-free request preview."},
			{ID: "lifecycle", Title: "Lifecycle", Summary: "Preflight, mutation, evidence, and cleanup order."},
		},
		Lessons: []Lesson{
			{ID: "identity", Title: "Document identity", Concept: "Start with the closed CorrTest document identity.", Effect: "It selects the wire contract, ownership names, fixed compiler pattern, and safe overall budget.", Fragment: "apiVersion: corrtest.oscar/v1alpha1\nkind: CorrelationScenario\nname: checkout-flood\nsuite: operator-custom\npattern: flood\nmaxDuration: 90s", CommonMistake: "Changing apiVersion or kind, or using an unsupported pattern, is rejected rather than inferred.", FieldIDs: []string{"scenario.apiVersion", "scenario.kind", "scenario.name", "scenario.suite", "scenario.pattern", "scenario.maxDuration"}},
			{ID: "cases", Title: "P01 and N01", Concept: "Every scenario has exactly one positive proof and one nearby negative control.", Effect: "The compiler produces one temporary rule and evidence contract for P01 and one for N01.", Fragment: "cases:\n  - name: emits-parent\n    code: P01\n    polarity: positive\n    window: 30s\n  - name: does-not-emit\n    code: N01\n    polarity: negative\n    window: 30s", CommonMistake: "Two P01 cases, a missing N01, or a code/polarity mismatch is invalid.", FieldIDs: []string{"scenario.cases", "case.name", "case.code", "case.polarity", "case.window", "case.groupBy", "case.labels"}},
			{ID: "stimuli", Title: "Stimuli", Concept: "Use either role plus repeat or explicit events; never both forms in one case.", Effect: "Stimuli become deterministic source-alert injections with stable ownership labels and delays.", Fragment: "role: interface_down\nrepeat: 5\n# Or use explicit events:\n# events:\n#   - role: interface_down\n#     status: firing\n#     delay: 1s", CommonMistake: "Mixing repeat with events, or resolving an identity that did not first fire, is rejected.", FieldIDs: []string{"case.role", "case.repeat", "case.events", "event.role", "event.status", "event.labels", "event.delay"}},
			{ID: "assertions", Title: "Assertions", Concept: "Declare exact evidence counts for each P01 and N01 case.", Effect: "The runner reads history and audit evidence across the complete observation window before deciding the case.", Fragment: "assertions:\n  - kind: audit-count\n    outcome: parent_emitted\n    equals: 1\n  - kind: synthetic-alert-count\n    equals: 1", CommonMistake: "Treating an early empty result as proof for equals: 0 produces an invalid conclusion.", FieldIDs: []string{"case.assertions", "assertion.kind", "assertion.outcome", "assertion.equals"}},
			{ID: "validate", Title: "Validate and run", Concept: "Strict validation and target-free inspection happen before any live run.", Effect: "Inspection shows the compiled contract and OSCAR lifecycle without a target, credential, persistence, or network contact.", Fragment: "# Validate strict YAML, inspect P01/N01, then select Phase A or Phase B for a live run.", CommonMistake: "Phase A is audit-only; it cannot prove a synthetic-parent assertion that requires Phase B dispatch.", FieldIDs: []string{"scenario.maxDuration", "case.window", "case.assertions"}},
		},
		Patterns: []PatternGuide{
			patternGuide("flood", "Flood", "Counts repeated source occurrences in one grouping window.", []string{"min_count=5", "occurrences require distinct fingerprints"}, []string{"case.groupBy", "case.labels", "case.role", "case.repeat", "case.events"}, "P01 proves parent_emitted and one synthetic parent; N01 proves no group reaches five.", "False positive: reuse one fingerprint and mistake deduplication for five occurrences.", "False negative: split the five distinct occurrences across grouping keys."),
			patternGuide("co_occurrence", "Co-occurrence", "Requires every compiled logical alert role in one grouping window.", []string{"all compiled required alert names must occur in one grouping window"}, []string{"case.groupBy", "case.labels", "case.events"}, "P01 observes parent_emitted and a synthetic parent; N01 omits a required member.", "False positive: treat roles from different grouping keys as one match.", "False negative: omit a required role from P01 or its shared group."),
			patternGuide("sequence", "Sequence", "Recognizes login_failure followed by privileged_command in one group.", []string{"login_failure then privileged_command"}, []string{"case.groupBy", "case.labels", "case.events", "event.delay"}, "P01 observes parent_emitted and a synthetic parent; N01 reverses or separates the ordered pair.", "False positive: accept a reversed pair as ordered evidence.", "False negative: put the required pair in different groups."),
			patternGuide("persistence", "Persistence", "Tests whether one matching alert remains unresolved.", []string{"one matching alert unresolved for 30 seconds"}, []string{"case.groupBy", "case.labels", "case.events", "event.delay"}, "P01 observes parent_emitted after the persistence timer; N01 resolves the same identity before 30 seconds.", "False positive: treat a resolved identity as still active.", "False negative: resolve after, rather than before, the 30-second boundary."),
			patternGuide("absence", "Absence", "Detects a completed heartbeat gap.", []string{"expected every 10 seconds", "absent for 30 seconds", "55-second observation"}, []string{"case.groupBy", "case.labels", "case.events", "event.delay"}, "P01 observes parent_emitted after a completed gap; N01 sustains heartbeats so no gap completes.", "False positive: decide before the full observation window closes.", "False negative: leave a 30-second heartbeat gap in the control."),
			patternGuide("parent_child", "Parent-child", "Links a child to an earlier active parent and applies notifier policy.", []string{"roles parent and child", "no synthetic emit rule"}, []string{"case.groupBy", "case.labels", "case.events", "case.suppressForNotifiers", "case.tagForNotifiers"}, "P01 observes suppressed_per_notifier parent-link evidence; N01 observes released_no_trigger for an unmatched child.", "False positive: expect a synthetic parent even though this pattern has no emit rule.", "False negative: put the active parent in another group or after the child."),
			patternGuide("cross_source", "Cross-source", "Requires one semantic source alert from both source systems.", []string{"required sources snmp and api for one semantic alert"}, []string{"case.groupBy", "case.labels", "case.events", "event.labels"}, "P01 observes parent_emitted when snmp and api share a group; N01 keeps required sources split.", "False positive: combine source values from separate grouping keys.", "False negative: fail to set both snmp and api source values in P01."),
			patternGuide("threshold", "Threshold", "Counts distinct device label values in a grouping key.", []string{"distinct label device", "minimum distinct count 3"}, []string{"case.groupBy", "case.labels", "case.events", "event.labels"}, "P01 observes parent_emitted at three devices; N01 splits values so no group reaches three.", "False positive: count repeated device values as distinct.", "False negative: spread the three device values across groups."),
		},
		AssertionNotes: []Note{
			{ID: "synthetic-alert-count", Title: "Synthetic alert count", Content: "Counts matching synthetic parents read from history; outcome is omitted."},
			{ID: "audit-count", Title: "Audit count", Content: "Counts correlation audit rows with the required exact outcome."},
			{ID: "parent-link-count", Title: "Parent-link count", Content: "Counts parent-child audit rows with the required exact outcome."},
			{ID: "zero-count-final-window", Title: "Exact zero needs the final window", Content: "An equals: 0 assertion requires a mandatory final snapshot at or after the absolute case deadline. Absence alone is not proof."},
			{ID: "phase-limitations", Title: "Phase A and Phase B", Content: "Phase B dispatch is required for synthetic-parent assertions. Phase A is audit-only and cannot prove dispatched synthetic parents."},
			{ID: "parent_emitted", Title: "parent_emitted", Content: "OSCAR emitted the expected synthetic parent."},
			{ID: "suppressed_per_notifier", Title: "suppressed_per_notifier", Content: "A parent-child policy suppressed the child for a notifier."},
			{ID: "released_no_trigger", Title: "released_no_trigger", Content: "A child had no active matching parent and was released without a trigger."},
		},
		ValidationNotes: []Note{
			{ID: "strict-yaml", Title: "Strict YAML", Content: "Unknown fields, duplicate keys, aliases, multiple documents, and inputs over 1 MiB are rejected."},
			{ID: "shape", Title: "Document shape", Content: "Use exactly P01 positive and N01 negative cases with exclusive repeat or event stimulus forms."},
			{ID: "budgets", Title: "Safe budgets", Content: "Durations, event counts, labels, assertions, grouping labels, and notifier names are bounded."},
			{ID: "pattern-aware", Title: "Pattern-aware semantics", Content: "Structural YAML still fails when its P01 or N01 cannot exercise the selected pattern contract."},
		},
	}
}

func patternGuide(id, title, behavior string, fixed, configurable []string, evidence, falsePositive, falseNegative string) PatternGuide {
	return PatternGuide{ID: id, Title: title, Behavior: behavior, FixedSemantics: fixed, Configurable: configurable, ExpectedEvidence: evidence, Mistakes: []string{falsePositive, falseNegative}}
}

// Validate verifies the exact stable authoring registry, narrative references,
// and executable example slots against the public scenario contract.
func (c Catalog) Validate(contract scenario.Contract, examples []scenario.ExampleDefinition) error {
	fields := make(map[string]struct{}, len(contract.Fields))
	for _, field := range contract.Fields {
		fields[field.ID] = struct{}{}
	}
	patternIDs := scenario.SupportedPatterns()
	patterns := make(map[string]struct{}, len(patternIDs))
	for _, patternID := range patternIDs {
		patterns[patternID] = struct{}{}
	}
	if err := validateSections(c.Sections); err != nil {
		return err
	}
	if err := validateLessons(c.Lessons, fields); err != nil {
		return err
	}
	seenPatterns := make(map[string]struct{}, len(c.Patterns))
	for _, guide := range c.Patterns {
		if _, found := patterns[guide.ID]; !found {
			return fmt.Errorf("unknown pattern %q", guide.ID)
		}
		if _, duplicate := seenPatterns[guide.ID]; duplicate {
			return fmt.Errorf("duplicate pattern %q", guide.ID)
		}
		seenPatterns[guide.ID] = struct{}{}
		if strings.TrimSpace(guide.Title) == "" || strings.TrimSpace(guide.Behavior) == "" || strings.TrimSpace(guide.ExpectedEvidence) == "" {
			return fmt.Errorf("pattern %q has empty narrative", guide.ID)
		}
		if err := validateNarrativeList("pattern "+guide.ID+" fixed semantics", guide.FixedSemantics); err != nil {
			return err
		}
		if err := validateNarrativeList("pattern "+guide.ID+" configurable inputs", guide.Configurable); err != nil {
			return err
		}
		if err := validateNarrativeList("pattern "+guide.ID+" mistakes", guide.Mistakes); err != nil {
			return err
		}
		for _, fieldID := range guide.Configurable {
			if _, found := fields[fieldID]; !found {
				return fmt.Errorf("pattern %q references unknown field %q", guide.ID, fieldID)
			}
		}
	}
	if err := validateExactIDs("pattern guide", seenPatterns, patternIDs); err != nil {
		return err
	}
	if err := validateViews(c.Views); err != nil {
		return err
	}
	if err := validateNotes("assertion note", c.AssertionNotes); err != nil {
		return err
	}
	if err := validateNotes("validation note", c.ValidationNotes); err != nil {
		return err
	}
	return validateExamples(examples, patterns, patternIDs)
}

func validateSections(sections []Section) error {
	seen := make(map[string]struct{}, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section.ID) == "" || strings.TrimSpace(section.Title) == "" || strings.TrimSpace(section.Summary) == "" {
			return fmt.Errorf("section has empty narrative")
		}
		if _, duplicate := seen[section.ID]; duplicate {
			return fmt.Errorf("duplicate section %q", section.ID)
		}
		seen[section.ID] = struct{}{}
	}
	return validateExactIDs("section", seen, []string{"quickstart", "schema", "patterns", "assertions", "validation"})
}

func validateLessons(lessons []Lesson, fields map[string]struct{}) error {
	seen := make(map[string]struct{}, len(lessons))
	for _, lesson := range lessons {
		if strings.TrimSpace(lesson.ID) == "" || strings.TrimSpace(lesson.Title) == "" || strings.TrimSpace(lesson.Concept) == "" || strings.TrimSpace(lesson.Effect) == "" || strings.TrimSpace(lesson.CommonMistake) == "" {
			return fmt.Errorf("lesson has empty narrative")
		}
		if _, duplicate := seen[lesson.ID]; duplicate {
			return fmt.Errorf("duplicate lesson %q", lesson.ID)
		}
		seen[lesson.ID] = struct{}{}
		if strings.TrimSpace(lesson.Fragment) == "" {
			return fmt.Errorf("lesson %q has empty fragment", lesson.ID)
		}
		for _, fieldID := range lesson.FieldIDs {
			if _, found := fields[fieldID]; !found {
				return fmt.Errorf("lesson %q references unknown field %q", lesson.ID, fieldID)
			}
		}
	}
	return validateExactIDs("lesson", seen, []string{"identity", "cases", "stimuli", "assertions", "validate"})
}

func validateViews(views []View) error {
	seen := make(map[string]struct{}, len(views))
	for _, view := range views {
		if strings.TrimSpace(view.ID) == "" || strings.TrimSpace(view.Title) == "" || strings.TrimSpace(view.Summary) == "" {
			return fmt.Errorf("view has empty narrative")
		}
		if _, duplicate := seen[view.ID]; duplicate {
			return fmt.Errorf("duplicate view %q", view.ID)
		}
		seen[view.ID] = struct{}{}
	}
	return validateExactIDs("view", seen, []string{"yaml", "contract", "api", "lifecycle"})
}

func validateNotes(kind string, notes []Note) error {
	seen := make(map[string]struct{}, len(notes))
	for _, note := range notes {
		if strings.TrimSpace(note.ID) == "" || strings.TrimSpace(note.Title) == "" || strings.TrimSpace(note.Content) == "" {
			return fmt.Errorf("%s has empty narrative", kind)
		}
		if _, duplicate := seen[note.ID]; duplicate {
			return fmt.Errorf("duplicate %s %q", kind, note.ID)
		}
		seen[note.ID] = struct{}{}
	}
	return nil
}

func validateNarrativeList(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s contains empty content", name)
		}
	}
	return nil
}

func validateExactIDs(kind string, seen map[string]struct{}, expected []string) error {
	if len(seen) != len(expected) {
		return fmt.Errorf("%s set has %d IDs, want exactly %d", kind, len(seen), len(expected))
	}
	for _, id := range expected {
		if _, found := seen[id]; !found {
			return fmt.Errorf("missing %s %q", kind, id)
		}
	}
	return nil
}

func validateExamples(examples []scenario.ExampleDefinition, patterns map[string]struct{}, patternIDs []string) error {
	seenIDs := make(map[string]struct{}, len(examples))
	seenSlots := make(map[string]struct{}, len(examples))
	for _, example := range examples {
		if _, found := patterns[example.Pattern]; !found {
			return fmt.Errorf("example %q has unknown pattern %q", example.ID, example.Pattern)
		}
		if example.Level != "basic" && example.Level != "advanced" {
			return fmt.Errorf("example %q has unknown level %q", example.ID, example.Level)
		}
		slot := example.Pattern + ":" + example.Level
		if _, duplicate := seenSlots[slot]; duplicate {
			return fmt.Errorf("duplicate example slot %q", slot)
		}
		seenSlots[slot] = struct{}{}
		if _, duplicate := seenIDs[example.ID]; duplicate {
			return fmt.Errorf("duplicate example ID %q", example.ID)
		}
		seenIDs[example.ID] = struct{}{}
		if example.ID != slot {
			return fmt.Errorf("example ID %q must equal %q", example.ID, slot)
		}
		if example.Scenario.Pattern != example.Pattern {
			return fmt.Errorf("example %q scenario pattern %q does not match %q", example.ID, example.Scenario.Pattern, example.Pattern)
		}
	}
	for _, patternID := range patternIDs {
		for _, level := range []string{"basic", "advanced"} {
			slot := patternID + ":" + level
			if _, found := seenSlots[slot]; !found {
				return fmt.Errorf("missing %s example for pattern %q", level, patternID)
			}
		}
	}
	if len(examples) != len(patternIDs)*2 {
		return fmt.Errorf("example registry has %d examples, want exactly %d", len(examples), len(patternIDs)*2)
	}
	return nil
}
