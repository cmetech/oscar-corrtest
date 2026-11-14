package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

const apiToken = "correct horse battery staple"

// fakeDataSourceForCampaign embeds the minimal DataSource and records campaign calls.
type fakeDataSourceForCampaign struct {
	DataSource
	startErr error
	started  []appruntime.CampaignOptions
	stopped  []string
	lanes    []workerapi.LaneSpec
}

func (f *fakeDataSourceForCampaign) ReadyStatus() (bool, string) { return true, "" }
func (f *fakeDataSourceForCampaign) StartCampaign(_ context.Context, o appruntime.CampaignOptions) (domain.Campaign, error) {
	if f.startErr != nil {
		return domain.Campaign{}, f.startErr
	}
	f.started = append(f.started, o)
	return domain.Campaign{ID: "camp_1", ScenarioID: o.Scenario.ID, TriggerIdentity: o.TriggerIdentity, Status: domain.CampaignPrecheck}, nil
}
func (f *fakeDataSourceForCampaign) StopCampaign(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}
func (f *fakeDataSourceForCampaign) CampaignStatus(_ context.Context, id string) (appruntime.CampaignView, error) {
	if id != "camp_1" {
		return appruntime.CampaignView{}, appruntime.ErrCampaignNotFound
	}
	status := domain.CampaignPrecheck
	if len(f.stopped) > 0 {
		status = domain.CampaignInterrupted
	}
	return appruntime.CampaignView{Campaign: domain.Campaign{ID: id, Status: status}}, nil
}
func (f *fakeDataSourceForCampaign) ListCampaigns(context.Context, domain.CampaignFilter) ([]domain.Campaign, error) {
	return []domain.Campaign{{ID: "camp_1"}}, nil
}

// fakeWorker makes the same fake also a campaignWorker.
type fakeWorkerData struct{ fakeDataSourceForCampaign }

func (f *fakeWorkerData) Info(context.Context) workerapi.Info {
	return workerapi.Info{Version: "v9", Now: time.Date(2026, 11, 13, 1, 2, 3, 456e6, time.UTC)}
}
func (f *fakeWorkerData) Start(_ context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error) {
	f.lanes = append(f.lanes, s)
	return workerapi.LaneStatus{ID: s.ID, State: "running"}, nil
}
func (f *fakeWorkerData) Heartbeat(_ context.Context, id string) (workerapi.LaneStatus, error) {
	return workerapi.LaneStatus{ID: id, State: "running"}, nil
}
func (f *fakeWorkerData) Stop(_ context.Context, id string) (workerapi.LaneStatus, error) {
	return workerapi.LaneStatus{ID: id, State: "stopped"}, nil
}
func (f *fakeWorkerData) ResolveAll(context.Context, string) (int, error) { return 3, nil }
func (f *fakeWorkerData) Status(_ context.Context, id string) (workerapi.LaneStatus, error) {
	if id == "nope" {
		return workerapi.LaneStatus{}, workerapi.ErrLaneNotFound
	}
	return workerapi.LaneStatus{ID: id, State: "running"}, nil
}
func (f *fakeWorkerData) Ledger(_ context.Context, id string, w io.Writer) error {
	if id == "fail" {
		_, _ = io.WriteString(w, "{\"seq\":1}\n")
		return errors.New("disk gone")
	}
	_, err := io.WriteString(w, "{\"seq\":1}\n")
	return err
}

func campaignServer(t *testing.T, data DataSource, workerMode bool) http.Handler {
	t.Helper()
	h, err := NewHandlerWithCampaign(version.Info{}, data, Security{Mode: SecurityBearer, BearerToken: []byte(apiToken), SecureCookies: true}, workerMode)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func apiDo(h http.Handler, method, path, body string, header bool, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://corrtest.example"+path, strings.NewReader(body))
	if header {
		r.Header.Set("Authorization", "Bearer "+apiToken)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func sessionCookie(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "https://corrtest.example/login", strings.NewReader("token="+strings.ReplaceAll(apiToken, " ", "+")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://corrtest.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == "corrtest_session" {
			return c
		}
	}
	t.Fatalf("no session cookie; login status=%d", w.Code)
	return nil
}

func TestCampaignAPIRequiresBearerHeader(t *testing.T) {
	t.Parallel()
	data := &fakeDataSourceForCampaign{}
	h := campaignServer(t, data, false)
	cookie := sessionCookie(t, h)
	body := `{"scenario":"alerting-day@v1"}`
	if w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", body, false, cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only status=%d", w.Code)
	}
	if len(data.started) != 0 {
		t.Fatal("cookie-only request started a campaign")
	}
	w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", body, true, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("header status=%d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"apiVersion":"corrtest.oscar/v1alpha1"`) {
		t.Fatalf("envelope: %s", w.Body)
	}
	// Reads work with the cookie.
	if w := apiDo(h, http.MethodGet, "/api/v1/campaign/runs", "", false, cookie); w.Code != http.StatusOK {
		t.Fatalf("cookie GET status=%d", w.Code)
	}
}

func TestCampaignAPIValidation(t *testing.T) {
	t.Parallel()
	data := &fakeDataSourceForCampaign{}
	h := campaignServer(t, data, false)
	for _, body := range []string{`{"scenario":"nope@v1"}`, `{"scenario":"alerting-day@v1","extra":1}`, `not json`} {
		if w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", body, true, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s status=%d", body, w.Code)
		}
	}
	w := apiDo(h, http.MethodPost, "/api/v1/campaign/prechecks", `{"scenario":"alerting-day@v1"}`, true, nil)
	if w.Code != http.StatusAccepted || len(data.started) != 1 || !data.started[0].PrecheckOnly {
		t.Fatalf("prechecks status=%d started=%+v", w.Code, data.started)
	}
	if w := apiDo(h, http.MethodGet, "/api/v1/campaign/runs/camp_1", "", true, nil); w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
	if w := apiDo(h, http.MethodGet, "/api/v1/campaign/runs/zzz", "", true, nil); w.Code != http.StatusNotFound {
		t.Errorf("missing status=%d", w.Code)
	}
	if w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs/camp_1/stop", "", true, nil); w.Code != http.StatusOK || len(data.stopped) != 1 || !strings.Contains(w.Body.String(), `"status":"INTERRUPTED"`) {
		t.Errorf("stop status=%d %s", w.Code, w.Body)
	}
	if w := apiDo(h, http.MethodGet, "/api/v1/campaign/compare?ids=a", "", true, nil); w.Code != http.StatusBadRequest {
		t.Errorf("compare status=%d", w.Code)
	}
	if w := apiDo(h, http.MethodGet, "/api/v1/campaign/runs/camp_1/bundle", "", true, nil); w.Code != http.StatusNotFound {
		t.Errorf("bundle status=%d", w.Code)
	}
}

func TestWorkerRoutesAbsentUnlessWorkerMode(t *testing.T) {
	t.Parallel()
	// Implements campaignWorker but flag off: 404.
	if w := apiDo(campaignServer(t, &fakeWorkerData{}, false), http.MethodGet, "/api/v1/campaign-worker/info", "", true, nil); w.Code != http.StatusNotFound {
		t.Fatalf("flag off status=%d", w.Code)
	}
	// Flag on but data source is not a worker: 404.
	if w := apiDo(campaignServer(t, &fakeDataSourceForCampaign{}, true), http.MethodGet, "/api/v1/campaign-worker/info", "", true, nil); w.Code != http.StatusNotFound {
		t.Fatalf("not worker status=%d", w.Code)
	}
	data := &fakeWorkerData{}
	h := campaignServer(t, data, true)
	w := apiDo(h, http.MethodGet, "/api/v1/campaign-worker/info", "", true, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"now":"2026-11-13T01:02:03.456Z"`) {
		t.Fatalf("info status=%d %s", w.Code, w.Body)
	}
	if w := apiDo(h, http.MethodPost, "/api/v1/campaign-worker/lanes", `{"id":"l1","campaignId":"c","kind":"baseline"}`, true, nil); w.Code != http.StatusAccepted || len(data.lanes) != 1 {
		t.Fatalf("start status=%d", w.Code)
	}
	if w := apiDo(h, http.MethodGet, "/api/v1/campaign-worker/lanes/nope", "", true, nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing lane status=%d", w.Code)
	}
	for _, p := range []string{"heartbeat", "stop", "resolve-all"} {
		if w := apiDo(h, http.MethodPost, "/api/v1/campaign-worker/lanes/l1/"+p, "", true, nil); w.Code != http.StatusOK {
			t.Errorf("%s status=%d %s", p, w.Code, w.Body)
		}
	}
	w = apiDo(h, http.MethodGet, "/api/v1/campaign-worker/lanes/l1/ledger", "", true, nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/x-ndjson" || w.Body.String() != "{\"seq\":1}\n{\"type\":\"end\",\"ok\":true}\n" {
		t.Fatalf("ledger status=%d ct=%s", w.Code, w.Header().Get("Content-Type"))
	}
	w = apiDo(h, http.MethodGet, "/api/v1/campaign-worker/lanes/fail/ledger", "", true, nil)
	if want := "{\"seq\":1}\n{\"type\":\"end\",\"ok\":false,\"error\":\"disk gone\"}\n"; w.Body.String() != want {
		t.Fatalf("mid-stream failure body=%q", w.Body.String())
	}
	if w := apiDo(h, http.MethodPost, "/api/v1/campaign-worker/lanes/l1/stop", "", false, sessionCookie(t, h)); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie worker POST status=%d", w.Code)
	}
}

func TestStartReturns409WhenActive(t *testing.T) {
	t.Parallel()
	h := campaignServer(t, &fakeDataSourceForCampaign{startErr: appruntime.ErrCampaignActive}, false)
	w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", `{"scenario":"alerting-day@v1"}`, true, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"campaign_active"`) {
		t.Fatalf("status=%d %s", w.Code, w.Body)
	}
}

func TestTriggerIdentityRecorded(t *testing.T) {
	t.Parallel()
	data := &fakeDataSourceForCampaign{}
	h := campaignServer(t, data, false)
	w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", `{"scenario":"alerting-day@v1","acknowledged":["adv1"]}`, true, nil)
	sum := sha256.Sum256([]byte(apiToken))
	want := "bearer:" + hex.EncodeToString(sum[:])[:8]
	if len(data.started) != 1 || data.started[0].TriggerIdentity != want || data.started[0].Trigger != "cli" {
		t.Fatalf("started=%+v want identity %s", data.started, want)
	}
	if strings.Contains(w.Body.String(), apiToken) {
		t.Fatal("token leaked in response")
	}
}

func TestCampaignPostRejectedWithoutBearerMode(t *testing.T) {
	t.Parallel()
	_, network, _ := net.ParseCIDR("192.0.2.0/24")
	for name, sec := range map[string]Security{
		"none":          {},
		"trusted-proxy": {Mode: SecurityTrustedProxy, ProxyHeader: "X-User", ProxyValue: "corey", TrustedProxies: []*net.IPNet{network}},
	} {
		data := &fakeDataSourceForCampaign{}
		h, err := NewHandlerWithOptions(version.Info{}, data, sec)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "https://corrtest.example/api/v1/campaign/runs", strings.NewReader(`{"scenario":"alerting-day@v1"}`))
		r.Header.Set("X-User", "corey")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || len(data.started) != 0 {
			t.Errorf("%s: status=%d started=%d", name, w.Code, len(data.started))
		}
	}
}

func TestCampaignAPIBodyLimitsAndStrictFields(t *testing.T) {
	t.Parallel()
	h := campaignServer(t, &fakeDataSourceForCampaign{}, false)
	big := `{"scenario":"alerting-day@v1","acknowledged":["` + strings.Repeat("a", 1<<20+1) + `"]}`
	if w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", big, true, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize status=%d", w.Code)
	}
	if w := apiDo(h, http.MethodPost, "/api/v1/campaign/runs", `{"scenario":"alerting-day@v1","triggerIdentity":"bearer:evil"}`, true, nil); w.Code != http.StatusBadRequest {
		t.Errorf("triggerIdentity status=%d", w.Code)
	}
}
