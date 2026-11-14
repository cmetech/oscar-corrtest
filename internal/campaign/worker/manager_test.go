package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }
func (c *fakeClock) SleepUntil(ctx context.Context, t time.Time) bool {
	for c.Now().Before(t) { // block until the fake time passes t
		if ctx.Err() != nil {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return ctx.Err() == nil
}

type memStore struct {
	mu   sync.Mutex
	rows map[string]map[int]*domain.LaneRow
}

func (s *memStore) Intent(_ context.Context, id string, e lane.Event, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]map[int]*domain.LaneRow{}
	}
	if s.rows[id] == nil {
		s.rows[id] = map[int]*domain.LaneRow{}
	}
	if s.rows[id][e.Seq] != nil {
		return errors.New("duplicate")
	}
	s.rows[id][e.Seq] = &domain.LaneRow{LaneID: id, Seq: e.Seq, Kind: string(e.Kind), Identity: e.Identity, Severity: e.Severity, Instance: e.Instance, ScheduledAt: at}
	return nil
}
func (s *memStore) Outcome(_ context.Context, id string, seq int, o lane.Outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id][seq]
	r.Done, r.SentAt, r.Status, r.ErrorClass, r.Missed = true, o.SentAt, o.Status, o.ErrorClass, o.Missed
	return nil
}
func (s *memStore) LaneLedger(_ context.Context, id string) ([]domain.LaneRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LaneRow
	for i := 0; i < 1000; i++ {
		if r := s.rows[id][i]; r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

type fakeSender struct {
	mu    sync.Mutex
	sends []lane.Event
}

func (f *fakeSender) Send(_ context.Context, e lane.Event, _, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, e)
	return 200, nil
}
func (f *fakeSender) count(k lane.EventKind) (n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.sends {
		if e.Kind == k {
			n++
		}
	}
	return
}

func newMgr(t *testing.T) (*Manager, *fakeClock, *memStore, *fakeSender) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)}
	st, snd := &memStore{}, &fakeSender{}
	m := NewManager(st, domain.Target{BaseURL: "https://oscar.home.arpa:8443/x"}, clk, 30*time.Second)
	m.newSender = func() (lane.Sender, error) { return snd, nil }
	m.tick = 2 * time.Millisecond
	t.Cleanup(func() { _ = m.Stop(context.Background(), "r1-baseline") })
	return m, clk, st, snd
}

// run advances the fake clock in 250 ms steps, yielding so the lane keeps up.
func (c *fakeClock) run(d time.Duration, each func()) {
	for el := time.Duration(0); el < d; el += 250 * time.Millisecond {
		c.advance(250 * time.Millisecond)
		if each != nil {
			each()
		}
		time.Sleep(time.Millisecond)
	}
}

func spec(id, url string, dur time.Duration) workerapi.LaneSpec {
	p, _ := json.Marshal(workerapi.BaselineParams{TargetURL: url, Seed: 1, Duration: dur, Baseline: scenario.BaselineSpec{
		Rate: 1, UniqueRatio: 1, PoolSize: 1, Instances: 1, Severity: map[string]float64{"critical": 1}, ResolveAfter: time.Hour}})
	return workerapi.LaneSpec{ID: id, CampaignID: "r1", Kind: "baseline", Params: p}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestOrphanStopAfter30sWithoutHeartbeat(t *testing.T) {
	m, clk, st, snd := newMgr(t)
	ctx := context.Background()
	if _, err := m.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443/", 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	clk.run(10*time.Second, nil)
	waitFor(t, "firings", func() bool { return snd.count(lane.Firing) == 10 })
	clk.run(31*time.Second, nil)
	waitFor(t, "orphan", func() bool { s, _ := m.Status(ctx, "r1-baseline"); return s.State == "orphan_stopped" })
	s, _ := m.Status(ctx, "r1-baseline")
	if s.OrphanStopAt == nil {
		t.Fatal("no orphan_stop marker")
	}
	rows, _ := st.LaneLedger(ctx, "r1-baseline")
	open := 0
	for _, r := range rows {
		if r.Kind == "firing" && r.Status == 200 {
			open++
		}
	}
	if got := snd.count(lane.Resolve); got != open || open == 0 {
		t.Fatalf("auto resolves=%d accepted firings=%d", got, open)
	}
	if s.Resolved != open {
		t.Fatalf("status resolved=%d", s.Resolved)
	}
}

func TestHeartbeatKeepsLaneRunning(t *testing.T) {
	m, clk, _, _ := newMgr(t)
	ctx := context.Background()
	if _, err := m.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443", time.Hour)); err != nil {
		t.Fatal(err)
	}
	n := 0
	clk.run(60*time.Second, func() {
		if n++; n%20 == 0 { // every 5 s
			if err := m.Heartbeat(ctx, "r1-baseline"); err != nil {
				t.Fatal(err)
			}
		}
	})
	if s, _ := m.Status(ctx, "r1-baseline"); s.State != "running" {
		t.Fatalf("state=%s", s.State)
	}
}

func TestResolveAllIdempotent(t *testing.T) {
	m, clk, _, snd := newMgr(t)
	ctx := context.Background()
	if _, err := m.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443", 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	clk.run(5*time.Second, nil)
	waitFor(t, "firings", func() bool { return snd.count(lane.Firing) == 5 })
	if err := m.Stop(ctx, "r1-baseline"); err != nil {
		t.Fatal(err)
	}
	n, err := m.ResolveAll(ctx, "r1-baseline")
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = m.ResolveAll(ctx, "r1-baseline")
	if err != nil || n != 0 || snd.count(lane.Resolve) != 5 {
		t.Fatalf("second n=%d err=%v resolves=%d", n, err, snd.count(lane.Resolve))
	}
}

func TestRejectsMismatchedTarget(t *testing.T) {
	m, _, _, _ := newMgr(t)
	if _, err := m.Start(context.Background(), spec("r1-baseline", "https://evil.example:8443", time.Hour)); err == nil {
		t.Fatal("expected host mismatch error")
	}
}

func TestRejectsSecondLane(t *testing.T) {
	m, _, _, _ := newMgr(t)
	ctx := context.Background()
	if _, err := m.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443", time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err := m.Start(ctx, spec("r2-baseline", "https://oscar.home.arpa:8443", time.Hour))
	if err == nil || !errors.Is(err, workerapi.ErrLaneExists) {
		t.Fatalf("err=%v", err)
	}
}

func TestLedgerStreamsRows(t *testing.T) {
	m, clk, _, snd := newMgr(t)
	ctx := context.Background()
	_, _ = m.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443", 2*time.Second))
	clk.run(2*time.Second, nil)
	waitFor(t, "firings", func() bool { return snd.count(lane.Firing) == 2 })
	var b safeBuf
	if err := m.Ledger(ctx, "r1-baseline", &b); err != nil {
		t.Fatal(err)
	}
	if got := b.lines(); got < 2 {
		t.Fatalf("lines=%d", got)
	}
	if _, err := m.Status(ctx, "nope"); !errors.Is(err, workerapi.ErrLaneNotFound) {
		t.Fatal(err)
	}
	_ = fmt.Sprint()
}

type safeBuf struct{ b []byte }

func (s *safeBuf) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *safeBuf) lines() (n int) {
	for _, c := range s.b {
		if c == '\n' {
			n++
		}
	}
	return
}
