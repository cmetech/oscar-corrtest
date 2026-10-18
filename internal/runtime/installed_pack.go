package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

// ExecuteInstalledPack owns one new execution until its durable outcome is
// settled. Reopening state or calling cleanup never invokes this method.
func (r *Runtime) ExecuteInstalledPack(ctx context.Context, kit *testpack.Kit, installation *testpack.Installation, apiTargetID, receiverTargetID string) (domain.Run, error) {
	// Admission and WaitGroup registration share Close's lock, so shutdown
	// cannot release the database while this execution is queued or running.
	r.activeMu.Lock()
	if r.closing {
		r.activeMu.Unlock()
		return domain.Run{}, fmt.Errorf("runtime is closing")
	}
	if err := ctx.Err(); err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	api, err := r.GetTarget(ctx, apiTargetID)
	if err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	receiver, err := r.GetTarget(ctx, receiverTargetID)
	if err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	engine, err := runner.NewInstalledPackRunner(kit, installation, api, receiver, r.database, r.artifacts, runner.InstalledPackRunnerOptions{Getenv: r.getenv, HarnessVersion: r.version})
	if err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	id, err := domain.NewRunID(rand.Reader)
	if err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	// Persist the mode with creation: a crash while waiting for runMu must not
	// expose an installed run to generic cleanup or hash-only export.
	plan, err := json.Marshal(struct {
		RunID     string `json:"runId"`
		Pattern   string `json:"pattern"`
		InputPath string `json:"inputPath"`
	}{id.String(), "flood", "installed-test-pack"})
	if err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	now := time.Now().UTC()
	run := domain.Run{ID: id.String(), ShortToken: id.Short(), TargetID: api.ID,
		Status: domain.RunQueued, CleanupStatus: domain.CleanupNotRequired,
		HarnessVersion: r.version, CreatedAt: now, UpdatedAt: now, CompiledPlanJSON: plan}
	if err = r.database.CreateRun(ctx, run); err != nil {
		r.activeMu.Unlock()
		return domain.Run{}, err
	}
	executionCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.rootCtx, cancel)
	r.active[run.ID] = cancel
	r.runWG.Add(1)
	r.activeMu.Unlock()
	defer func() {
		r.activeMu.Lock()
		delete(r.active, run.ID)
		r.activeMu.Unlock()
		stop()
		cancel()
		r.runWG.Done()
	}()
	r.runMu.Lock()
	defer r.runMu.Unlock()
	executeErr := engine.Execute(executionCtx, run)
	stored, getErr := r.GetRun(context.Background(), run.ID)
	if getErr != nil {
		return run, errors.Join(executeErr, getErr)
	}
	return stored, executeErr
}
