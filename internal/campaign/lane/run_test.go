package lane

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }
func (c *fakeClock) SleepUntil(ctx context.Context, t time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	c.mu.Lock()
	if t.After(c.t) {
		c.t = t
	}
	c.mu.Unlock()
	return true
}

type memLedger struct {
	mu       sync.Mutex
	intents  map[int]Event
	outcomes map[int]Outcome
}

func newLedger() *memLedger { return &memLedger{intents: map[int]Event{}, outcomes: map[int]Outcome{}} }
func (m *memLedger) Intent(_ context.Context, _ string, e Event, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.intents[e.Seq] = e
	return nil
}
func (m *memLedger) Outcome(_ context.Context, _ string, seq int, o Outcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes[seq] = o
	return nil
}
func (m *memLedger) has(seq int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.intents[seq]
	return ok
}

type fakeSender struct {
	clock *fakeClock
	check func(Event)
	fn    func(n int) (int, error)
	mu    sync.Mutex
	sends []time.Time
	stall int // 1-based send number that stalls 3s
}

func (f *fakeSender) Send(_ context.Context, e Event, _, _ string) (int, error) {
	if f.check != nil {
		f.check(e)
	}
	f.mu.Lock()
	f.sends = append(f.sends, f.clock.Now())
	n := len(f.sends)
	f.mu.Unlock()
	if n == f.stall {
		f.clock.advance(3 * time.Second)
	}
	if f.fn != nil {
		return f.fn(n)
	}
	return 202, nil
}

// firings-only plan at 3.5/s (resolves far beyond the window).
func firingPlan(d time.Duration) Plan {
	s := scenario.BaselineSpec{Rate: 3.5, UniqueRatio: 1, PoolSize: 1, Instances: 2,
		Severity: map[string]float64{"warning": 1}, ResolveAfter: time.Hour}
	p := Compile("r", 1, s, d)
	f := p.Events[:0:0]
	for _, e := range p.Events {
		if e.Kind == Firing {
			f = append(f, e)
		}
	}
	p.Events = f
	return p
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestIntentWrittenBeforeSend(t *testing.T) {
	clk, led := &fakeClock{t: t0}, newLedger()
	s := &fakeSender{clock: clk, check: func(e Event) {
		if !led.has(e.Seq) {
			t.Errorf("seq %d sent before intent", e.Seq)
		}
	}}
	c := Run(context.Background(), "baseline", firingPlan(2*time.Second), t0, s, led, clk, nil)
	if c.Offered != 7 || c.Accepted != 7 || len(led.outcomes) != 7 {
		t.Fatalf("%+v outcomes=%d", c, len(led.outcomes))
	}
}

func TestLaneNoCatchUpAfterStall(t *testing.T) {
	clk, led := &fakeClock{t: t0}, newLedger()
	s := &fakeSender{clock: clk, stall: 20}
	c := Run(context.Background(), "baseline", firingPlan(30*time.Second), t0, s, led, clk, nil)
	if c.Missed == 0 || c.Missed > 14 {
		t.Fatalf("missed=%d", c.Missed)
	}
	if len(s.sends)+c.Missed != c.Offered {
		t.Fatalf("sends=%d missed=%d offered=%d", len(s.sends), c.Missed, c.Offered)
	}
	min := time.Second*2/7 - 10*time.Millisecond
	for i := 21; i < len(s.sends); i++ { // after the stalled send
		if g := s.sends[i].Sub(s.sends[i-1]); g < min {
			t.Fatalf("burst: gap %v at send %d", g, i)
		}
	}
}

func TestClassifiesOutcomes(t *testing.T) {
	errs := []error{nil, nil, nil, context.DeadlineExceeded, &net.OpError{Op: "dial", Err: errors.New("refused")}}
	codes := []int{202, 429, 503, 0, 0}
	clk, led := &fakeClock{t: t0}, newLedger()
	s := &fakeSender{clock: clk, fn: func(n int) (int, error) { return codes[n-1], errs[n-1] }}
	p := firingPlan(2 * time.Second)
	p.Events = p.Events[:5]
	c := Run(context.Background(), "baseline", p, t0, s, led, clk, nil)
	if c.Accepted != 1 || c.Failed != 2 || c.Unknown != 2 {
		t.Fatalf("%+v", c)
	}
	want := []string{"", "http", "http", "timeout", "network"}
	for i, w := range want {
		if got := led.outcomes[i].ErrorClass; got != w {
			t.Errorf("seq %d class %q want %q", i, got, w)
		}
	}
}

type cancelClock struct {
	*fakeClock
	cancel context.CancelFunc
	after  int
	n      int
}

func (c *cancelClock) SleepUntil(ctx context.Context, t time.Time) bool {
	c.n++
	if c.n > c.after {
		c.cancel()
	}
	return c.fakeClock.SleepUntil(ctx, t)
}

func TestCancelStopsWithinOneEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := &fakeClock{t: t0}
	clk, led := &cancelClock{fakeClock: base, cancel: cancel, after: 3}, newLedger()
	s := &fakeSender{clock: base}
	c := Run(ctx, "baseline", firingPlan(10*time.Second), t0, s, led, clk, nil)
	if c.Offered != 3 || c.Offered != len(led.intents) {
		t.Fatalf("offered=%d intents=%d", c.Offered, len(led.intents))
	}
}

func TestAlertmanagerPayload(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(202)
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	cert, _ := x509.ParseCertificate(srv.TLS.Certificates[0].Certificate[0])
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewAlertmanagerSender(srv.URL, ca, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		e      Event
		ticket string
		ends   bool
	}{
		{Event{Kind: Firing, Identity: "A1", Severity: "critical", Instance: "h1", Ticket: true}, "snow", false},
		{Event{Kind: Resolve, Identity: "A1", Severity: "critical", Instance: "h1"}, "false", true},
		{Event{Kind: Resolve, Identity: "A1", Severity: "critical", Instance: "h1", Ticket: true}, "snow", true},
	} {
		got = nil
		if st, err := s.Send(context.Background(), tc.e, "run1", "baseline"); err != nil || st != 202 {
			t.Fatalf("st=%d err=%v", st, err)
		}
		if len(got) != 1 {
			t.Fatalf("alerts=%d", len(got))
		}
		l := got[0]["labels"].(map[string]any)
		for k, v := range map[string]string{"alertname": "A1", "campaign_run": "run1", "campaign_lane": "baseline",
			"severity": "critical", "instance": "h1", "create_ticket": tc.ticket} {
			if l[k] != v {
				t.Errorf("label %s=%v want %s", k, l[k], v)
			}
		}
		if _, has := got[0]["endsAt"]; has != tc.ends {
			t.Errorf("endsAt present=%v want %v", has, tc.ends)
		}
	}
	// Without the CA the self-signed server must be rejected.
	bad, _ := NewAlertmanagerSender(srv.URL, "", 5*time.Second)
	if _, err := bad.Send(context.Background(), Event{Kind: Firing}, "r", "baseline"); err == nil {
		t.Fatal("expected TLS verification failure")
	}
}

type failLedger struct {
	*memLedger
	n, failAt int
}

func (f *failLedger) Intent(ctx context.Context, l string, e Event, at time.Time) error {
	if f.n++; f.n == f.failAt {
		return errors.New("disk full")
	}
	return f.memLedger.Intent(ctx, l, e, at)
}

func TestLedgerFailureStopsAndReportsErr(t *testing.T) {
	clk := &fakeClock{t: t0}
	led := &failLedger{memLedger: newLedger(), failAt: 3}
	s := &fakeSender{clock: clk}
	var last Counters
	c := Run(context.Background(), "baseline", firingPlan(10*time.Second), t0, s, led, clk, func(c Counters) { last = c })
	if c.Err == nil || last.Err == nil {
		t.Fatalf("err not surfaced: %+v / %+v", c, last)
	}
	if len(s.sends) != 2 || led.has(2) {
		t.Fatalf("sends=%d intent for failed seq=%v", len(s.sends), led.has(2))
	}
}
