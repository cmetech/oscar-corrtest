package runtime

import (
	"context"
	_ "embed"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

//go:embed fixtures/flood-ordinary-webhook.yaml
var ordinaryWebhookFixture string

// StartOrdinaryWebhook runs the existing pinned receiver fixture from the UI.
func (r *Runtime) StartOrdinaryWebhook(ctx context.Context, targetID, pipelineMode string) (domain.Run, error) {
	document, err := scenario.Decode(strings.NewReader(ordinaryWebhookFixture))
	if err != nil {
		return domain.Run{}, err
	}
	return r.startScenario(ctx, targetID, "", document, pipelineMode)
}
