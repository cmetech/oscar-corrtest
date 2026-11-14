package workerclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerclient"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

const token = "s3cret-worker-token-xyz"

type store struct {
	lane.Ledger
	db *storage.Database
}

func (s store) LaneLedger(ctx context.Context, id string) ([]domain.LaneRow, error) {
	return s.db.LaneLedger(ctx, id)
}

// data serves the real worker routes from a real Manager.
type data struct {
	web.DataSource
	m *worker.Manager
}

func (d data) ReadyStatus() (bool, string)             { return true, "" }
func (d data) Info(ctx context.Context) workerapi.Info { return d.m.Info(ctx) }
func (d data) Start(ctx context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error) {
	return d.m.Start(ctx, s)
}
func (d data) Heartbeat(ctx context.Context, id string) error { return d.m.Heartbeat(ctx, id) }
func (d data) Stop(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	if err := d.m.Stop(ctx, id); err != nil {
		return workerapi.LaneStatus{}, err
	}
	return d.m.Status(ctx, id)
}
func (d data) ResolveAll(ctx context.Context, id string) (int, error) { return d.m.ResolveAll(ctx, id) }
func (d data) Status(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	return d.m.Status(ctx, id)
}
func (d data) Ledger(ctx context.Context, id string, w io.Writer) error {
	return d.m.Ledger(ctx, id, w)
}

func writePEM(t *testing.T, dir, name string, s *httptest.Server) string {
	t.Helper()
	p := filepath.Join(dir, name)
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// setup returns a client for a real handler + manager + sqlite, with a fake OSCAR ingest endpoint.
func setup(t *testing.T) (*workerclient.Client, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	oscar := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(oscar.Close)
	db, err := storage.Open(context.Background(), filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	target := domain.Target{BaseURL: oscar.URL, TLS: domain.TLSPolicy{CAPath: writePEM(t, dir, "oscar.pem", oscar)}}
	m := worker.NewManager(store{Ledger: storage.NewLaneLedger(db), db: db}, target, worker.RealClock{}, time.Minute)
	t.Cleanup(m.Shutdown)
	h, err := web.NewHandlerWithCampaign(version.Info{}, data{m: m}, web.Security{Mode: web.SecurityBearer, BearerToken: []byte(token), SecureCookies: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	tokFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := workerclient.New(srv.URL, tokFile, writePEM(t, dir, "worker.pem", srv))
	if err != nil {
		t.Fatal(err)
	}
	return c, oscar.URL, dir, srv.URL
}

func TestClientRoundTrip(t *testing.T) {
	c, oscarURL, _, _ := setup(t)
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil || info.Now.IsZero() || time.Since(info.Now) > time.Minute {
		t.Fatalf("info %+v err %v", info, err)
	}
	params, _ := json.Marshal(workerapi.BaselineParams{TargetURL: oscarURL, Seed: 1, Duration: time.Second,
		Baseline: scenario.BaselineSpec{Rate: 20, UniqueRatio: 1, PoolSize: 10, Instances: 2,
			Severity: map[string]float64{"critical": 1}, ResolveAfter: time.Hour, TicketEveryNth: 1, TicketTo: time.Hour}})
	spec := workerapi.LaneSpec{ID: "c1-baseline", CampaignID: "c1", Kind: "baseline", Params: params}
	st, err := c.Start(ctx, spec)
	if err != nil || st.ID != spec.ID {
		t.Fatalf("start %+v %v", st, err)
	}
	if _, err := c.Start(ctx, spec); !errors.Is(err, workerapi.ErrLaneExists) || !asCode(err, 409, "lane_exists") {
		t.Fatalf("second start: %v", err)
	}
	if err := c.Heartbeat(ctx, spec.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if st, err = c.Status(ctx, spec.ID); err != nil || st.Sent >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || st.Sent < 3 {
		t.Fatalf("status %+v %v", st, err)
	}
	if st, err = c.Stop(ctx, spec.ID); err != nil || st.State != "stopped" {
		t.Fatalf("stop %+v %v", st, err)
	}
	n, err := c.ResolveAll(ctx, spec.ID)
	if err != nil || n == 0 {
		t.Fatalf("resolve-all %d %v", n, err)
	}
	var buf bytes.Buffer
	if err := c.Ledger(ctx, spec.ID, &buf); err != nil {
		t.Fatal(err)
	}
	ticketed := 0
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var row struct {
			Kind   string `json:"kind"`
			Ticket bool   `json:"ticket"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil || row.Kind == "end" {
			t.Fatalf("bad ledger line %q: %v", line, err)
		}
		if row.Kind == "firing" && row.Ticket {
			ticketed++
		}
	}
	if ticketed == 0 {
		t.Fatalf("no ticketed firing survived the SQLite round trip:\n%s", buf.String())
	}
	if _, err := c.Status(ctx, "nope"); !errors.Is(err, workerapi.ErrLaneNotFound) {
		t.Fatalf("want ErrLaneNotFound, got %v", err)
	}
}

func asCode(err error, status int, code string) bool {
	var ae *workerclient.APIError
	return errors.As(err, &ae) && ae.Status == status && ae.Code == code
}

func TestClientRejectsUntrustedCert(t *testing.T) {
	_, _, dir, srvURL := setup(t)
	// httptest servers all share one built-in cert, so "wrong CA" is modelled by
	// trusting only the system roots (no CA file), which cannot verify the worker.
	bad, err := workerclient.New(srvURL, filepath.Join(dir, "token"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("want x509 error, got %v", err)
	}
}

func TestClientNeverLogsToken(t *testing.T) {
	c, _, dir, srvURL := setup(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer func() { log.SetOutput(os.Stderr); slog.SetDefault(old) }()

	wrong := filepath.Join(dir, "wrong") // a 401 must not echo the token either
	_ = os.WriteFile(wrong, []byte("not-"+token), 0o600)
	badTok, err := workerclient.New(srvURL, wrong, filepath.Join(dir, "worker.pem"))
	if err != nil {
		t.Fatal(err)
	}
	var errs []string
	_, e1 := c.Status(context.Background(), "nope")
	errs = append(errs, e1.Error())
	err = badTok.Heartbeat(context.Background(), "x")
	if !asCode(err, 401, "unauthorized") {
		t.Fatalf("want 401 unauthorized, got %v", err)
	}
	errs = append(errs, err.Error())
	for _, e := range errs {
		if strings.Contains(e, token) || strings.Contains(e, "not-"+token) {
			t.Fatalf("token in error: %s", e)
		}
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("token in logs")
	}
}

func TestLedgerTruncatedAndFailedEnd(t *testing.T) {
	for name, body := range map[string]string{
		"truncated": "{\"seq\":0}\n",
		"partial":   "{\"seq\":0}\n{\"type\":\"end\",\"ok\":tr",
		"failed":    "{\"seq\":0}\n{\"type\":\"end\",\"ok\":false,\"error\":\"boom\"}\n",
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
		dir := t.TempDir()
		tok := filepath.Join(dir, "t")
		_ = os.WriteFile(tok, []byte(token), 0o600)
		c, err := workerclient.New(srv.URL, tok, writePEM(t, dir, "ca.pem", srv))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := c.Ledger(context.Background(), "x", &buf); err == nil {
			t.Fatalf("%s: want error", name)
		}
		srv.Close()
	}
	// ok end: no error, end record not copied
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{\"seq\":0}\n{\"type\":\"end\",\"ok\":true}\n")
	}))
	defer srv.Close()
	dir := t.TempDir()
	tok := filepath.Join(dir, "t")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	c, _ := workerclient.New(srv.URL, tok, writePEM(t, dir, "ca.pem", srv))
	var buf bytes.Buffer
	if err := c.Ledger(context.Background(), "x", &buf); err != nil || buf.String() != "{\"seq\":0}\n" {
		t.Fatalf("%q %v", buf.String(), err)
	}
}

func TestHeartbeatTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	dir := t.TempDir()
	tok := filepath.Join(dir, "t")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	c, _ := workerclient.New(srv.URL, tok, writePEM(t, dir, "ca.pem", srv))
	c.SetTimeouts(200*time.Millisecond, time.Second, time.Second)
	start := time.Now()
	if err := c.Heartbeat(context.Background(), "x"); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("want quick timeout error, got %v after %v", err, time.Since(start))
	}
}

func TestLedgerLineCap(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 70<<10)+"\n")
	}))
	defer srv.Close()
	dir := t.TempDir()
	tok := filepath.Join(dir, "t")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	c, _ := workerclient.New(srv.URL, tok, writePEM(t, dir, "ca.pem", srv))
	if err := c.Ledger(context.Background(), "x", io.Discard); !errors.Is(err, workerclient.ErrLedgerLineTooLong) {
		t.Fatalf("got %v", err)
	}
}

func TestBearerRequiredIsTyped(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"code":"bearer_required","message":"m"}}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	tok := filepath.Join(dir, "t")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	c, _ := workerclient.New(srv.URL, tok, writePEM(t, dir, "ca.pem", srv))
	if err := c.Heartbeat(context.Background(), "x"); !asCode(err, 401, "bearer_required") {
		t.Fatalf("got %v", err)
	}
}

func TestLedgerTotalCap(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 10; i++ {
			_, _ = io.WriteString(w, "{\"seq\":0}\n")
		}
		_, _ = io.WriteString(w, "{\"type\":\"end\",\"ok\":true}\n")
	}))
	defer srv.Close()
	dir := t.TempDir()
	tok := filepath.Join(dir, "t")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	c, _ := workerclient.New(srv.URL, tok, writePEM(t, dir, "ca.pem", srv))
	c.SetMaxStream(30)
	if err := c.Ledger(context.Background(), "x", io.Discard); !errors.Is(err, workerclient.ErrLedgerTooLarge) {
		t.Fatalf("got %v", err)
	}
}
