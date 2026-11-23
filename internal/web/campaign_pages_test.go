package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	campaignscenario "github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

type pageData2 struct {
	fakeDataSourceForCampaign
	campaigns map[string]domain.Campaign
	options   map[string]*domain.CampaignRecordedOptions
	statusIDs []string
	listed    bool
	checks    []precheck.Result
}

func (f *pageData2) CampaignStatus(_ context.Context, id string) (appruntime.CampaignView, error) {
	f.statusIDs = append(f.statusIDs, id)
	if id == "pc" || id == "pc-smoke" || id == "pc-control" {
		d, _ := json.Marshal(map[string]any{"results": f.checks})
		s := campaignscenario.AlertingDayV1()
		switch id {
		case "pc-smoke":
			s = campaignscenario.AlertingDaySmokeV1()
		case "pc-control":
			s = campaignscenario.AlertingDayControlV1()
		}
		return appruntime.CampaignView{Scenario: &s, Campaign: domain.Campaign{ID: id, Status: domain.CampaignCompleted, ScenarioID: s.ID, ScenarioHash: s.Hash()},
			Events: []domain.CampaignEvent{{Sequence: 1, Type: precheckEventType, Detail: d}}}, nil
	}
	if c, ok := f.campaigns[id]; ok {
		s, _ := campaignscenario.ByHash(c.ScenarioHash)
		return appruntime.CampaignView{Scenario: &s, Campaign: c, Options: f.options[id]}, nil
	}
	return appruntime.CampaignView{}, appruntime.ErrCampaignNotFound
}
func (f *pageData2) ListCampaigns(context.Context, domain.CampaignFilter) ([]domain.Campaign, error) {
	f.listed = true
	out := []domain.Campaign{{ID: "pc", StartedAt: time.Now()}}
	for _, c := range f.campaigns {
		out = append(out, c)
	}
	return out, nil
}

// csrfPost fetches /campaigns for a CSRF cookie+token, then posts the form.
func csrfPost(t *testing.T, h http.Handler, path string, form url.Values, withToken bool) *httptest.ResponseRecorder {
	t.Helper()
	g := httptest.NewRecorder()
	h.ServeHTTP(g, httptest.NewRequest(http.MethodGet, "/campaigns", nil))
	cookies := g.Result().Cookies()
	body := g.Body.String()
	i := strings.Index(body, `name="csrf_token" value="`)
	if i < 0 {
		t.Fatalf("no csrf token in page: %s", body)
	}
	tok := strings.SplitN(body[i+len(`name="csrf_token" value="`):], `"`, 2)[0]
	if withToken {
		form.Set("csrf_token", tok)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func newPagesHandler(f *pageData2) http.Handler {
	return NewHandlerWithData(version.Info{Version: "test"}, f)
}

func TestStartFormRequiresCSRFAndSameOrigin(t *testing.T) {
	f := &pageData2{checks: []precheck.Result{{ID: "B1", Tier: precheck.Blocking, Status: precheck.Pass}}}
	h := newPagesHandler(f)
	if w := csrfPost(t, h, "/campaigns/start", url.Values{"precheck": {"pc"}}, false); w.Code != http.StatusForbidden {
		t.Fatalf("no token status=%d", w.Code)
	}
	if w := csrfPost(t, h, "/campaigns/start", url.Values{"precheck": {"pc"}}, true); w.Code != http.StatusSeeOther || len(f.started) != 1 {
		t.Fatalf("valid start status=%d started=%d", w.Code, len(f.started))
	} else if o := f.started[0]; o.Trigger != "ui" || o.RunBy != "human" || !strings.HasPrefix(o.TriggerIdentity, "session:") {
		t.Fatalf("options=%+v", o)
	}
}

func TestStartWithUnacknowledgedAdvisoryRerendersWithError(t *testing.T) {
	f := &pageData2{checks: []precheck.Result{{ID: "A1", Tier: precheck.Advisory, Status: precheck.Warn}}}
	h := newPagesHandler(f)
	w := csrfPost(t, h, "/campaigns/start", url.Values{"precheck": {"pc"}, "ack_A1": {"yes"}}, true)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Type A1") || len(f.started) != 0 {
		t.Fatalf("status=%d started=%d", w.Code, len(f.started))
	}
	w = csrfPost(t, h, "/campaigns/start", url.Values{"precheck": {"pc"}, "ack_A1": {"A1"}}, true)
	if w.Code != http.StatusSeeOther || len(f.started) != 1 || len(f.started[0].Acknowledged) != 1 {
		t.Fatalf("status=%d started=%v", w.Code, f.started)
	}
}

func TestBlockingFailureDisablesStart(t *testing.T) {
	f := &pageData2{checks: []precheck.Result{{ID: "B2", Tier: precheck.Blocking, Status: precheck.Fail}}}
	w := httptest.NewRecorder()
	newPagesHandler(f).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/campaigns", nil))
	if !strings.Contains(w.Body.String(), "disabled") {
		t.Fatal("Start not disabled")
	}
}

func TestStopButtonPostsStop(t *testing.T) {
	f := &pageData2{campaigns: map[string]domain.Campaign{"c1": {ID: "c1", Status: domain.CampaignRunning}}}
	h := newPagesHandler(f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/campaigns/c1", nil))
	if !strings.Contains(w.Body.String(), `action="/campaigns/c1/stop"`) {
		t.Fatal("no stop form")
	}
	if w := csrfPost(t, h, "/campaigns/c1/stop", url.Values{}, true); w.Code != http.StatusSeeOther || len(f.stopped) != 1 || f.stopped[0] != "c1" {
		t.Fatalf("status=%d stopped=%v", w.Code, f.stopped)
	}
}

func cardJSON(hash, runBy string, comparable bool, v float64) json.RawMessage {
	b, _ := json.Marshal(score.Scorecard{ObserverConfig: "fixture", ScenarioHash: hash, RunBy: runBy, Comparable: comparable, CleanupClean: true, Verdict: "PASS",
		Headlines: []score.Headline{{Name: "queue_peak", Value: v, Pass: true}}})
	return b
}

func comparisonPageFixture() (*pageData2, url.Values) {
	hash := campaignscenario.AlertingDayControlV1().Hash()
	t0 := time.Unix(1_800_000_000, 0)
	campaign := func(id, by string, at time.Time, value float64) domain.Campaign {
		return domain.Campaign{ID: id, ScenarioHash: hash, Status: domain.CampaignCompleted,
			RunBy: by, Comparable: true, Verdict: domain.VerdictPass, Cleanup: domain.CleanupClean, StartedAt: at, EndedAt: at.Add(3 * time.Hour),
			Scorecard: cardJSON(hash, by, true, value), Report: json.RawMessage(`{"fullLoadCompleted":true}`)}
	}
	opts := func(role domain.CampaignRole, build string) *domain.CampaignRecordedOptions {
		return &domain.CampaignRecordedOptions{
			Provenance: &domain.CampaignProvenance{Role: role, TargetBuild: build, CorrTestBuild: "same-runner", WorkloadConfig: "same-pack"},
		}
	}
	return &pageData2{
		campaigns: map[string]domain.Campaign{
			"unrelated": campaign("unrelated", "agent", t0.Add(-4*time.Hour), 10),
			"before":    campaign("before", "agent", t0, 100),
			"after":     campaign("after", "human", t0.Add(4*time.Hour), 108),
		},
		options: map[string]*domain.CampaignRecordedOptions{
			"unrelated": opts(domain.CampaignPreChangeControl, "older-build"),
			"before":    opts(domain.CampaignPreChangeControl, "old-build"),
			"after":     opts(domain.CampaignPostChangeControl, "new-build"),
		},
	}, url.Values{"ids": {"after"}, "referenceIds": {"before"}, "scenarioHash": {hash}, "mode": {"control_before_after"}}
}

func TestComparePageRendersReferenceProvenance(t *testing.T) {
	f, query := comparisonPageFixture()
	w := httptest.NewRecorder()
	newPagesHandler(f).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/campaigns/compare?"+query.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("compare page status=%d %s", w.Code, w.Body)
	}
	for _, want := range []string{"before", "after", "old-build", "new-build", "human", "provisional", "8.0%"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("compare page missing %q", want)
		}
	}
}

func TestCampaignStartFormForwardsComparisonProvenance(t *testing.T) {
	f := &pageData2{checks: []precheck.Result{{ID: "B1", Tier: precheck.Blocking, Status: precheck.Pass}}}
	form := url.Values{
		"precheck": {"pc-control"}, "role": {"pre_change_control"}, "target_build": {"before-id"},
		"corrtest_build": {"corrtest-id"}, "workload_config": {"same-pack"},
	}
	w := csrfPost(t, newPagesHandler(f), "/campaigns/start", form, true)
	if w.Code != http.StatusSeeOther || len(f.started) != 1 || f.started[0].Provenance == nil ||
		*f.started[0].Provenance != (domain.CampaignProvenance{
			Role: domain.CampaignPreChangeControl, TargetBuild: "before-id", CorrTestBuild: "corrtest-id", WorkloadConfig: "same-pack",
		}) {
		t.Fatalf("start status=%d started=%v", w.Code, f.started)
	}
}

// Ruling C39: the smoke is selectable in the UI. Pre-checks take the chosen
// scenario; Start runs the scenario its pre-check ran.
func TestSmokeSelectableInUI(t *testing.T) {
	f := &pageData2{checks: []precheck.Result{{ID: "B1", Tier: precheck.Blocking, Status: precheck.Pass}}}
	h := newPagesHandler(f)
	g := httptest.NewRecorder()
	h.ServeHTTP(g, httptest.NewRequest(http.MethodGet, "/campaigns", nil))
	if !strings.Contains(g.Body.String(), `value="alerting-day-smoke@v1"`) {
		t.Fatal("the pre-check form offers no smoke scenario")
	}
	if w := csrfPost(t, h, "/campaigns/prechecks", url.Values{"scenario": {"alerting-day-smoke@v1"}}, true); w.Code != http.StatusSeeOther {
		t.Fatalf("smoke pre-check status=%d", w.Code)
	}
	if w := csrfPost(t, h, "/campaigns/prechecks", url.Values{"scenario": {"nope@v9"}}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown scenario status=%d", w.Code)
	}
	if w := csrfPost(t, h, "/campaigns/start", url.Values{"precheck": {"pc-smoke"}}, true); w.Code != http.StatusSeeOther {
		t.Fatalf("smoke start status=%d", w.Code)
	}
	if len(f.started) != 2 || f.started[0].Scenario.ID != "alerting-day-smoke" || f.started[1].Scenario.ID != "alerting-day-smoke" || f.started[1].PrecheckOnly {
		t.Fatalf("started %+v", f.started)
	}
}

// A Scenarios page link (/campaigns?scenario=...) preselects that campaign scenario.
func TestCampaignsPagePreselectsLinkedScenario(t *testing.T) {
	g := httptest.NewRecorder()
	newPagesHandler(&pageData2{}).ServeHTTP(g, httptest.NewRequest(http.MethodGet, "/campaigns?scenario=delivery-retry%40v1", nil))
	if !strings.Contains(g.Body.String(), `<option value="delivery-retry@v1" selected>`) {
		t.Fatal("campaigns page does not preselect the linked scenario")
	}
}
