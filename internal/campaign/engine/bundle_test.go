package engine

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
