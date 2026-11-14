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
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

type pageData2 struct {
	fakeDataSourceForCampaign
	campaigns map[string]domain.Campaign
	checks    []precheck.Result
}

func (f *pageData2) CampaignStatus(_ context.Context, id string) (appruntime.CampaignView, error) {
	if id == "pc" {
		d, _ := json.Marshal(map[string]any{"results": f.checks})
		return appruntime.CampaignView{Campaign: domain.Campaign{ID: id, Status: domain.CampaignCompleted},
			Events: []domain.CampaignEvent{{Sequence: 1, Type: precheckEventType, Detail: d}}}, nil
	}
	if c, ok := f.campaigns[id]; ok {
		return appruntime.CampaignView{Campaign: c}, nil
	}
	return appruntime.CampaignView{}, appruntime.ErrCampaignNotFound
}
func (f *pageData2) ListCampaigns(context.Context, domain.CampaignFilter) ([]domain.Campaign, error) {
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
	b, _ := json.Marshal(score.Scorecard{ScenarioHash: hash, RunBy: runBy, Comparable: comparable, CleanupClean: true, Verdict: "PASS",
		Headlines: []score.Headline{{Name: "queue_peak", Value: v, Pass: true}}})
	return b
}

func TestCompareMarksNonComparableRuns(t *testing.T) {
	t0 := time.Now()
	f := &pageData2{campaigns: map[string]domain.Campaign{
		"a": {ID: "a", Status: domain.CampaignCompleted, RunBy: "agent", Comparable: true, StartedAt: t0, Scorecard: cardJSON("h1", "agent", true, 10)},
		"b": {ID: "b", Status: domain.CampaignCompleted, RunBy: "human", Comparable: false, StartedAt: t0.Add(time.Second), Scorecard: cardJSON("h1", "human", false, 15)},
		"c": {ID: "c", Status: domain.CampaignCompleted, RunBy: "agent", Custom: true, Comparable: true, StartedAt: t0.Add(2 * time.Second), Scorecard: cardJSON("h2", "agent", true, 10)},
	}}
	h := newPagesHandler(f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/campaigns/compare?ids=a,b,c", nil))
	body := w.Body.String()
	for _, want := range []string{"not comparable", "custom scenario", "other scenario version", "human"} {
		if !strings.Contains(body, want) {
			t.Errorf("compare page missing %q", want)
		}
	}
	api := apiDo(NewHandlerWithData(version.Info{}, f), http.MethodGet, "/api/v1/campaign/compare?ids=a,b", "", false, nil)
	if api.Code != http.StatusOK {
		t.Fatalf("api status=%d %s", api.Code, api.Body.String())
	}
}
