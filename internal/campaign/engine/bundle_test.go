package engine

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign"
	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
)

const (
	secretFO      = "fo-secret-key-7f3a91"
	secretFO2     = "fo-secret-key-2b8c44"
	secretWorker  = "sentinel-worker-token-91ac"
	secretSink    = "sentinel-sink-token-5d2e"
	secretMetrics = "sentinel-metrics-cred-77b0"
	secretPDI     = "sentinel-pdi-password-3c19"
	secretProbe   = "sentinel-probe-key-a4f6"
	secretAPI     = "sentinel-oscar-api-key-e08d"
)

func exportedBundle(t *testing.T) (map[string][]byte, string) {
	t.Helper()
	h := newHarness(t)
	h.deps.FrontOfficeUsers = []frontoffice.User{{Name: "fo-01", APIKey: secretFO}, {Name: "fo-02", APIKey: secretFO2}}
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Sentinel credentials of every kind the real engine loads.
	dir := t.TempDir()
	wr := func(n, body string) string {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fo := filepath.Join(dir, "fo")
	_ = os.Mkdir(fo, 0o700)
	for i := 1; i <= 4; i++ {
		wr(fmt.Sprintf("fo/fo-%02d.json", i), `{"api_key":"`+secretFO+`"}`)
	}
	cfg := campaign.Config{TargetID: "t1", WorkerURL: "https://mini:8443", WorkerTokenFile: wr("worker", secretWorker), SinkURL: "https://mini:9443",
		SinkAdminTokenFile: wr("sink", secretSink), MetricsURL: "https://vm:8428", MetricsCredentialFile: wr("metrics", secretMetrics),
		SNOWInstanceURL: "https://dev.service-now.com", SNOWCredentialFile: wr("snow.json", `{"username":"u","password":"`+secretPDI+`"}`),
		FrontOfficeDir: fo, ProbeHost: "oscar", ProbeKeyFile: wr("probe", secretProbe)}
	real, err := NewFromConfig(context.Background(), cfg, stubHost{db: h.db, url: "https://oscar.home.arpa", dataDir: dir, apiKey: secretAPI})
	if err != nil {
		t.Fatal(err)
	}
	real.d.ReportDir = h.deps.ReportDir
	real.d.ExportRun = func(_ context.Context, id, dest string) (evidence.Result, error) {
		return evidence.Result{}, os.WriteFile(dest, []byte("child-zip-"+id), 0o600)
	}
	dest := filepath.Join(t.TempDir(), "campaign.zip")
	if _, err := real.ExportCampaign(context.Background(), h.id, dest); err != nil {
		t.Fatal(err)
	}
	if err := evidence.Verify(context.Background(), dest); err != nil {
		t.Fatalf("verify: %v", err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = b
	}
	raw, _ := os.ReadFile(dest)
	files["<raw zip>"] = raw
	return files, dest
}

func TestBundleContainsAllAttachmentsAndVerifies(t *testing.T) {
	t.Parallel()
	files, _ := exportedBundle(t)
	for _, n := range []string{"campaign.json", "prechecks.json", "guards.json", "lane-ledger.ndjson", "cohorts.ndjson", "metrics.json",
		"tickets.json", "resend.json", "frontoffice.ndjson", "scorecard.json", "campaign-report.html", "manifest.json"} {
		if _, ok := files[n]; !ok {
			t.Errorf("missing %s", n)
		}
	}
	child := false
	for n := range files {
		child = child || strings.HasPrefix(n, "correlation-") && strings.HasSuffix(n, ".zip")
	}
	if !child {
		t.Error("no child correlation bundle")
	}
	if !bytes.Contains(files["campaign-report.html"], []byte("OSCAR campaign evidence")) {
		t.Error("report not rendered")
	}
	if !bytes.Contains(files["prechecks.json"], []byte("B1")) {
		t.Errorf("prechecks: %s", files["prechecks.json"])
	}
}

func TestBundleHasNoSecrets(t *testing.T) {
	t.Parallel()
	files, _ := exportedBundle(t)
	for name, b := range files {
		for _, s := range []string{secretFO, secretFO2, secretWorker, secretSink, secretMetrics, secretPDI, secretProbe, secretAPI} {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s contains secret %q", name, s)
			}
		}
	}
}

func TestLargeLedgerIsGzipped(t *testing.T) {
	t.Parallel()
	raw := []byte(strings.Repeat("{\"a\":1}\n", 100))
	a, err := ledgerAttachment(raw, 50)
	if err != nil || a.Name != "lane-ledger.ndjson.gz" {
		t.Fatalf("%v %s", err, a.Name)
	}
	zr, _ := gzip.NewReader(bytes.NewReader(a.Content))
	if out, _ := io.ReadAll(zr); !bytes.Equal(out, raw) {
		t.Error("gzip round trip differs")
	}
	if a, _ := ledgerAttachment(raw, 1<<20); a.Name != "lane-ledger.ndjson" {
		t.Errorf("small ledger %s", a.Name)
	}
	_ = domain.LaneRow{}
}

func TestExportFailsWithoutLedger(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.deps.ExportRun = func(context.Context, string, string) (evidence.Result, error) { return evidence.Result{}, nil }
	ents, _ := filepath.Glob(filepath.Join(h.deps.ReportDir, "*.ledger.jsonl"))
	for _, f := range ents {
		_ = os.Remove(f)
	}
	if _, err := New(h.deps, h.clock).ExportCampaign(context.Background(), h.id, filepath.Join(t.TempDir(), "x.zip")); err == nil || !strings.Contains(err.Error(), "ledger") {
		t.Fatalf("err %v", err)
	}
}

func TestBundleMarksIncompleteWhenChildExportFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.deps.ExportRun = func(context.Context, string, string) (evidence.Result, error) {
		return evidence.Result{}, errors.New("boom")
	}
	dest := filepath.Join(t.TempDir(), "x.zip")
	if _, err := New(h.deps, h.clock).ExportCampaign(context.Background(), h.id, dest); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.OpenReader(dest)
	defer zr.Close()
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		switch f.Name {
		case "campaign.json":
			if !bytes.Contains(b, []byte(`"complete": false`)) || !bytes.Contains(b, []byte("boom")) {
				t.Errorf("campaign.json: %s", b)
			}
		case "campaign-report.html":
			if !bytes.Contains(b, []byte("Evidence incomplete")) {
				t.Error("no banner")
			}
		}
	}
}

// readEventChunks decodes every campaign-events-*.ndjson.gz entry in name order.
func readEventChunks(t *testing.T, files map[string][]byte) (seqs []int, chunks int) {
	t.Helper()
	var names []string
	for n := range files {
		if strings.HasPrefix(n, "campaign-events-") && strings.HasSuffix(n, ".ndjson.gz") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		zr, err := gzip.NewReader(bytes.NewReader(files[n]))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			var ev domain.CampaignEvent
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			seqs = append(seqs, ev.Sequence)
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	return seqs, len(names)
}

func assertContiguous(t *testing.T, seqs []int, last int) {
	t.Helper()
	if len(seqs) != last {
		t.Fatalf("exported %d events, store holds %d", len(seqs), last)
	}
	for i, s := range seqs {
		if s != i+1 {
			t.Fatalf("event %d has sequence %d: events missing or out of order", i, s)
		}
	}
}

// camp_681887ad6d96c6ff logged ~3.9M events; the export kept only the newest
// 1M and lost the scenario. Every event must be exported, oldest included.
func TestBundleExportsEveryEventAcrossPages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < eventPage+500; i++ {
		if err := h.db.AppendCampaignEvent(ctx, h.id, "campaign.correlation", "warn", "noise", json.RawMessage(`{"n":1}`), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	tail, err := h.db.RecentCampaignEvents(ctx, h.id, 1)
	if err != nil || len(tail) != 1 {
		t.Fatal(err)
	}
	h.deps.ExportRun = func(_ context.Context, id, dest string) (evidence.Result, error) {
		return evidence.Result{}, os.WriteFile(dest, []byte("child-zip-"+id), 0o600)
	}
	dest := filepath.Join(t.TempDir(), "x.zip")
	if _, err := New(h.deps, h.clock).ExportCampaign(ctx, h.id, dest); err != nil {
		t.Fatal(err)
	}
	if err := evidence.Verify(ctx, dest); err != nil {
		t.Fatalf("verify: %v", err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		files[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	seqs, _ := readEventChunks(t, files)
	assertContiguous(t, seqs, tail[0].Sequence)
	var cj struct {
		Plan   json.RawMessage
		Events int
	}
	if err := json.Unmarshal(files["campaign.json"], &cj); err != nil || string(cj.Plan) == "null" || cj.Events != tail[0].Sequence {
		t.Fatalf("campaign.json plan %s events %d (want %d): %v", cj.Plan, cj.Events, tail[0].Sequence, err)
	}
}

// A chunk that would pass the entry limit rolls over into the next file.
func TestEventExportRollsOverChunks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3000; i++ {
		if err := h.db.AppendCampaignEvent(ctx, h.id, "campaign.correlation", "warn", fmt.Sprintf("noise %d", i), json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	tail, _ := h.db.RecentCampaignEvents(ctx, h.id, 1)
	atts, ex, err := New(h.deps, h.clock).exportEvents(ctx, h.id, 4<<10)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, a := range atts {
		files[a.Name] = a.Content
	}
	seqs, chunks := readEventChunks(t, files)
	if chunks < 2 {
		t.Fatalf("%d chunks, want a rollover", chunks)
	}
	assertContiguous(t, seqs, tail[0].Sequence)
	if string(ex.scenario) == "null" || ex.count != tail[0].Sequence {
		t.Fatalf("scan: scenario %s count %d", ex.scenario, ex.count)
	}
}
