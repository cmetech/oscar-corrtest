//go:build linux || darwin

package runtime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func compactRuntimeFixture() []byte {
	return []byte(`{"authoritative":false,"dispatch_enabled":false,"enhanced_correlation":{"production_enabled":false},"compact_summary":{"state":"running","local_readiness":{"owner_verified":true,"parent_handoff_ready":true,"ordinary_correlator_dispatch":"disabled","reason":null,"scope":{"rule_id":1,"epoch":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","rule_revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","manifest_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}}}}`)
}

func TestCompactObservationScopeRequiresActualReadyInstalledOwner(t *testing.T) {
	scope, manifest, err := compactObservationScope(compactRuntimeFixture(), 1)
	if err != nil || scope.RuleID != 1 || scope.Epoch != strings.Repeat("a", 32) || manifest != strings.Repeat("c", 64) {
		t.Fatalf("scope not derived: %+v %q %v", scope, manifest, err)
	}
	for _, pair := range [][2]string{
		{`"owner_verified":true`, `"owner_verified":false`},
		{`"parent_handoff_ready":true`, `"parent_handoff_ready":false`},
		{`"state":"running"`, `"state":"blocked"`},
		{`"reason":null`, `"reason":"unknown"`},
		{`"rule_id":1`, `"rule_id":2`},
		{`"production_enabled":false`, `"production_enabled":true`},
		{`"dispatch_enabled":false`, `"dispatch_enabled":true`},
		{`"ordinary_correlator_dispatch":"disabled"`, `"ordinary_correlator_dispatch":"unobserved"`},
		{`"epoch":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"epoch":"bad"`},
	} {
		raw := bytes.Replace(compactRuntimeFixture(), []byte(pair[0]), []byte(pair[1]), 1)
		if _, _, err := compactObservationScope(raw, 1); err == nil {
			t.Fatalf("accepted unproven scope: %s", pair[0])
		}
	}
}

func TestCompactObservationOutputIsExclusivePrivateAndByteExact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	writer, err := prepareCompactObservationOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.close()
	if _, err := prepareCompactObservationOutput(path); err == nil {
		t.Fatal("concurrent observation accepted same destination")
	}
	raw := []byte("{\"raw\":\"<é>\\u2028\",\"outcome\":\"observation-only\"}\n")
	if err := writer.publish(raw); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("original bytes changed: %q %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode: %v %v", info, err)
	}
	if _, err := prepareCompactObservationOutput(path); err == nil {
		t.Fatal("existing observation overwritten")
	}
	if !json.Valid(got) {
		t.Fatal("invalid final artifact")
	}
}

func TestCompactObservationOutputNeverReplacesForeignDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	writer, err := prepareCompactObservationOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.close()
	if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writer.publish([]byte("{}")); err == nil {
		t.Fatal("foreign destination replaced")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "foreign" {
		t.Fatal("foreign bytes changed")
	}
}

func TestCompactObservationReplayKeepsCaptureByteBounds(t *testing.T) {
	for _, bodySize := range []int{(1 << 20) + 1, 800 << 10} {
		capture := &CompactConfigurationCapture{StartedAt: time.Now(), CompletedAt: time.Now(),
			HTTPStatus: 200, Bodies: map[string][]byte{}}
		for _, key := range []string{"packs", "load", "mapping", "rules", "rule", "runtime"} {
			capture.Bodies[key] = bytes.Repeat([]byte(" "), bodySize)
		}
		if _, err := compactConfigurationValue(capture); err == nil {
			t.Fatalf("replay accepted impossible capture byte size %d", bodySize)
		}
	}
}
