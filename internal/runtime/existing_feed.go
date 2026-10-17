package runtime

import (
	"context"
	"errors"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
)

// ExecuteOwnedExistingFeed serializes the finite identity journey with other
// mutation runs. Ownership/immutable deployment proof remains external.
func (r *Runtime) ExecuteOwnedExistingFeed(ctx context.Context, plan runner.OwnedExistingFeedPlan) (domain.Run, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if err := plan.Validate(); err != nil {
		return domain.Run{}, err
	}
	receiver, err := r.GetTarget(ctx, plan.Manifest.TargetID)
	if err != nil {
		return domain.Run{}, err
	}
	native, err := r.GetTarget(ctx, plan.NativeTargetID)
	if err != nil {
		return domain.Run{}, err
	}
	history, err := r.GetTarget(ctx, plan.HistoryTargetID)
	if err != nil {
		return domain.Run{}, err
	}
	engine, err := runner.NewOwnedExistingFeedRunner(plan, receiver, native, history, r.database, oscar.Options{Getenv: r.getenv, HarnessVersion: r.version})
	if err != nil {
		return domain.Run{}, err
	}
	run, err := r.history.CreateRun(ctx, history.ID, "", r.version)
	if err != nil {
		return domain.Run{}, err
	}
	executeErr := engine.Execute(ctx, run)
	stored, getErr := r.GetRun(context.Background(), run.ID)
	if getErr != nil {
		return run, errors.Join(executeErr, getErr)
	}
	return stored, executeErr
}
