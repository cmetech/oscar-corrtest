package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runtime"
)

// fakeMaint is OSCAR's maintenance API as the fake target sees it: patterns,
// templates and one window per CR whose rule suppresses the alerthistory
// handler for matching alerts while now is inside its bounds (rounded to
// whole seconds, as taskmanager's DATETIME columns store them).
type fakeMaint struct {
	mu        sync.Mutex
	clock     interface{ Now() time.Time }
	patterns  map[string]targetops.MaintPattern
	templates map[string]bool
	windows   map[string]*fakeWindow
	records   []targetops.MaintRecord
	// Behaviour switches.
	alwaysApply bool // an always-applied pattern exists and joins every window
	noSuppress  bool // the window's rule never suppresses
	keepExpired bool // disable answers 200 but leaves the window
	denied      bool // every call answers 403
	calls       []string
}

type fakeWindow struct {
	start, end time.Time
	value      string // the pattern's campaign_maint value
	present    bool
}

func newFakeMaint(clock interface{ Now() time.Time }) *fakeMaint {
	return &fakeMaint{clock: clock, patterns: map[string]targetops.MaintPattern{}, templates: map[string]bool{}, windows: map[string]*fakeWindow{}}
}

func (f *fakeMaint) deny(call string) error {
	f.calls = append(f.calls, call)
	if f.denied {
		return &targetops.StatusError{Status: http.StatusForbidden, Body: "missing manage:maintenance-windows"}
	}
	return nil
}

func (f *fakeMaint) MaintPatterns(context.Context) ([]targetops.MaintPattern, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.deny("list patterns"); err != nil {
		return nil, err
	}
	var out []targetops.MaintPattern
	if f.alwaysApply {
		out = append(out, targetops.MaintPattern{Key: "maint-mode-rules", AlwaysApply: true})
	}
	for _, p := range f.patterns {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeMaint) MaintTemplates(context.Context) ([]targetops.MaintTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []targetops.MaintTemplate
	for k := range f.templates {
		out = append(out, targetops.MaintTemplate{Key: k})
	}
	return out, nil
}

var maintSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (f *fakeMaint) CreateMaintPattern(_ context.Context, p targetops.MaintPattern) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.deny("create pattern " + p.Key); err != nil {
		return err
	}
	if !maintSlug.MatchString(p.Key) {
		return &targetops.StatusError{Status: 422, Body: "invalid key"}
	}
	f.patterns[p.Key] = p
	return nil
}

func (f *fakeMaint) CreateMaintTemplate(_ context.Context, t targetops.MaintTemplate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "create template "+t.Key)
	if !maintSlug.MatchString(t.Key) {
		return &targetops.StatusError{Status: 422, Body: "invalid key"}
	}
	f.templates[t.Key] = true
	return nil
}

func (f *fakeMaint) DeleteMaintPattern(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.deny("delete pattern " + key); err != nil {
		return err
	}
	delete(f.patterns, key)
	return nil
}

func (f *fakeMaint) DeleteMaintTemplate(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "delete template "+key)
	delete(f.templates, key)
	return nil
}

func (f *fakeMaint) EnableMaint(_ context.Context, w targetops.MaintWindow) (targetops.MaintEnabled, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "enable "+w.CR)
	if w.Start.Nanosecond() != 0 || w.End.Nanosecond() != 0 {
		return targetops.MaintEnabled{}, &targetops.StatusError{Status: 400, Body: "test: bounds must be whole seconds"}
	}
	p := f.patterns[w.Patterns[0]]
	f.windows[w.CR] = &fakeWindow{start: w.Start.Round(time.Second), end: w.End.Round(time.Second), value: p.Match[0].Value, present: true}
	f.records = append(f.records, targetops.MaintRecord{ID: "w-" + w.CR, CR: w.CR})
	applied := append([]string(nil), w.Patterns...)
	if f.alwaysApply {
		applied = append(applied, "maint-mode-rules")
	}
	return targetops.MaintEnabled{CR: w.CR, Status: "enabled", Patterns: applied, Handlers: w.Handlers}, nil
}

func (f *fakeMaint) DisableMaint(_ context.Context, cr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "disable "+cr)
	if w := f.windows[cr]; w != nil && !f.keepExpired {
		w.present = false
	}
	return nil
}

func (f *fakeMaint) state(w *fakeWindow) string {
	now := f.clock.Now()
	switch {
	case !w.present:
		return ""
	case now.Before(w.start):
		return "scheduled"
	case !now.After(w.end):
		return "active"
	}
	return "expired"
}

func (f *fakeMaint) MaintStatus(_ context.Context, cr string) (targetops.MaintStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.windows[cr]
	if w == nil || !w.present {
		return targetops.MaintStatus{}, nil
	}
	start, end := w.start, w.end
	return targetops.MaintStatus{Active: f.state(w) == "active", State: f.state(w), SuppressionID: "s-" + cr, Start: &start, End: &end}, nil
}

func (f *fakeMaint) MaintRecords(_ context.Context, q string) ([]targetops.MaintRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []targetops.MaintRecord
	for _, r := range f.records {
		if strings.Contains(r.CR, q) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeMaint) MaintLive(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w := f.windows[strings.TrimPrefix(id, "w-")]; w != nil && w.present {
		return "present", nil
	}
	return "absent", nil
}

// suppress is the alerthistory rule the fake target applies to new rows.
func (f *fakeMaint) suppress(labels map[string]string, at time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for cr, w := range f.windows {
		if w.present && !f.noSuppress && labels[maintLabel] == w.value && !at.Before(w.start) && !at.After(w.end) {
			return cr
		}
	}
	return ""
}

// maintHarness is the v4 harness with a maintenance lane: setup at T+30s,
// a 60 s window from T+60s, probes every 5 s.
func maintHarness(t *testing.T) (*harness, *fakeMaint) {
	h, _ := v4Harness(t)
	h.s.Maintenance = &scenario.MaintenanceSpec{At: 30 * time.Second, Start: 60 * time.Second, For: 60 * time.Second, Every: 5 * time.Second, Lag: 10 * time.Second}
	h.oscar.s = h.s
	m := newFakeMaint(h.clock)
	h.deps.Maintenance = m
	h.oscar.maintSuppress = m.suppress
	return h, m
}

func (h *harness) maintCard() score.Scorecard {
	h.t.Helper()
	var card score.Scorecard
	if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil || card.Maintenance == nil {
		h.t.Fatalf("no Maintenance section: %v", err)
	}
	return card
}

// TestMaintenanceLane (OSC-189 AC-5, AC-7, AC-9): a target that suppresses
// matching alerts in the window passes every gate; the window, template and
// pattern are removed and read back; cleanup stays CLEAN.
func TestMaintenanceLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, m := maintHarness(t)
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		card := h.maintCard()
		mt := card.Maintenance
		for _, r := range card.Reasons {
			if strings.HasPrefix(r, "maintenance") {
				t.Errorf("unexpected reason: %s", r)
			}
		}
		kinds := map[string]int{}
		for _, p := range mt.Probes {
			kinds[p.Kind]++
			if !p.Found {
				t.Errorf("probe %s has no row", p.Name)
			}
		}
		if kinds[score.MaintInside] < 8 || kinds[score.MaintControl] != kinds[score.MaintInside] || kinds[score.MaintAfter] < 10 || mt.Posted != len(mt.Probes) {
			t.Errorf("probes %v posted %d", kinds, mt.Posted)
		}
		if len(mt.Edges) != 3 || mt.Edges[0].Got != "scheduled" || mt.Edges[1].Got != "active" || mt.Edges[2].Got != "expired" || !mt.Removed {
			t.Errorf("edges %+v removed %v", mt.Edges, mt.Removed)
		}
		if !maintSlug.MatchString(mt.CR) || mt.Start.Nanosecond() != 0 {
			t.Errorf("CR %q start %s", mt.CR, mt.Start)
		}
		if len(m.patterns) != 0 || len(m.templates) != 0 || m.windows[mt.CR].present {
			t.Errorf("left behind: patterns %v templates %v window %+v", m.patterns, m.templates, m.windows[mt.CR])
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupClean {
			t.Errorf("cleanup %s", c.Cleanup)
		}
		for _, o := range card.Offered.PerMinute {
			if o.Lanes[maintLane] > 0 {
				t.Errorf("maintenance probes counted as offered load: %+v", o)
			}
		}
	})
}

// TestMaintenanceLaneFailures (OSC-189 AC-5, AC-6, AC-7): a window that does
// not suppress FAILs (and its inside probes get tickets); an always-applied
// pattern in the enable answer FAILs and the window is disabled before it
// starts; a disable that leaves the expired window present is not restored.
func TestMaintenanceLaneFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		set   func(*fakeMaint)
		check func(*testing.T, *harness, *fakeMaint, score.Scorecard)
	}{
		{"not suppressed", func(m *fakeMaint) { m.noSuppress = true }, func(t *testing.T, h *harness, _ *fakeMaint, card score.Scorecard) {
			if card.Verdict != domain.VerdictFail || !hasReason(card.Reasons, "maintenance: Campaign-") || !hasReason(card.Reasons, "maintenance: ticketed inside") {
				t.Errorf("verdict %s reasons %v", card.Verdict, card.Reasons)
			}
		}},
		{"always applied", func(m *fakeMaint) { m.alwaysApply = true }, func(t *testing.T, h *harness, m *fakeMaint, card score.Scorecard) {
			if card.Verdict != domain.VerdictFail || !hasReason(card.Reasons, "maintenance: the window applied") || len(card.Maintenance.Probes) != 0 {
				t.Errorf("verdict %s reasons %v probes %d", card.Verdict, card.Reasons, len(card.Maintenance.Probes))
			}
			if w := m.windows[card.Maintenance.CR]; w == nil || w.present {
				t.Errorf("window not disabled: %+v", w)
			}
		}},
		{"expired window left", func(m *fakeMaint) { m.keepExpired = true }, func(t *testing.T, h *harness, _ *fakeMaint, card score.Scorecard) {
			if c := h.campaign(); c.Cleanup == domain.CleanupClean || card.Maintenance.Removed {
				t.Errorf("cleanup %s removed %v: an expired window still holds its rules", c.Cleanup, card.Maintenance.Removed)
			}
		}},
		{"denied", func(m *fakeMaint) { m.denied = true }, func(t *testing.T, h *harness, _ *fakeMaint, card score.Scorecard) {
			if card.Verdict == domain.VerdictPass || !hasReason(card.Reasons, "maintenance: lane did not run: create pattern") {
				t.Errorf("verdict %s reasons %v", card.Verdict, card.Reasons)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, m := maintHarness(t)
				tc.set(m)
				if err := h.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				tc.check(t, h, m, h.maintCard())
			})
		})
	}
}

// TestMaintenanceSkipLane (OSC-189 AC-8): --skip-lane maintenance makes no
// maintenance call and reads skipped (by option), never a pass.
func TestMaintenanceSkipLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, m := maintHarness(t)
		pctx := context.WithValue(context.Background(), skipLanesKey{}, []string{"maintenance"})
		var b16 *precheck.Result
		for _, r := range v4Prechecks(pctx, h.deps, h.s) {
			if r.ID == "B16" {
				b16 = &r
			}
		}
		if b16 == nil || b16.Status != precheck.Skipped {
			t.Fatalf("B16 with the lane skipped: %+v", b16)
		}
		if err := New(h.deps, h.clock).Run(context.Background(), h.id, runtime.CampaignOptions{Scenario: h.s, Trigger: "cli", RunBy: "agent", SkipLanes: []string{"maintenance"}}); err != nil {
			t.Fatal(err)
		}
		card := h.maintCard()
		if len(m.calls) != 0 || card.Maintenance.Skipped != laneSkipped || !hasReason(card.SkippedLanes, "maintenance: skipped") {
			t.Errorf("calls %v skipped %q lanes %v", m.calls, card.Maintenance.Skipped, card.SkippedLanes)
		}
	})
}

// TestMaintenancePrecheck (OSC-189 AC-8): B16 passes on a clean target with
// manage:; names manage:maintenance-windows when denied; blocks on an
// always-applied pattern and on a leftover expired campaign window.
func TestMaintenancePrecheck(t *testing.T) {
	t.Parallel()
	clock := newClock()
	for _, tc := range []struct {
		name string
		set  func(*fakeMaint)
		want string
	}{
		{"clean", func(*fakeMaint) {}, ""},
		{"denied", func(m *fakeMaint) { m.denied = true }, "needs manage:maintenance-windows"},
		{"always applied", func(m *fakeMaint) { m.alwaysApply = true }, `always-applied maintenance pattern "maint-mode-rules"`},
		{"leftover window", func(m *fakeMaint) {
			m.windows["ct-maint-old"] = &fakeWindow{start: clock.Now().Add(-time.Hour), end: clock.Now().Add(-55 * time.Minute), present: true}
			m.records = append(m.records, targetops.MaintRecord{ID: "w-ct-maint-old", CR: "ct-maint-old"})
		}, "leftover campaign maintenance window: window ct-maint-old still present (expired)"},
		{"leftover pattern", func(m *fakeMaint) { m.patterns["ct-maint-old"] = targetops.MaintPattern{Key: "ct-maint-old"} }, "leftover campaign maintenance pattern ct-maint-old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newFakeMaint(clock)
			tc.set(m)
			res := precheck.MaintenanceReady(precheckMaint(context.Background(), m))
			if tc.want == "" && res.Status != precheck.Pass || tc.want != "" && (res.Status != precheck.Fail || !strings.Contains(res.Detail, tc.want)) {
				t.Errorf("%+v, want %q", res, tc.want)
			}
			for k := range m.patterns {
				if strings.HasPrefix(k, maintPrefix+"pre-") {
					t.Errorf("probe pattern %s left behind", k)
				}
			}
		})
	}
}

// TestMaintKeys (plan review F3): every maintenance key is a valid OSCAR slug.
func TestMaintKeys(t *testing.T) {
	if got := maintToken("camp_ed4aa0e5fd4ccf94"); got != "ed4aa0e5fd4ccf94" {
		t.Errorf("token %q", got)
	}
	r, long := &run{id: "camp_ed4aa0e5fd4ccf94"}, &run{id: "camp_Test_Some/Very_Long_Name_" + strings.Repeat("x", 80)}
	for _, k := range []string{r.maintKey(), r.maintKey() + "-ctl", long.maintKey(), long.maintKey() + "-ctl", maintPrefix + "pre-0123abcd"} {
		if !maintSlug.MatchString(k) {
			t.Errorf("%q is not a maintenance key", k)
		}
	}
}
