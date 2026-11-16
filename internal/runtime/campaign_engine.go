package runtime

import (
	"context"
	"errors"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// CampaignDatabase exposes the store the campaign engine records into.
func (r *Runtime) CampaignDatabase() *storage.Database { return r.database }

// CampaignDataDir is the data directory campaign ledgers live under.
func (r *Runtime) CampaignDataDir() string { return r.settings.DataDir }

// CampaignTarget resolves a target and its OSCAR client for the engine.
func (r *Runtime) CampaignTarget(ctx context.Context, targetID string) (domain.Target, *oscar.Client, error) {
	t, err := r.GetTarget(ctx, targetID)
	if err != nil {
		return domain.Target{}, nil, err
	}
	c, err := r.newOSCARClient(t)
	return t, c, err
}

// ExecuteScenarioForCampaign runs a correlation document for the active
// campaign (gate bypass, runMu per call).
func (r *Runtime) ExecuteScenarioForCampaign(ctx context.Context, targetID string, s scenario.Scenario, mode string) (domain.Run, error) {
	return r.executeScenarioForCampaign(ctx, targetID, s, mode)
}

// EnableCampaignEngine installs the engine and hands interrupted campaigns
// to its recovery. Call it once, before serving.
func (r *Runtime) EnableCampaignEngine(ctx context.Context, e CampaignEngine) error {
	r.activeMu.Lock()
	if r.campaignEngine != nil {
		r.activeMu.Unlock()
		return errors.New("campaign engine already configured")
	}
	r.campaignEngine = e
	r.activeMu.Unlock()
	return r.recoverCampaigns(ctx)
}
