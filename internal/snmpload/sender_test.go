package snmpload_test

import (
	"context"
	"github.com/cmetech/oscar-corrtest/internal/snmpload"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSenderKeepsCommunityOffArgvAndRemovesPrivateConfig(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	script := "#!/bin/sh\ncase \"$*\" in *private-test*) exit 8;; esac\n[ -f \"$SNMPCONFPATH/snmp.conf\" ] || exit 9\nprintf '%s\\n' \"$SNMPCONFPATH\" \"$@\" > \"$TRAP_TRACE\"\n"
	os.WriteFile(filepath.Join(dir, "snmptrap"), []byte(script), 0700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TRAP_TRACE", trace)
	sender, err := snmpload.NewSender("127.0.0.1", 1162, "private-test")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := snmpload.Fixture("crt_secret_test", 0, "dhcp-limit")
	if err := sender.Send(context.Background(), e, "problem"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(trace)
	lines := strings.Split(string(body), "\n")
	config := filepath.Join(lines[0], "snmp.conf")
	info, err := os.Stat(config)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions: %v", err)
	}
	if !strings.Contains(string(body), e.ProblemOID) || !strings.Contains(string(body), "\nu\n1024\n") {
		t.Fatalf("incorrect typed varbinds: %s", body)
	}
	if err := sender.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("credential retained")
	}
}
