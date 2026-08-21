package runtime

import (
	"context"

	"github.com/cmetech/oscar-corrtest/internal/authoring"
)

// ScenarioInspection is a target-free source and compiled-contract preview.
type ScenarioInspection = authoring.Inspection

// InspectScenario parses and compiles source without reading targets, resolving
// credentials, contacting OSCAR, or writing durable state.
func (r *Runtime) InspectScenario(ctx context.Context, source []byte, pipelineMode string) (ScenarioInspection, error) {
	return authoring.New(r.Version()).Inspect(ctx, source, pipelineMode)
}
