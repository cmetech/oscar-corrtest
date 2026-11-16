package campaign

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigStrict(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	_ = os.WriteFile(good, []byte(`{"TargetID":"t1","SinkURL":"https://s","CampaignNotifier":"CAMPAIGN_SINK"}`), 0o600)
	c, err := LoadConfig(good)
	if err != nil || c.TargetID != "t1" || c.MetricsCredentialFile != "" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"TargetID":"t1","Bogus":1}`), 0o600)
	if _, err := LoadConfig(bad); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file accepted")
	}
}
