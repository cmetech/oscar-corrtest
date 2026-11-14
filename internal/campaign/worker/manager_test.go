package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	s.rows[id][e.Seq] = &domain.LaneRow{LaneID: id, Seq: e.Seq, Kind: string(e.Kind), Identity: e.Identity, Severity: e.Severity, Instance: e.Instance, Ticket: e.Ticket, ScheduledAt: at}
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

// fireN lets the 1/s lane send exactly n firings with no lag: it waits for each
// firing to be sent before advancing the fake clock to the next one, so no
// event is ever late enough to be recorded as missed.
func fireN(t *testing.T, clk *fakeClock, snd *fakeSender, n int) {
	t.Helper()
	for k := 1; k <= n; k++ {
		waitFor(t, "firing", func() bool { return snd.count(lane.Firing) >= k })
		clk.advance(time.Second)
	}
}

func spec(id, url string, dur time.Duration) workerapi.LaneSpec {
	p, _ := json.Marshal(workerapi.BaselineParams{TargetURL: url, Seed: 1, Duration: dur, Baseline: scenario.BaselineSpec{
		Rate: 1, UniqueRatio: 1, PoolSize: 1, Instances: 1, Severity: map[string]float64{"critical": 1}, ResolveAfter: time.Hour}})
	return workerapi.LaneSpec{ID: id, CampaignID: strings.TrimSuffix(id, "-baseline"), Kind: "baseline", Params: p}
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
	fireN(t, clk, snd, 10)
	clk.advance(31 * time.Second) // no heartbeat for >30 s
	// State flips to orphan_stopped before the automatic ResolveAll runs, so wait
	// for its completion (Resolved is updated last) instead of the state alone.
	waitFor(t, "orphan resolve-all", func() bool {
		s, _ := m.Status(ctx, "r1-baseline")
		return s.State == "orphan_stopped" && s.Resolved == 10
	})
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
			if _, err := m.Heartbeat(ctx, "r1-baseline"); err != nil {
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
	fireN(t, clk, snd, 5)
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
	fireN(t, clk, snd, 2)
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

func TestResolveAllRecoversAfterRestart(t *testing.T) {
	m, clk, st, snd := newMgr(t)
	ctx := context.Background()
	if _, err := m.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443", 3*time.Second)); err != nil {
		t.Fatal(err)
	}
	fireN(t, clk, snd, 3)
	_ = m.Stop(ctx, "r1-baseline")

	snd2 := &fakeSender{}
	m2 := NewManager(st, domain.Target{BaseURL: "https://oscar.home.arpa:8443"}, clk, 30*time.Second)
	m2.newSender = func() (lane.Sender, error) { return snd2, nil }
	if _, err := m2.ResolveAll(ctx, "nope-baseline"); !errors.Is(err, workerapi.ErrLaneNotFound) {
		t.Fatalf("err=%v", err)
	}
	if n, err := m2.ResolveAll(ctx, "r1-baseline"); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n, _ := m2.ResolveAll(ctx, "r1-baseline"); n != 0 {
		t.Fatalf("second n=%d", n)
	}
	if _, err := m2.Start(ctx, spec("r1-baseline", "https://oscar.home.arpa:8443", time.Hour)); !errors.Is(err, workerapi.ErrLaneExists) {
		t.Fatalf("reused lane id err=%v", err)
	}
	if _, err := m2.Start(ctx, workerapi.LaneSpec{ID: "x", CampaignID: "y", Kind: "baseline"}); err == nil {
		t.Fatal("lane id must be <campaign>-baseline")
	}
}

func TestResolveAllIncludesUnknownOutcomeAndTicket(t *testing.T) {
	m, clk, st, snd := newMgr(t)
	ctx := context.Background()
	_ = st.Intent(ctx, "r1-baseline", lane.Event{Seq: 0, Kind: lane.Firing, Identity: "A", Severity: "critical", Instance: "h", Ticket: true}, clk.Now())
	_ = st.Outcome(ctx, "r1-baseline", 0, lane.Outcome{SentAt: clk.Now(), ErrorClass: lane.ErrClassTimeout})
	_ = st.Intent(ctx, "r1-baseline", lane.Event{Seq: 1, Kind: lane.Firing, Identity: "B", Severity: "critical", Instance: "h"}, clk.Now())
	_ = st.Outcome(ctx, "r1-baseline", 1, lane.Outcome{SentAt: clk.Now(), ErrorClass: lane.ErrClassMissed, Missed: true})
	n, err := m.ResolveAll(ctx, "r1-baseline")
	if err != nil || n != 1 || len(snd.sends) != 1 || !snd.sends[0].Ticket || snd.sends[0].Identity != "A" {
		t.Fatalf("n=%d err=%v sends=%+v", n, err, snd.sends)
	}
}

// Ruling C38: a definite HTTP answer (any status) is Failed, never Unknown.
func TestSnapshotSeparatesDefiniteFailuresFromUnknown(t *testing.T) {
	m := &Manager{}
	s := m.snapshot(&laneState{counts: lane.Counters{Accepted: 5, Failed: 2, Unknown: 1, Missed: 3}})
	if s.Sent != 5 || s.Failed != 2 || s.Unknown != 1 || s.Missed != 3 {
		t.Fatalf("snapshot %+v", s)
	}
}
