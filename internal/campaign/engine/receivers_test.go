package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/receivers"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runtime"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// fakeRx is OSCAR's mapping API and the three receivers as the lane sees
// them: a probe through an installed path lands in History 2 s later with
// its labels; the trap path needs the campaign trap mapping (without it the
// row carries the sender's address, as the trap receiver's defaults do).
type fakeRx struct {
	mu       sync.Mutex
	clock    interface{ Now() time.Time }
	mappings map[string][]receivers.MappingElement // ns/name
	rows     []oscar.HistoryRecord
	sends    map[string]int
	// Behaviour switches.
	dropSMTP   bool // the SMTP receiver accepts and loses every message
	failCreate bool
	keepAfter  bool // delete answers 204 but the Mapping stays
	webhookErr error
	reads      int
	creates    int
}

func newFakeRx(clock interface{ Now() time.Time }) *fakeRx {
	return &fakeRx{clock: clock, mappings: map[string][]receivers.MappingElement{}, sends: map[string]int{}}
}

func key(ns, name string) string { return strings.ToLower(ns) + "/" + name }

func (f *fakeRx) Mapping(_ context.Context, ns, name string) ([]receivers.MappingElement, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	els, ok := f.mappings[key(ns, name)]
	out := slices.Clone(els) // OSCAR answers in its index order, not write order (code review F1)
	slices.Reverse(out)
	return out, ok, nil
}

func (f *fakeRx) MappingNames(_ context.Context, ns, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.mappings {
		if n := strings.TrimPrefix(k, strings.ToLower(ns)+"/"); n != k && strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeRx) CreateMapping(_ context.Context, ns, name, _ string, els []receivers.MappingElement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreate {
		return errors.New("HTTP 500")
	}
	f.creates++
	f.mappings[key(ns, name)] = append([]receivers.MappingElement(nil), els...)
	return nil
}

func (f *fakeRx) DeleteMapping(_ context.Context, ns, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.keepAfter {
		delete(f.mappings, key(ns, name))
	}
	return nil
}

func (f *fakeRx) Send(_ context.Context, rx, profile string, p receivers.Probe) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends[rx]++
	labels := map[string]string{}
	for k, v := range p.Labels {
		labels[k] = v
	}
	switch rx {
	case "webhook":
		if f.webhookErr != nil {
			return f.webhookErr
		}
		if _, ok := f.mappings[key(receivers.ProfileNamespace, profile)]; !ok {
			return errors.New("webhook HTTP 404 status \"\"") // no such profile
		}
	case "trap":
		if _, ok := f.mappings[key(receivers.TrapNamespace, receivers.TrapOID)]; !ok {
			labels["instance"], labels["meta_ipaddress"] = "192.0.2.24", "192.0.2.24" // unmapped: the sender's address
			delete(labels, "meta_hostname")
		}
	case "smtp":
		if f.dropSMTP {
			return nil
		}
	}
	sev := labels["severity"]
	delete(labels, "severity") // History promotes severity
	f.rows = append(f.rows, oscar.HistoryRecord{AlertName: labels["alertname"], Fingerprint: "fp-" + labels["alertname"], Status: "firing",
		Severity: &sev, StartsAt: p.StartsAt, CreatedAt: f.clock.Now().Add(2 * time.Second), Labels: labels})
	return nil
}

func (f *fakeRx) ProbeHistory(_ context.Context, start, end time.Time) ([]oscar.HistoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	var out []oscar.HistoryRecord
	for _, r := range f.rows {
		if !r.StartsAt.Before(start) && !r.StartsAt.After(end) && !r.CreatedAt.After(f.clock.Now()) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRx) Reachable(context.Context) []string { return nil }

// rxHarness is the v4 harness with a receivers lane: probes every 10 s from
// T+20s to T+300s (a spike at T+100s skips T+70s to T+130s), 60 s bound.
func rxHarness(t *testing.T) (*harness, *fakeRx) {
	h, _ := v4Harness(t)
	h.s.Baseline.Servers, h.s.Correlation.Servers = 4, 2
	h.s.Baseline.Bursts = []scenario.BurstSpec{{At: 100 * time.Second, For: 30 * time.Second, Rate: 2}}
	h.s.Receivers = &scenario.ReceiversSpec{From: 20 * time.Second, Until: 300 * time.Second, Every: 10 * time.Second, Bound: time.Minute}
	h.oscar.s = h.s
	f := newFakeRx(h.clock)
	h.deps.Receivers = f
	return h, f
}

func (h *harness) rxCard() score.Scorecard {
	h.t.Helper()
	var card score.Scorecard
	if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil || card.Receivers == nil {
		h.t.Fatalf("no Receivers section: %v", err)
	}
	return card
}

// TestReceiversLane (OSC-207 AC-5, AC-6): both Mappings are installed as
// recorded mutations and removed with read-back, every probe lands with its
// labels, nothing in the spike margin, nothing counted as offered load,
// rolling reads; cleanup CLEAN.
func TestReceiversLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, f := rxHarness(t)
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		card := h.rxCard()
		rx := card.Receivers
		for _, r := range card.Reasons {
			if strings.HasPrefix(r, "receivers") {
				t.Errorf("unexpected reason: %s", r)
			}
		}
		ticks := 0
		for at := 20 * time.Second; at <= 300*time.Second; at += 10 * time.Second {
			if at < 70*time.Second || at >= 160*time.Second {
				ticks++
			}
		}
		if rx.Planned != 3*ticks || len(rx.Probes) != rx.Planned || !rx.Removed || len(f.mappings) != 0 {
			t.Errorf("planned %d probes %d removed %v left %v", rx.Planned, len(rx.Probes), rx.Removed, f.mappings)
		}
		for _, n := range receivers.Names {
			if c := rx.Receivers[n]; c.Landed != ticks || c.LatencyP95 > 3 {
				t.Errorf("%s: %+v", n, c)
			}
		}
		for _, p := range rx.Probes {
			if p.Server < 5 || p.Server > 6 {
				t.Errorf("probe server %d outside the correlation servers", p.Server)
			}
		}
		if f.reads < 3 {
			t.Errorf("only %d History reads: not rolling", f.reads)
		}
		open, _ := h.db.OpenMutations(context.Background(), h.id)
		if len(open) != 0 || f.creates != 2 { // "mapping stays" proves they are journaled
			t.Errorf("open %v, creates %d", open, f.creates)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupClean {
			t.Errorf("cleanup %s", c.Cleanup)
		}
		for _, o := range card.Offered.PerMinute {
			if o.Lanes[receivers.Lane] > 0 {
				t.Errorf("receiver probes counted as offered load: %+v", o)
			}
		}
	})
}

// TestReceiversLaneFailures (AC-5, AC-6): a lost SMTP probe and a trap
// without its mapping FAIL; a failed install is INCONCLUSIVE; a Mapping that
// will not go keeps cleanup DIRTY; a skipped lane is reported, not judged.
func TestReceiversLaneFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		set   func(*harness, *fakeRx)
		skip  bool
		check func(*testing.T, *harness, *fakeRx, score.Scorecard)
	}{
		{"smtp lost", func(_ *harness, f *fakeRx) { f.dropSMTP = true }, false, func(t *testing.T, _ *harness, _ *fakeRx, c score.Scorecard) {
			if c.Verdict != domain.VerdictFail || !hasReason(c.Reasons, "receivers smtp: ") || c.Receivers.Receivers["smtp"].Lost == 0 {
				t.Errorf("verdict %s reasons %v", c.Verdict, c.Reasons)
			}
		}},
		{"webhook refused", func(_ *harness, f *fakeRx) { f.webhookErr = errors.New("webhook HTTP 502") }, false, func(t *testing.T, _ *harness, _ *fakeRx, c score.Scorecard) {
			if c.Verdict != domain.VerdictFail || c.Receivers.Receivers["webhook"].SendErrors == 0 {
				t.Errorf("verdict %s reasons %v", c.Verdict, c.Reasons)
			}
		}},
		{"install fails", func(_ *harness, f *fakeRx) { f.failCreate = true }, false, func(t *testing.T, h *harness, f *fakeRx, c score.Scorecard) {
			if c.Verdict == domain.VerdictPass || !hasReason(c.Reasons, "receivers: setup: create") || len(c.Receivers.Probes) != 0 || f.sends["trap"] != 0 {
				t.Errorf("verdict %s reasons %v", c.Verdict, c.Reasons)
			}
			if hc := h.campaign(); hc.Cleanup != domain.CleanupClean {
				t.Errorf("cleanup %s after a failed install", hc.Cleanup)
			}
		}},
		{"mapping stays", func(_ *harness, f *fakeRx) { f.keepAfter = true }, false, func(t *testing.T, h *harness, _ *fakeRx, c score.Scorecard) {
			if hc := h.campaign(); hc.Cleanup == domain.CleanupClean || c.Receivers.Removed {
				t.Errorf("cleanup %s removed %v", hc.Cleanup, c.Receivers.Removed)
			}
		}},
		{"skipped", func(*harness, *fakeRx) {}, true, func(t *testing.T, h *harness, f *fakeRx, c score.Scorecard) {
			if c.Receivers.Skipped == "" || len(f.sends) != 0 || hasReason(c.Reasons, "receivers") || h.campaign().Comparable {
				t.Errorf("skipped lane: %+v sends %v reasons %v", c.Receivers, f.sends, c.Reasons)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, f := rxHarness(t)
				tc.set(h, f)
				opts := runtime.CampaignOptions{Scenario: h.s, Trigger: "cli", RunBy: "agent"}
				if tc.skip {
					opts.SkipLanes = []string{receivers.Lane}
				}
				if err := New(h.deps, h.clock).Run(context.Background(), h.id, opts); err != nil {
					t.Fatal(err)
				}
				tc.check(t, h, f, h.rxCard())
			})
		})
	}
}

// TestTrapWithoutMappingIsWrong: the row an unmapped trap leaves is judged
// wrong (the sender's address, no Source host).
func TestTrapWithoutMappingIsWrong(t *testing.T) {
	f := newFakeRx(fixedClock{time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)})
	p := receivers.Probe{Labels: score.ReceiverLabels("camp_x", 35), StartsAt: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
	p.Labels["alertname"], p.Labels["severity"] = "A", "warning"
	_ = f.Send(context.Background(), "trap", "", p)
	if got := score.CheckReceiverRow("camp_x", score.ReceiverProbe{Server: 35}, f.rows[0]); !strings.Contains(got, "instance") || !strings.Contains(got, "Source") {
		t.Errorf("unmapped trap row judged %q", got)
	}
}

// TestPrecheckReceivers (AC-7): leftovers, an enterprise mapping and
// unreachable receivers block B17.
func TestPrecheckReceivers(t *testing.T) {
	f := newFakeRx(fixedClock{})
	if r := precheckReceivers(context.Background(), f); r.Status != precheck.Pass || r.ID != "B17" {
		t.Errorf("clean: %+v", r)
	}
	f.mappings[key(receivers.ProfileNamespace, "ct-rx-old")] = nil
	f.mappings[key(receivers.TrapNamespace, receivers.TrapEnterpriseOID)] = nil
	r := precheckReceivers(context.Background(), f)
	if r.Status != precheck.Fail || !strings.Contains(fmt.Sprint(r), "ct-rx-old") || !strings.Contains(fmt.Sprint(r), receivers.TrapEnterpriseOID) {
		t.Errorf("leftovers: %+v", r)
	}
}

// TestCorrServerSeeded (AC-1, code review F2): a document's server depends
// only on the seed and the document, and the documents spread over servers.
func TestCorrServerSeeded(t *testing.T) {
	seen := map[int]bool{}
	for _, ref := range scenario.AlertingDay3psV3().Correlation.SetA {
		a, b := corrServer(20261113, ref, "setA", 8), corrServer(20261113, ref, "setA", 8)
		if a != b || a < 0 || a >= 8 {
			t.Fatalf("%v: %d %d", ref, a, b)
		}
		seen[a] = true
	}
	if len(seen) < 5 {
		t.Errorf("set A uses only %d of 8 servers", len(seen))
	}
}

// TestServerLabelsOnCorrelationDocs (AC-1): every case of a document gets
// the server's labels; its own labels win.
func TestServerLabelsOnCorrelationDocs(t *testing.T) {
	doc := corr.Builtin("flood")
	serverLabels(&doc, 35)
	for _, c := range doc.Cases {
		want := lane.ServerLabels(35)
		want["site"] = c.Labels["site"]
		if !reflect.DeepEqual(c.Labels, want) || c.Labels["site"] == "" {
			t.Errorf("case %s labels %v", c.Name, c.Labels)
		}
	}
}
