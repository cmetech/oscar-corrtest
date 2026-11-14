package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	campaignscenario "github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

type fakeEngine struct {
	release   chan struct{}
	started   chan string
	gotOpts   CampaignOptions
	mu        sync.Mutex
	recovered []string
	cleaned   []string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{release: make(chan struct{}), started: make(chan string, 4)}
}

func (f *fakeEngine) Run(ctx context.Context, id string, o CampaignOptions) error {
	f.gotOpts = o
	f.started <- id
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return nil
}

func (f *fakeEngine) Cleanup(_ context.Context, id string) (domain.CleanupStatus, error) {
	f.mu.Lock()
	f.cleaned = append(f.cleaned, id)
	f.mu.Unlock()
	return domain.CleanupClean, nil
}

func (f *fakeEngine) Recover(_ context.Context, id string) error {
	f.mu.Lock()
	f.recovered = append(f.recovered, id)
	f.mu.Unlock()
	return nil
}

func openCampaignRuntime(t *testing.T, dir string, e CampaignEngine) *Runtime {
	t.Helper()
	r, err := OpenWithOptions(context.Background(), config.Settings{DataDir: dir, ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"}, Options{CampaignEngine: e})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStartCampaignWithoutEngine(t *testing.T) {
	r := openCampaignRuntime(t, t.TempDir(), nil)
	defer r.Close()
	if _, err := r.StartCampaign(context.Background(), CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"}); err == nil {
		t.Fatal("want engine-not-configured error")
	}
}

func TestCampaignGateBlocksOtherRuns(t *testing.T) {
	e := newFakeEngine()
	r := openCampaignRuntime(t, t.TempDir(), e)
	defer r.Close()
	ctx := context.Background()
	target, err := r.CreateTarget(ctx, domain.TargetInput{DisplayName: "Lab", BaseURL: "https://oscar.example", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	sc := campaignscenario.AlertingDayV1()
	c, err := r.StartCampaign(ctx, CampaignOptions{Scenario: sc, Trigger: "cli", RunBy: "agent", Acknowledged: []string{"adv-1"}, PrecheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	<-e.started
	if g := e.gotOpts; !g.PrecheckOnly || len(g.Acknowledged) != 1 || g.Acknowledged[0] != "adv-1" || g.RunBy != "agent" || g.Scenario.Hash() != sc.Hash() {
		t.Fatalf("engine options=%+v", g)
	}
	if _, err := r.startScenario(ctx, target.ID, "", scenario.Builtin("flood"), "phase_b_dispatch"); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("startScenario: %v", err)
	}
	if _, _, _, err := r.createRegisteredRun(ctx, target.ID, ""); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("createRegisteredRun: %v", err)
	}
	if c.RunBy != "agent" || c.Status != domain.CampaignPrecheck {
		t.Fatalf("campaign=%+v", c)
	}
	if _, err := r.StartBuiltin(ctx, target.ID, "flood", "phase_b_dispatch"); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("StartBuiltin: %v", err)
	}
	if _, err := r.ExecuteBuiltin(ctx, "tgt", "flood", "phase_b_dispatch"); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("ExecuteBuiltin: %v", err)
	}
	if _, err := r.ExecuteScenario(ctx, "tgt", scenario.Builtin("flood"), "phase_b_dispatch"); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("ExecuteScenario: %v", err)
	}
	if _, err := r.StartTrapLoad(ctx, "tgt", TrapLoadOptions{}); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("StartTrapLoad: %v", err)
	}
	if _, err := r.StartCampaign(ctx, CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"}); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("second StartCampaign: %v", err)
	}
	if runs, err := r.ListRuns(ctx, domain.RunFilter{}); err != nil || len(runs) != 0 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	view, err := r.CampaignStatus(ctx, c.ID)
	if err != nil || view.Campaign.ID != c.ID || len(view.Events) == 0 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	close(e.release)
	deadline := time.Now().Add(2 * time.Second)
	for r.campaignActive.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.campaignActive.Load() {
		t.Fatal("gate not released after engine returned")
	}
}

func TestCampaignCanRunScenarioInternally(t *testing.T) {
	e := newFakeEngine()
	r := openCampaignRuntime(t, t.TempDir(), e)
	defer r.Close()
	if _, err := r.StartCampaign(context.Background(), CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"}); err != nil {
		t.Fatal(err)
	}
	<-e.started
	// Unknown target: must fail past the gate (and must not deadlock on runMu).
	_, err := r.executeScenarioForCampaign(context.Background(), "tgt_missing", scenario.Builtin("flood"), "phase_b_dispatch")
	if err == nil || errors.Is(err, ErrCampaignActive) {
		t.Fatalf("err=%v, want a non-gate error", err)
	}
}

func TestStopCampaignCancelsEngine(t *testing.T) {
	e := newFakeEngine()
	r := openCampaignRuntime(t, t.TempDir(), e)
	defer r.Close()
	c, err := r.StartCampaign(context.Background(), CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"})
	if err != nil {
		t.Fatal(err)
	}
	<-e.started
	// PRECHECK -> STOPPING is not a legal transition; move to RUNNING first.
	if err := r.database.TransitionCampaign(context.Background(), c.ID, domain.CampaignRunning, time.Now().UTC(), "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.StopCampaign(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.StopCampaign(context.Background(), "camp_other"); err == nil {
		t.Fatal("stop of unknown campaign must fail")
	}
	view, _ := r.CampaignStatus(context.Background(), c.ID)
	if view.Campaign.Status != domain.CampaignStopping && view.Campaign.Status != domain.CampaignInterrupted {
		t.Fatalf("status=%s", view.Campaign.Status)
	}
}

func TestRecoverCampaignsOnOpen(t *testing.T) {
	dir := t.TempDir()
	r := openCampaignRuntime(t, dir, nil)
	c := domain.Campaign{ID: "camp_seed", ScenarioID: "alerting-day", Trigger: "cli", RunBy: "human", Status: domain.CampaignRunning, StartedAt: time.Now().UTC()}
	if err := r.database.CreateCampaign(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	e := newFakeEngine()
	r = openCampaignRuntime(t, dir, e)
	if err := r.Close(); err != nil { // Close waits for the recovery goroutine.
		t.Fatal(err)
	}
	if len(e.recovered) != 1 || e.recovered[0] != "camp_seed" {
		t.Fatalf("recovered=%v", e.recovered)
	}
	r = openCampaignRuntime(t, dir, nil)
	defer r.Close()
	got, err := r.database.GetCampaign(context.Background(), "camp_seed")
	if err != nil || got.Status != domain.CampaignInterrupted {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestStartRefusesWhenMutationRunActive(t *testing.T) {
	e := newFakeEngine()
	r := openCampaignRuntime(t, t.TempDir(), e)
	defer r.Close()
	r.activeMu.Lock()
	r.active["run_x"] = func() {}
	r.activeMu.Unlock()
	if _, err := r.StartCampaign(context.Background(), CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"}); err == nil {
		t.Fatal("want refusal while a mutation run is active")
	}
	if r.campaignActive.Load() {
		t.Fatal("gate must not be set on refusal")
	}
	r.activeMu.Lock()
	delete(r.active, "run_x")
	r.activeMu.Unlock()
}

func TestStopDuringPrecheckCancels(t *testing.T) {
	e := newFakeEngine()
	r := openCampaignRuntime(t, t.TempDir(), e)
	defer r.Close()
	ctx := context.Background()
	c, err := r.StartCampaign(ctx, CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"})
	if err != nil {
		t.Fatal(err)
	}
	<-e.started
	if err := r.StopCampaign(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.campaignActive.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.campaignActive.Load() {
		t.Fatal("engine context was not cancelled by stop during PRECHECK")
	}
	view, _ := r.CampaignStatus(ctx, c.ID)
	if view.Campaign.Status != domain.CampaignInterrupted {
		t.Fatalf("status=%s", view.Campaign.Status)
	}
}

func TestWorkerCampaignModeNeedsTarget(t *testing.T) {
	r := openCampaignRuntime(t, t.TempDir(), nil)
	defer r.Close()
	if _, err := r.Status(context.Background(), "x"); err == nil {
		t.Fatal("worker methods must fail when worker mode is off")
	}
	if err := r.EnableCampaignWorker(context.Background()); err == nil {
		t.Fatal("want no-target error")
	}
}

// Ruling C40: cleanup is refused while any campaign is active.
func TestCleanupCampaignRefusedWhileActive(t *testing.T) {
	e := newFakeEngine()
	r := openCampaignRuntime(t, t.TempDir(), e)
	defer r.Close()
	c, err := r.StartCampaign(context.Background(), CampaignOptions{Scenario: campaignscenario.AlertingDayV1(), Trigger: "cli", RunBy: "human"})
	if err != nil {
		t.Fatal(err)
	}
	<-e.started
	if _, err := r.CleanupCampaign(context.Background(), "camp_old"); !errors.Is(err, ErrCampaignActive) {
		t.Fatalf("cleanup while active: %v", err)
	}
	close(e.release)
	waitIdle := time.Now().Add(5 * time.Second)
	for r.campaignActive.Load() && time.Now().Before(waitIdle) {
		time.Sleep(5 * time.Millisecond)
	}
	if st, err := r.CleanupCampaign(context.Background(), c.ID); err != nil || st != domain.CleanupClean || len(e.cleaned) != 1 {
		t.Fatalf("cleanup %s %v %v", st, err, e.cleaned)
	}
	if r.campaignActive.Load() {
		t.Fatal("cleanup left the campaign gate held")
	}
}
