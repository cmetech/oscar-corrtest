package engine

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
)

const (
	secretFO  = "fo-secret-key-7f3a91"
	secretFO2 = "fo-secret-key-2b8c44"
)

func exportedBundle(t *testing.T) (map[string][]byte, string) {
	t.Helper()
	h := newHarness(t)
	h.deps.FrontOfficeUsers = []frontoffice.User{{Name: "fo-01", APIKey: secretFO}, {Name: "fo-02", APIKey: secretFO2}}
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.deps.ExportRun = func(_ context.Context, id, dest string) (evidence.Result, error) {
		return evidence.Result{}, os.WriteFile(dest, []byte("child-zip-"+id), 0o600)
	}
	dest := filepath.Join(t.TempDir(), "campaign.zip")
	if _, err := New(h.deps, h.clock).ExportCampaign(context.Background(), h.id, dest); err != nil {
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
		for _, s := range []string{secretFO, secretFO2, "worker-token", "pdi-password", "sink-token"} {
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
