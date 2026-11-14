package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
)

// workerHeartbeatTimeout is how long a lane survives without controller heartbeats.
const workerHeartbeatTimeout = 30 * time.Second

var errWorkerOff = errors.New("campaign worker mode is not enabled")

// EnableCampaignWorker builds the worker lane manager from this runtime's
// configured target (the first one listed). It fails if no target exists.
func (r *Runtime) EnableCampaignWorker(ctx context.Context) error {
	targets, err := r.history.ListTargets(ctx)
	if err != nil {
		return fmt.Errorf("campaign worker: list targets: %w", err)
	}
	if len(targets) == 0 {
		return errors.New("campaign worker: no target is configured; create a target first")
	}
	if len(targets) > 1 {
		r.logger.WarnContext(ctx, "campaign worker: several targets configured; using the first", "target", targets[0].ID, "count", len(targets))
	}
	r.worker = worker.NewManager(workerStore{Ledger: storage.NewLaneLedger(r.database), db: r.database}, targets[0], worker.RealClock{}, workerHeartbeatTimeout)
	return nil
}

type workerStore struct {
	lane.Ledger
	db *storage.Database
}

func (s workerStore) LaneLedger(ctx context.Context, id string) ([]domain.LaneRow, error) {
	return s.db.LaneLedger(ctx, id)
}

func (r *Runtime) workerMgr() (*worker.Manager, error) {
	if r.worker == nil {
		return nil, errWorkerOff
	}
	return r.worker, nil
}

func (r *Runtime) Info(ctx context.Context) workerapi.Info {
	if m, err := r.workerMgr(); err == nil {
		return m.Info(ctx)
	}
	return workerapi.Info{Now: time.Now().UTC()}
}

func (r *Runtime) Start(ctx context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error) {
	m, err := r.workerMgr()
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	return m.Start(ctx, s)
}

func (r *Runtime) Heartbeat(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	m, err := r.workerMgr()
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	return m.Heartbeat(ctx, id)
}

// Stop stops a worker lane; it returns the lane's status afterwards.
func (r *Runtime) Stop(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	m, err := r.workerMgr()
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	if err := m.Stop(ctx, id); err != nil {
		return workerapi.LaneStatus{}, err
	}
	return m.Status(ctx, id)
}

func (r *Runtime) ResolveAll(ctx context.Context, id string) (workerapi.ResolveResult, error) {
	m, err := r.workerMgr()
	if err != nil {
		return workerapi.ResolveResult{}, err
	}
	return m.ResolveAll(ctx, id)
}

func (r *Runtime) Status(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	m, err := r.workerMgr()
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	return m.Status(ctx, id)
}

func (r *Runtime) Ledger(ctx context.Context, id string, w io.Writer) error {
	m, err := r.workerMgr()
	if err != nil {
		return err
	}
	return m.Ledger(ctx, id, w)
}
