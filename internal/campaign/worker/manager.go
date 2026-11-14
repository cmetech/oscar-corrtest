// Package worker runs the paced baseline lane on a worker CorrTest. The
// controller starts a lane and must heartbeat it; if heartbeats stop for
// heartbeatTimeout the worker stops the lane and resolves its own alerts.
//
// Ledger conventions: planned events use their plan seq. ResolveAll appends
// resolves as new seqs beyond every existing seq (max+1, kind "resolve"), so a
// retried resolve never collides. The orphan_stop marker is LaneStatus.OrphanStopAt
// (the ledger kind column only permits firing/resolve).
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

const ingestPath = "/ext/alertmanager/api/v2/alerts"

// Store is the lane ledger plus read access to it.
type Store interface {
	lane.Ledger
	LaneLedger(ctx context.Context, laneID string) ([]domain.LaneRow, error)
}

type laneState struct {
	id      string
	runID   string
	sender  lane.Sender
	cancel  context.CancelFunc
	done    chan struct{} // closed when Run returned
	status  workerapi.LaneStatus
	counts  lane.Counters
	stopped bool
}

type Manager struct {
	store     Store
	target    domain.Target
	clock     lane.Clock
	timeout   time.Duration
	newSender func() (lane.Sender, error)
	tick      time.Duration

	resolveMu sync.Mutex // serializes ResolveAll
	mu        sync.Mutex
	lanes     map[string]*laneState
	running   string
}

func NewManager(store Store, target domain.Target, clock lane.Clock, heartbeatTimeout time.Duration) *Manager {
	m := &Manager{store: store, target: target, clock: clock, timeout: heartbeatTimeout, tick: time.Second, lanes: map[string]*laneState{}}
	m.newSender = func() (lane.Sender, error) {
		u, err := url.Parse(target.BaseURL)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("target base URL %q is invalid", target.BaseURL)
		}
		return lane.NewAlertmanagerSender(u.Scheme+"://"+u.Host+ingestPath, target.TLS.CAPath, 10*time.Second)
	}
	return m
}

func (m *Manager) Info(context.Context) workerapi.Info {
	return workerapi.Info{Version: version.Current().Version, Now: m.clock.Now().UTC()}
}

func hostOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	return strings.ToLower(u.Host), nil
}

func (m *Manager) Start(ctx context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error) {
	if s.Kind != "baseline" {
		return workerapi.LaneStatus{}, fmt.Errorf("unsupported lane kind %q", s.Kind)
	}
	var p workerapi.BaselineParams
	if err := json.Unmarshal(s.Params, &p); err != nil {
		return workerapi.LaneStatus{}, fmt.Errorf("lane params: %w", err)
	}
	if s.ID != s.CampaignID+"-baseline" {
		return workerapi.LaneStatus{}, fmt.Errorf("lane id %q must be <campaignId>-baseline (%q)", s.ID, s.CampaignID+"-baseline")
	}
	want, err := hostOf(m.target.BaseURL)
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	if got, err := hostOf(p.TargetURL); err != nil || got != want {
		return workerapi.LaneStatus{}, fmt.Errorf("target host %q does not match the configured target host %q", p.TargetURL, want)
	}
	if p.Duration <= 0 || p.Baseline.Rate <= 0 {
		return workerapi.LaneStatus{}, errors.New("lane needs a positive duration and rate")
	}
	snd, err := m.newSender()
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	if rows, err := m.store.LaneLedger(ctx, s.ID); err != nil {
		return workerapi.LaneStatus{}, err
	} else if len(rows) > 0 {
		return workerapi.LaneStatus{}, fmt.Errorf("%w: lane %s already has %d ledger rows; use a new campaign id", workerapi.ErrLaneExists, s.ID, len(rows))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != "" {
		return workerapi.LaneStatus{}, fmt.Errorf("%w: lane %s is running", workerapi.ErrLaneExists, m.running)
	}
	if _, ok := m.lanes[s.ID]; ok {
		return workerapi.LaneStatus{}, fmt.Errorf("%w: %s", workerapi.ErrLaneExists, s.ID)
	}
	start := p.StartAt
	if start.IsZero() {
		start = m.clock.Now()
	}
	plan := lane.Compile(s.CampaignID, p.Seed, p.Baseline, p.Duration)
	runCtx, cancel := context.WithCancel(context.Background()) // outlives the request
	ls := &laneState{id: s.ID, runID: s.CampaignID, sender: snd, cancel: cancel, done: make(chan struct{}),
		status: workerapi.LaneStatus{ID: s.ID, State: "running", LastHeartbeat: m.clock.Now()}}
	m.lanes[s.ID], m.running = ls, s.ID
	go func() {
		defer close(ls.done)
		c := lane.Run(runCtx, s.ID, plan, start, snd, m.store, m.clock, func(c lane.Counters) {
			m.mu.Lock()
			ls.counts = c
			m.mu.Unlock()
		})
		m.mu.Lock()
		ls.counts = c
		if c.Err != nil {
			ls.status.Error = c.Err.Error()
		}
		if ls.status.State == "running" {
			ls.status.State = "completed"
		}
		if m.running == s.ID {
			m.running = ""
		}
		m.mu.Unlock()
	}()
	go m.watchdog(ls)
	return m.snapshot(ls), nil
}

// watchdog ticks against clock.Now(); on a stale heartbeat it stops the lane,
// records the orphan_stop marker and resolves every open alert.
func (m *Manager) watchdog(ls *laneState) {
	t := time.NewTicker(m.tick)
	defer t.Stop()
	for {
		select {
		case <-ls.done:
			return
		case <-t.C:
		}
		m.mu.Lock()
		stale := ls.status.State == "running" && m.clock.Now().Sub(ls.status.LastHeartbeat) >= m.timeout
		if stale {
			ls.status.State = "orphan_stopped"
			now := m.clock.Now()
			ls.status.OrphanStopAt = &now
			ls.stopped = true
			ls.cancel()
		}
		m.mu.Unlock()
		if stale {
			<-ls.done
			if _, err := m.ResolveAll(context.Background(), ls.id); err != nil {
				m.mu.Lock()
				ls.status.Error = "orphan resolve-all: " + err.Error()
				m.mu.Unlock()
			}
			return
		}
	}
}

func (m *Manager) get(id string) (*laneState, error) {
	ls, ok := m.lanes[id]
	if !ok {
		return nil, workerapi.ErrLaneNotFound
	}
	return ls, nil
}

func (m *Manager) snapshot(ls *laneState) workerapi.LaneStatus { // m.mu held
	s := ls.status
	s.Sent, s.Failed, s.Missed = ls.counts.Accepted, ls.counts.Failed+ls.counts.Unknown, ls.counts.Missed
	return s
}

func (m *Manager) Heartbeat(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, err := m.get(id)
	if err != nil {
		return err
	}
	if ls.status.State == "running" {
		ls.status.LastHeartbeat = m.clock.Now()
	}
	return nil
}

func (m *Manager) Status(_ context.Context, id string) (workerapi.LaneStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, err := m.get(id)
	if err != nil {
		return workerapi.LaneStatus{}, err
	}
	return m.snapshot(ls), nil
}

// Stop cancels a running lane and waits for it to finish. It does not resolve.
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	ls, err := m.get(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if ls.status.State == "running" {
		ls.status.State = "stopped"
		ls.stopped = true
	}
	m.mu.Unlock()
	ls.cancel()
	select {
	case <-ls.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ResolveAll resolves every firing that was attempted and not missed (intent
// present, so a timed-out send that reached OSCAR counts) whose identity has no
// later accepted resolve. It derives this from the ledger alone (rows are in seq
// order; appended resolves sit after all planned seqs), so it also works for a
// lane this process no longer holds in memory (worker restart). Idempotent. A
// lane still running in this process must be stopped first.
func (m *Manager) ResolveAll(ctx context.Context, id string) (int, error) {
	m.resolveMu.Lock()
	defer m.resolveMu.Unlock()
	m.mu.Lock()
	ls := m.lanes[id]
	if ls != nil && ls.status.State == "running" {
		m.mu.Unlock()
		return 0, fmt.Errorf("lane %s is still running; stop it first", id)
	}
	m.mu.Unlock()
	if ls != nil {
		<-ls.done
	}
	rows, err := m.store.LaneLedger(ctx, id)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, workerapi.ErrLaneNotFound
	}
	var snd lane.Sender
	runID := strings.TrimSuffix(id, "-baseline")
	if ls != nil {
		snd, runID = ls.sender, ls.runID
	} else if snd, err = m.newSender(); err != nil {
		return 0, err
	}
	open := map[string]domain.LaneRow{}
	next := 0
	for _, r := range rows {
		next = max(next, r.Seq+1)
		switch {
		case r.Kind == string(lane.Firing) && !r.Missed:
			open[r.Identity] = r
		case r.Kind == string(lane.Resolve) && r.Done && r.Status >= 200 && r.Status <= 299:
			delete(open, r.Identity)
		}
	}
	// todo follows ledger (seq) order, so a retry after a partial failure resends in the same order.
	todo := make([]domain.LaneRow, 0, len(open))
	for _, r := range rows {
		if o, ok := open[r.Identity]; ok && o.Seq == r.Seq {
			todo = append(todo, r)
		}
	}
	n := 0
	var firstErr error
	for _, r := range todo {
		e := lane.Event{Seq: next, Kind: lane.Resolve, Identity: r.Identity, Severity: r.Severity, Instance: r.Instance, Ticket: r.Ticket, FiringSeq: r.Seq}
		next++
		now := m.clock.Now()
		if err := m.store.Intent(ctx, id, e, now); err != nil {
			return n, err
		}
		status, serr := snd.Send(ctx, e, runID, id)
		o := lane.Outcome{SentAt: now, Status: status, LatencyMillis: m.clock.Now().Sub(now).Milliseconds()}
		switch {
		case serr != nil:
			o.ErrorClass = lane.ErrClassNetwork
			firstErr = errors.Join(firstErr, serr)
		case status < 200 || status > 299:
			o.ErrorClass = lane.ErrClassHTTP
			firstErr = errors.Join(firstErr, fmt.Errorf("resolve %s: status %d", r.Identity, status))
		default:
			n++
		}
		if err := m.store.Outcome(ctx, id, e.Seq, o); err != nil {
			return n, err
		}
	}
	if ls != nil {
		m.mu.Lock()
		ls.status.Resolved += n
		m.mu.Unlock()
	}
	return n, firstErr
}

type ledgerRow struct {
	Seq           int       `json:"seq"`
	Kind          string    `json:"kind"`
	Identity      string    `json:"identity"`
	Severity      string    `json:"severity"`
	Instance      string    `json:"instance"`
	ScheduledAt   time.Time `json:"scheduledAt"`
	SentAt        time.Time `json:"sentAt,omitempty"`
	Done          bool      `json:"done"`
	Missed        bool      `json:"missed"`
	Status        int       `json:"status"`
	ErrorClass    string    `json:"errorClass,omitempty"`
	LatencyMillis int64     `json:"latencyMillis"`
}

// Ledger writes the lane's rows as NDJSON, one row per line. It does not write
// the terminal end record; the web layer does (see the ledger streaming
// contract: a stream with no {"type":"end"} record is truncated).
func (m *Manager) Ledger(ctx context.Context, id string, w io.Writer) error {
	rows, err := m.store.LaneLedger(ctx, id)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		m.mu.Lock()
		_, gerr := m.get(id)
		m.mu.Unlock()
		if gerr != nil {
			return gerr
		}
	}
	enc := json.NewEncoder(w)
	for _, r := range rows {
		if err := enc.Encode(ledgerRow{r.Seq, r.Kind, r.Identity, r.Severity, r.Instance, r.ScheduledAt, r.SentAt, r.Done, r.Missed, r.Status, r.ErrorClass, r.LatencyMillis}); err != nil {
			return err
		}
	}
	return nil
}

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) SleepUntil(ctx context.Context, t time.Time) bool {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err() == nil
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}

// Shutdown cancels every running lane without resolving (process exit) and
// waits up to 10 s for the lane goroutines so their last ledger write lands.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	var dones []chan struct{}
	for _, ls := range m.lanes {
		ls.cancel()
		dones = append(dones, ls.done)
	}
	m.mu.Unlock()
	deadline := time.After(10 * time.Second)
	for _, d := range dones {
		select {
		case <-d:
		case <-deadline:
			return
		}
	}
}
