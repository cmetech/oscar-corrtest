package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	campaignscenario "github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

// ErrCampaignActive is returned by every other mutation run while a campaign owns the target.
var ErrCampaignActive = errors.New("a campaign is active; other test runs are blocked")

// CampaignEngine executes and recovers campaigns. The real engine arrives in a later task.
//
// Run's ctx is cancelled by StopCampaign and by Runtime.Close. Drain and cleanup
// must therefore run on context.WithoutCancel(ctx). opts carries the full scenario,
// Acknowledged, PrecheckOnly and RunBy.
type CampaignEngine interface {
	Run(ctx context.Context, campaignID string, opts CampaignOptions) error
	Recover(ctx context.Context, campaignID string) error
}

// CampaignOptions describes one campaign start. RunBy is agent|human.
type CampaignOptions struct {
	Scenario        campaignscenario.Scenario
	Trigger         string
	TriggerIdentity string
	RunBy           string
	Acknowledged    []string // advisory IDs
	PrecheckOnly    bool
}

// CampaignView is a campaign plus its most recent events, for status endpoints.
type CampaignView struct {
	Campaign domain.Campaign        `json:"campaign"`
	Events   []domain.CampaignEvent `json:"events"`
}

const campaignViewEvents = 50

// gateCampaign marks the gate and records cancel under activeMu, so the gate
// and the mutation-run registry are checked atomically against each other.
func (r *Runtime) gateCampaign(id string, cancel context.CancelFunc) error {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.closing {
		return fmt.Errorf("runtime is closing")
	}
	if r.campaignActive.Load() {
		return ErrCampaignActive
	}
	if len(r.active) > 0 {
		return fmt.Errorf("wait for the active run and its cleanup before starting a campaign")
	}
	r.campaignActive.Store(true)
	r.campaignID, r.campaignCancel = id, cancel
	r.runWG.Add(1)
	return nil
}

// releaseCampaign clears the gate and balances gateCampaign's runWG.Add.
func (r *Runtime) releaseCampaign() {
	defer r.runWG.Done()
	r.activeMu.Lock()
	r.campaignActive.Store(false)
	r.campaignID, r.campaignCancel = "", nil
	r.activeMu.Unlock()
}

// StartCampaign persists a PRECHECK campaign and runs the engine in the background.
func (r *Runtime) StartCampaign(ctx context.Context, o CampaignOptions) (domain.Campaign, error) {
	if r.campaignEngine == nil {
		return domain.Campaign{}, errors.New("campaign engine not configured")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return domain.Campaign{}, fmt.Errorf("create campaign id: %w", err)
	}
	c := domain.Campaign{
		ID: "camp_" + hex.EncodeToString(raw[:]), ScenarioID: o.Scenario.ID, ScenarioHash: o.Scenario.Hash(), Custom: o.Scenario.Custom,
		Trigger: o.Trigger, RunBy: o.RunBy, TriggerIdentity: o.TriggerIdentity, Status: domain.CampaignPrecheck, StartedAt: time.Now().UTC(),
	}
	execCtx, cancel := context.WithCancel(r.rootCtx)
	// A synchronous Execute* run holds runMu; refuse rather than queue behind it.
	if !r.runMu.TryLock() {
		cancel()
		return domain.Campaign{}, errors.New("a synchronous run is active")
	}
	r.runMu.Unlock()
	if err := r.gateCampaign(c.ID, cancel); err != nil {
		cancel()
		return domain.Campaign{}, err
	}
	if err := r.database.CreateCampaign(ctx, c); err != nil {
		cancel()
		r.releaseCampaign()
		return domain.Campaign{}, err
	}
	// Audit record only; the engine receives the options directly.
	detail, _ := json.Marshal(map[string]any{"acknowledged": o.Acknowledged, "precheckOnly": o.PrecheckOnly, "runBy": o.RunBy})
	if err := r.database.AppendCampaignEvent(ctx, c.ID, "campaign.options", "info", "campaign options recorded", detail, c.StartedAt); err != nil {
		_ = r.database.TransitionCampaign(context.WithoutCancel(ctx), c.ID, domain.CampaignInterrupted, time.Now().UTC(), "options audit failed")
		cancel()
		r.releaseCampaign()
		return domain.Campaign{}, fmt.Errorf("record campaign options: %w", err)
	}
	go func() {
		defer r.releaseCampaign()
		defer cancel()
		stable := context.WithoutCancel(execCtx)
		if err := r.campaignEngine.Run(execCtx, c.ID, o); err != nil {
			_ = r.database.AppendCampaignEvent(stable, c.ID, "campaign.error", "error", "campaign engine failed: "+err.Error(), nil, time.Now().UTC())
			// Leave a non-COMPLETED campaign for recovery; invalid transitions (already terminal) are ignored.
			_ = r.database.TransitionCampaign(stable, c.ID, domain.CampaignInterrupted, time.Now().UTC(), "engine stopped before completion")
		}
	}()
	return c, nil
}

// StopCampaign always cancels the matching campaign's context. It records STOPPING
// when that transition is legal, INTERRUPTED from PRECHECK. A status-write failure is
// returned after the cancel. The engine must drain and clean up on a detached context.
func (r *Runtime) StopCampaign(ctx context.Context, id string) error {
	r.activeMu.Lock()
	cancel, ok := r.campaignCancel, r.campaignActive.Load() && r.campaignID == id
	r.activeMu.Unlock()
	if !ok {
		return errors.New("campaign is not active in this process")
	}
	c, err := r.database.GetCampaign(ctx, id)
	next := domain.CampaignStopping
	if err == nil && c.Status == domain.CampaignPrecheck {
		next = domain.CampaignInterrupted
	}
	if err == nil && c.Status != next {
		err = r.database.TransitionCampaign(ctx, id, next, time.Now().UTC(), "stop requested")
	}
	cancel()
	return err
}

// CampaignStatus returns a campaign and its recent events.
func (r *Runtime) CampaignStatus(ctx context.Context, id string) (CampaignView, error) {
	c, err := r.database.GetCampaign(ctx, id)
	if err != nil {
		return CampaignView{}, err
	}
	events, err := r.database.RecentCampaignEvents(ctx, id, campaignViewEvents)
	if err != nil {
		return CampaignView{}, err
	}
	return CampaignView{Campaign: c, Events: events}, nil
}

// executeScenarioForCampaign is the engine's internal scenario run: it bypasses the
// campaign gate and takes runMu per call, so the campaign goroutine never holds it.
func (r *Runtime) executeScenarioForCampaign(ctx context.Context, targetID string, s scenario.Scenario, mode string) (domain.Run, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.executeScenarioLocked(ctx, targetID, s, mode)
}

// recoverCampaigns hands every unfinished campaign to the engine, one at a time,
// holding the gate for the duration. Without an engine nothing is touched.
func (r *Runtime) recoverCampaigns(ctx context.Context) error {
	if r.campaignEngine == nil {
		return nil
	}
	pending, err := r.database.InterruptedCampaigns(ctx)
	if err != nil || len(pending) == 0 {
		return err
	}
	for _, c := range pending {
		if c.Status == domain.CampaignInterrupted {
			continue
		}
		if err := r.database.TransitionCampaign(ctx, c.ID, domain.CampaignInterrupted, time.Now().UTC(), "runtime restarted; handing to recovery"); err != nil {
			return fmt.Errorf("interrupt campaign %s: %w", c.ID, err)
		}
	}
	recoverCtx, cancel := context.WithCancel(r.rootCtx)
	if err := r.gateCampaign(pending[0].ID, cancel); err != nil {
		cancel()
		return err
	}
	go func() {
		defer cancel()
		defer r.releaseCampaign()
		for _, c := range pending {
			if err := r.campaignEngine.Recover(recoverCtx, c.ID); err != nil {
				r.logger.Error("campaign recovery failed", "campaign_id", c.ID, "error", err)
			}
		}
	}()
	return nil
}
