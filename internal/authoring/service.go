package authoring

import (
	"bytes"
	"context"
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

const (
	// PreviewRunID is a deterministic, non-live CorrTest identity used only by previews.
	PreviewRunID = "crt_PREV1EW1000000000000000000"
	// PreviewShortToken is PreviewRunID's first eight encoded body characters.
	PreviewShortToken = "PREV1EW1"
)

// Selection is the closed set of linkable authoring workspace choices.
type Selection struct {
	Section, Step, Pattern, Level, View string
}

// DefaultSelection is the stable landing selection for the authoring workspace.
func DefaultSelection() Selection {
	return Selection{Section: "quickstart", Step: "identity", Pattern: "flood", Level: "basic", View: "yaml"}
}

// Inspection is a strictly decoded document, deterministic compiler plan, and
// credential-free OSCAR operation preview.
type Inspection struct {
	Document   scenario.Scenario        `json:"document"`
	Source     string                   `json:"source"`
	Plan       compiler.Plan            `json:"plan"`
	Operations []oscar.OperationPreview `json:"operations"`
}

// Page is every server-renderable value for a selected known authoring example.
type Page struct {
	Selection  Selection
	Catalog    Catalog
	Contract   scenario.Contract
	Example    scenario.ExampleDefinition
	Inspection Inspection
}

// Service composes public contracts only; it deliberately owns no live dependencies.
type Service struct{ harnessVersion string }

// New returns a target-free authoring service.
func New(harnessVersion string) Service { return Service{harnessVersion: harnessVersion} }

// Build resolves a server-known cookbook example and renders its fixed Phase B preview.
func (s Service) Build(selection Selection) (Page, error) {
	normalized, err := normalizeSelection(selection)
	if err != nil {
		return Page{}, err
	}
	catalog, contract := DefaultCatalog(), scenario.PublicContract()
	if err := catalog.Validate(contract, scenario.AllExamples()); err != nil {
		return Page{}, fmt.Errorf("validate authoring catalog: %w", err)
	}
	example, found := scenario.LookupExample(normalized.Pattern, normalized.Level)
	if !found {
		return Page{}, fmt.Errorf("unknown authoring example %q:%q", normalized.Pattern, normalized.Level)
	}
	source, err := scenario.Encode(example.Scenario)
	if err != nil {
		return Page{}, fmt.Errorf("encode authoring example %q: %w", example.ID, err)
	}
	inspection, err := s.Inspect(context.Background(), source, "phase_b_dispatch")
	if err != nil {
		return Page{}, err
	}
	return Page{Selection: normalized, Catalog: catalog, Contract: contract, Example: example, Inspection: inspection}, nil
}

// Inspect strictly decodes and compiles source using a deterministic non-live
// identity. It never resolves a target or credential, uses randomness, or I/O.
func (s Service) Inspect(ctx context.Context, source []byte, pipelineMode string) (Inspection, error) {
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	document, err := scenario.Decode(bytes.NewReader(source))
	if err != nil {
		return Inspection{}, err
	}
	plan, err := compiler.Compile(domain.Run{ID: PreviewRunID, ShortToken: PreviewShortToken}, document, compiler.Capabilities{PipelineMode: pipelineMode})
	if err != nil {
		return Inspection{}, err
	}
	operations, err := oscar.BuildOperationPreview(plan, s.harnessVersion)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{Document: document, Source: string(source), Plan: plan, Operations: operations}, nil
}

func normalizeSelection(selection Selection) (Selection, error) {
	defaults := DefaultSelection()
	values := []struct {
		value    *string
		fallback string
		allowed  []string
		name     string
	}{
		{&selection.Section, defaults.Section, []string{"quickstart", "schema", "patterns", "assertions", "validation"}, "section"},
		{&selection.Step, defaults.Step, []string{"identity", "cases", "stimuli", "assertions", "validate"}, "step"},
		{&selection.Pattern, defaults.Pattern, scenario.SupportedPatterns(), "pattern"},
		{&selection.Level, defaults.Level, []string{"basic", "advanced"}, "level"},
		{&selection.View, defaults.View, []string{"yaml", "contract", "api", "lifecycle"}, "view"},
	}
	for _, item := range values {
		if *item.value == "" {
			*item.value = item.fallback
		}
		if !contains(item.allowed, *item.value) {
			return Selection{}, fmt.Errorf("invalid authoring %s %q", item.name, *item.value)
		}
	}
	return selection, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
