package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// ScenarioInspection is a target-free source and compiled-contract preview.
type ScenarioInspection struct {
	Document scenario.Scenario `json:"document"`
	Source   string            `json:"source"`
	Plan     compiler.Plan     `json:"plan"`
}

// InspectScenario parses and compiles source without reading targets, resolving
// credentials, contacting OSCAR, or writing durable state.
func (r *Runtime) InspectScenario(ctx context.Context, source []byte, pipelineMode string) (ScenarioInspection, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioInspection{}, err
	}
	document, err := scenario.Decode(bytes.NewReader(source))
	if err != nil {
		return ScenarioInspection{}, err
	}
	id, err := domain.NewRunID(rand.Reader)
	if err != nil {
		return ScenarioInspection{}, fmt.Errorf("create inspection identity: %w", err)
	}
	plan, err := compiler.Compile(domain.Run{ID: id.String(), ShortToken: id.Short()}, document, compiler.Capabilities{PipelineMode: pipelineMode})
	if err != nil {
		return ScenarioInspection{}, err
	}
	return ScenarioInspection{Document: document, Source: string(source), Plan: plan}, nil
}
