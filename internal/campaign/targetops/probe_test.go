package targetops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeUsesFixedArgs(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	ssh := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\" >> " + argv + "; done\n" +
		`echo '{"schema":"oscar-target-probe/v1","utc":"2026-11-13T00:00:00.000Z","oscar_version":"1.2","metrics_retention":"6","images":[],"containers":[],"flags":{"X":"true","Y":null},"overrides_sha256":"ab","alert_history":{"rows":3,"data_bytes":9},"errors":{"docker":"x"}}'` + "\nexit 2\n"
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := Probe(context.Background(), ssh, "/k/key", "oscar.home.arpa")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argv)
	got := strings.Fields(string(raw))
	want := []string{"-i", "/k/key", "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "oscar@oscar.home.arpa"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v, want %v (no remote command allowed)", got, want)
	}
	if *res.MetricsRetention != "6" || res.AlertHistory.Rows != 3 || res.Flags["Y"] != nil || *res.Flags["X"] != "true" || res.Errors["docker"] == "" {
		t.Fatalf("bad parse: %+v", res)
	}
}

func TestProbeRejectsWrongSchema(t *testing.T) {
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh")
	os.WriteFile(ssh, []byte("#!/bin/sh\necho '{\"schema\":\"v2\"}'\n"), 0o700)
	if _, err := Probe(context.Background(), ssh, "k", "h"); err == nil {
		t.Fatal("want schema error")
	}
}
