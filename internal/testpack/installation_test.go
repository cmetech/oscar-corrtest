//go:build linux || darwin

package testpack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const installOrigin = "https://owned-target.test"

func installationFixture(t *testing.T) (string, *Kit, []byte) {
	t.Helper()
	kd, pin := fixture(t)
	k, err := Load(kd, pin)
	if err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(t.TempDir(), "original")
	if err := os.Mkdir(d, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d, "kit"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(append([]string{}, names[:]...), "manifest.json", "manifest.sha256") {
		writeTest(t, d, "kit/"+name, readTest(t, kd, name))
	}
	entries, err := os.ReadDir("testdata/installation/records")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		writeTest(t, d, entry.Name(), readTest(t, "testdata/installation/records", entry.Name()))
	}
	c := installationObject(t, d, "completed.json")
	return d, k, encodeCanonical(c["ownership_declaration"])
}
func installationObject(t *testing.T, d, n string) map[string]any {
	t.Helper()
	v, e := parseJSON(readTest(t, d, n))
	if e != nil {
		t.Fatal(e)
	}
	return v.(map[string]any)
}
func editInstallation(t *testing.T, d, n string, edit func(map[string]any)) {
	t.Helper()
	v := installationObject(t, d, n)
	edit(v)
	writeTest(t, d, n, encodeCanonical(v))
}
func rehashInstallation(t *testing.T, d string) {
	t.Helper()
	c := installationObject(t, d, "completed.json")
	n := c["journal_operations"].(int64)
	rows := []any{}
	for i := int64(1); i <= n; i++ {
		for _, suffix := range []string{"before", "after"} {
			name := fmt.Sprintf("%04d-%s.json", i, suffix)
			raw := readTest(t, d, name)
			rows = append(rows, map[string]any{"path": name, "bytes": int64(len(raw)), "sha256": digest(raw)})
		}
	}
	c["journal_sha256"] = digest(encodeCanonical(rows))
	writeTest(t, d, "completed.json", encodeCanonical(c))
}
func installationRefused(t *testing.T, d string, k *Kit, origin string, owner []byte) {
	t.Helper()
	got, e := LoadInstallation(d, k, origin, owner)
	if e == nil || got != nil {
		t.Fatalf("invalid installation accepted: result=%v error=%v", got != nil, e)
	}
}
func TestInstallationActualPythonJournal(t *testing.T) {
	d, k, owner := installationFixture(t)
	got, e := LoadInstallation(d, k, installOrigin, owner)
	if e != nil {
		t.Fatal(e)
	}
	c := installationObject(t, d, "completed.json")
	b := got.Binding()
	if b.TargetOrigin != installOrigin || b.ManifestSHA256 != k.Bindings().ManifestSHA256 || b.RuleID != 7 || b.LoadID != c["load_id"] || b.ProjectOperationID != c["project_operation_id"] || b.JournalOperations != 20 || b.JournalSHA256 != c["journal_sha256"] || b.CompletedSHA256 != digest(readTest(t, d, "completed.json")) || b.OwnershipSHA256 != digest(owner) {
		t.Fatalf("lost original binding: %+v", b)
	}
	if b.LoadedAt != "2026-10-17T00:00:00+00:00" || b.LastAttemptMS != 1792200000000 || b.AttemptID == "" {
		t.Fatal("runtime/attempt identity lost")
	}
	if len(got.Files()) != 52 {
		t.Fatalf("lost full retained snapshot: %d", len(got.Files()))
	}
	// Raw response was emitted with indentation by the actual Python peer. Its
	// original hash is not a hash of the canonical parsed body; accept that fact.
	after := installationObject(t, d, "0001-after.json")
	if after["body_sha256"] == digest(encodeCanonical(after["body"])) {
		t.Fatal("fixture does not exercise raw wire limitation")
	}
	raw, ok := got.Evidence("completed.json")
	if !ok {
		t.Fatal("missing snapshot")
	}
	raw[0] = 'X'
	again, _ := got.Evidence("completed.json")
	if again[0] != '{' {
		t.Fatal("mutable evidence exposed")
	}
	files := got.Files()
	files[0].SHA256 = "changed"
	if got.Files()[0].SHA256 == "changed" {
		t.Fatal("mutable files exposed")
	}
	writeTest(t, d, "completed.json", []byte("changed"))
	afterEdit, _ := got.Evidence("completed.json")
	if !bytes.Equal(again, afterEdit) || got.Binding() != b {
		t.Fatal("disk mutation changed retained value")
	}
	if _, ok := got.Evidence("../completed.json"); ok {
		t.Fatal("arbitrary path accepted")
	}
}
func TestInstallationZeroAndExplicitInputs(t *testing.T) {
	for _, name := range []string{"nil-kit", "zero-kit", "origin", "origin-path", "owner", "duplicate-owner", "nil-owner"} {
		t.Run(name, func(t *testing.T) {
			d, k, owner := installationFixture(t)
			origin := installOrigin
			switch name {
			case "nil-kit":
				k = nil
			case "zero-kit":
				k = &Kit{}
			case "origin":
				origin = "https://other.test"
			case "origin-path":
				origin += "/"
			case "owner":
				owner = bytes.Replace(owner, []byte("root-owned-target-1"), []byte("different"), 1)
			case "duplicate-owner":
				owner = append([]byte(`{"format":"wrong",`), owner[1:]...)
			case "nil-owner":
				owner = nil
			}
			installationRefused(t, d, k, origin, owner)
		})
	}
	var zero Installation
	if zero.Binding() != (InstallationBinding{}) || len(zero.Files()) != 0 {
		t.Fatal("zero installation produced proof")
	}
	if _, ok := zero.Evidence("completed.json"); ok {
		t.Fatal("zero installation exposed proof")
	}
}
func TestInstallationOriginalBindingsAndRehashedMutations(t *testing.T) {
	tests := []struct {
		name, file string
		edit       func(map[string]any)
	}{
		{"operation-substitution", "completed.json", func(m map[string]any) { m["project_operation_id"] = "11111111-1111-4111-8111-111111111111" }},
		{"load-substitution", "completed.json", func(m map[string]any) { m["load_id"] = "11111111-1111-4111-8111-111111111111" }},
		{"rule-substitution", "completed.json", func(m map[string]any) { m["rule_id"] = int64(8) }},
		{"runtime-substitution", "completed.json", func(m map[string]any) { m["runtime_binding"].(map[string]any)["last_attempt_ms"] = int64(1) }},
		{"completed-authority", "completed.json", func(m map[string]any) { m["ownership_observed"] = true }},
		{"completed-revision", "completed.json", func(m map[string]any) { m["profile_revision_observed"] = true }},
		{"completed-extra", "completed.json", func(m map[string]any) { m["authority"] = true }},
		{"completed-limits", "completed.json", func(m map[string]any) { m["limits"] = []any{} }},
		{"completed-scope", "completed.json", func(m map[string]any) { m["scope"] = "PASS" }},
		{"completed-manifest", "completed.json", func(m map[string]any) { m["manifest_sha256"] = strings.Repeat("0", 64) }},
		{"completed-effective", "completed.json", func(m map[string]any) { m["profile_effective_sha256"] = strings.Repeat("0", 64) }},
		{"completed-content", "completed.json", func(m map[string]any) { m["pack_content_hash"] = "sha256:" + strings.Repeat("0", 64) }},
		{"completed-rule", "completed.json", func(m map[string]any) { m["authored_rule_sha256"] = strings.Repeat("0", 64) }},
		{"attempt-format", "attempt.json", func(m map[string]any) { m["format"] = "other" }},
		{"attempt-id", "attempt.json", func(m map[string]any) { m["attempt_id"] = "missing" }},
		{"attempt-owner", "attempt.json", func(m map[string]any) { m["ownership_declaration"].(map[string]any)["owner_reference"] = "different" }},
		{"attempt-responsibility", "attempt.json", func(m map[string]any) { m["initial_responsibility"] = "CLEAN" }},
		{"project-command", "0005-before.json", func(m map[string]any) { m["request"].(map[string]any)["command"] = "/owned/other" }},
		{"project-archive", "0005-before.json", func(m map[string]any) { m["request"].(map[string]any)["archive_sha256"] = strings.Repeat("0", 64) }},
		{"project-receipt", "0005-after.json", func(m map[string]any) {
			m["receipt"].(map[string]any)["operation_id"] = "11111111-1111-4111-8111-111111111111"
		}},
		{"project-side-effects", "0005-after.json", func(m map[string]any) { m["receipt"].(map[string]any)["updated"] = int64(1) }},
		{"project-bool-count", "0005-after.json", func(m map[string]any) { m["receipt"].(map[string]any)["updated"] = false }},
		{"request-responsibility", "0004-before.json", func(m map[string]any) { m["responsibility"] = "KNOWN" }},
		{"request-extra", "0004-before.json", func(m map[string]any) { m["request"].(map[string]any)["retry"] = true }},
		{"validate-rule", "0004-before.json", func(m map[string]any) { m["request"].(map[string]any)["body"].(map[string]any)["enabled"] = false }},
		{"validate-failed", "0004-after.json", func(m map[string]any) { m["body"].(map[string]any)["valid"] = false }},
		{"preview-conflict", "0007-after.json", func(m map[string]any) {
			m["body"].(map[string]any)["items"].([]any)[0].(map[string]any)["state"] = "conflict"
		}},
		{"load-overwrite", "0008-before.json", func(m map[string]any) {
			m["request"].(map[string]any)["body"].(map[string]any)["overwrite"] = []any{"mapping"}
		}},
		{"accepted-foreign-url", "0008-after.json", func(m map[string]any) { m["body"].(map[string]any)["status_url"] = "https://other.test/load" }},
		{"accepted-status", "0008-after.json", func(m map[string]any) { m["status"] = int64(200) }},
		{"poll-foreign-id", "0009-after.json", func(m map[string]any) { m["body"].(map[string]any)["load_id"] = "11111111-1111-4111-8111-111111111111" }},
		{"load-partial", "0011-after.json", func(m map[string]any) { m["body"].(map[string]any)["total"] = int64(2) }},
		{"load-activation", "0011-after.json", func(m map[string]any) { m["body"].(map[string]any)["activation_status"] = "failed" }},
		{"import-zip", "0015-before.json", func(m map[string]any) { m["request"].(map[string]any)["file_bytes"] = int64(1) }},
		{"import-adopted", "0015-after.json", func(m map[string]any) { m["body"].(map[string]any)["updated"] = []any{ruleName} }},
		{"final-rule-field", "0017-after.json", func(m map[string]any) { m["body"].(map[string]any)["window_seconds"] = int64(61) }},
		{"final-rule-list", "0016-after.json", func(m map[string]any) { m["body"].(map[string]any)["total"] = int64(2) }},
		{"final-runtime-authority", "0018-after.json", func(m map[string]any) { m["body"].(map[string]any)["authoritative"] = true }},
		{"final-runtime-dispatch", "0018-after.json", func(m map[string]any) { m["body"].(map[string]any)["dispatch_enabled"] = true }},
		{"final-runtime-dependency", "0018-after.json", func(m map[string]any) { m["body"].(map[string]any)["dependencies"].(map[string]any)["redis"] = false }},
		{"final-runtime-omitted", "0018-after.json", func(m map[string]any) { m["body"].(map[string]any)["loaded_omitted"] = int64(1) }},
		{"final-pack", "0019-after.json", func(m map[string]any) {
			m["body"].(map[string]any)["packs"].([]any)[0].(map[string]any)["pack_version"] = "other"
		}},
		{"final-mapping", "0020-after.json", func(m map[string]any) { m["body"].(map[string]any)["live"].(map[string]any)["description"] = "changed" }},
		{"initial-rules", "0002-after.json", func(m map[string]any) { m["body"].(map[string]any)["total"] = int64(1) }},
		{"initial-runtime", "0003-after.json", func(m map[string]any) { m["body"].(map[string]any)["loaded_total"] = int64(1) }},
		{"after-status-type", "0001-after.json", func(m map[string]any) { m["status"] = true }},
		{"after-body-bound", "0001-after.json", func(m map[string]any) { m["body_bytes"] = int64(262145) }},
		{"after-digest", "0001-after.json", func(m map[string]any) { m["body_sha256"] = "bad" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, k, owner := installationFixture(t)
			editInstallation(t, d, tt.file, tt.edit)
			if strings.Contains(tt.file, "-before") || strings.Contains(tt.file, "-after") {
				rehashInstallation(t, d)
			}
			installationRefused(t, d, k, installOrigin, owner)
		})
	}
}
func TestInstallationSnapshotRefusals(t *testing.T) {
	for _, name := range []string{"receipt-missing", "extra-failed", "extra-directory", "pair-gap", "kit-change", "kit-symlink", "file-symlink", "fifo", "root-mode", "file-mode", "kit-mode", "root-symlink", "root-symlink-slash", "root-symlink-dot", "oversize", "extra-json-key", "float", "unsafe-int", "noncanonical", "161-operations", "duplicate-operation", "reordered-operation"} {
		t.Run(name, func(t *testing.T) {
			d, k, owner := installationFixture(t)
			switch name {
			case "receipt-missing":
				os.Remove(filepath.Join(d, "0005-after.json"))
			case "extra-failed":
				writeTest(t, d, "failed.json", []byte("{}\n"))
			case "extra-directory":
				os.Mkdir(filepath.Join(d, "foreign"), 0700)
			case "pair-gap":
				os.Remove(filepath.Join(d, "0010-before.json"))
			case "kit-change":
				writeTest(t, d, "kit/README.md", []byte("different"))
			case "kit-symlink":
				moved := filepath.Join(t.TempDir(), "moved")
				os.Rename(filepath.Join(d, "kit"), moved)
				os.Symlink(moved, filepath.Join(d, "kit"))
			case "file-symlink":
				os.Remove(filepath.Join(d, "0005-after.json"))
				os.Symlink(filepath.Join(d, "completed.json"), filepath.Join(d, "0005-after.json"))
			case "fifo":
				os.Remove(filepath.Join(d, "0005-after.json"))
				if err := unix.Mkfifo(filepath.Join(d, "0005-after.json"), 0600); err != nil {
					t.Fatal(err)
				}
			case "root-mode":
				os.Chmod(d, 0755)
			case "file-mode":
				os.Chmod(filepath.Join(d, "completed.json"), 0644)
			case "kit-mode":
				os.Chmod(filepath.Join(d, "kit"), 0755)
			case "root-symlink", "root-symlink-slash", "root-symlink-dot":
				link := filepath.Join(t.TempDir(), "link")
				os.Symlink(d, link)
				d = link
				if name == "root-symlink-slash" {
					d += "/"
				}
				if name == "root-symlink-dot" {
					d += "/."
				}
			case "oversize":
				writeTest(t, d, "0005-after.json", bytes.Repeat([]byte("x"), maxBytes+1))
			case "extra-json-key":
				raw := readTest(t, d, "completed.json")
				writeTest(t, d, "completed.json", append([]byte(`{"status":"completed",`), raw[1:]...))
			case "float":
				raw := readTest(t, d, "completed.json")
				writeTest(t, d, "completed.json", bytes.Replace(raw, []byte(`"rule_id":7`), []byte(`"rule_id":7.0`), 1))
			case "unsafe-int":
				raw := readTest(t, d, "completed.json")
				writeTest(t, d, "completed.json", bytes.Replace(raw, []byte(`"rule_id":7`), []byte(`"rule_id":9007199254740992`), 1))
			case "noncanonical":
				writeTest(t, d, "completed.json", append([]byte(" "), readTest(t, d, "completed.json")...))
			case "161-operations":
				editInstallation(t, d, "completed.json", func(m map[string]any) { m["journal_operations"] = int64(161) })
			case "duplicate-operation":
				for _, s := range []string{"before", "after"} {
					writeTest(t, d, "0013-"+s+".json", readTest(t, d, "0004-"+s+".json"))
				}
				rehashInstallation(t, d)
			case "reordered-operation":
				for _, s := range []string{"before", "after"} {
					a := readTest(t, d, "0019-"+s+".json")
					b := readTest(t, d, "0020-"+s+".json")
					writeTest(t, d, "0019-"+s+".json", b)
					writeTest(t, d, "0020-"+s+".json", a)
				}
				rehashInstallation(t, d)
			}
			installationRefused(t, d, k, installOrigin, owner)
		})
	}
}

func TestInstallationPrivateSpecialModesRefused(t *testing.T) {
	for _, name := range []string{"root-sticky", "kit-setgid", "file-setuid"} {
		t.Run(name, func(t *testing.T) {
			d, k, owner := installationFixture(t)
			switch name {
			case "root-sticky":
				if e := os.Chmod(d, 0700|os.ModeSticky); e != nil {
					t.Fatal(e)
				}
			case "kit-setgid":
				if e := os.Chmod(filepath.Join(d, "kit"), 0700|os.ModeSetgid); e != nil {
					t.Fatal(e)
				}
			case "file-setuid":
				if e := os.Chmod(filepath.Join(d, "completed.json"), 0600|os.ModeSetuid); e != nil {
					t.Fatal(e)
				}
			}
			installationRefused(t, d, k, installOrigin, owner)
		})
	}
}
func TestInstallationAggregateBoundBeforeSemanticDecode(t *testing.T) {
	d, k, owner := installationFixture(t)
	// 40 bounded individual journal files exceed the original 32 MiB aggregate.
	// The snapshot must refuse before interpreting these intentionally invalid bodies.
	padded := bytes.Repeat([]byte("x"), maxBytes)
	for n := 1; n <= 20; n++ {
		for _, suffix := range []string{"before", "after"} {
			writeTest(t, d, fmt.Sprintf("%04d-%s.json", n, suffix), padded)
		}
	}
	got, e := LoadInstallation(d, k, installOrigin, owner)
	if got != nil || e == nil || !strings.Contains(e.Error(), "aggregate") {
		t.Fatalf("aggregate must be refused before semantic decode: %v", e)
	}
}
func TestInstallationSingleCompletedPollAndCopiedNullFields(t *testing.T) {
	d, k, owner := installationFixture(t)
	// Remove only the two queued/running poll pairs. This is the actual producer's
	// other legal outcome: first GET already completed. Renumber original bytes.
	for n := 9; n <= 18; n++ {
		for _, suffix := range []string{"before", "after"} {
			writeTest(t, d, fmt.Sprintf("%04d-%s.json", n, suffix), readTest(t, d, fmt.Sprintf("%04d-%s.json", n+2, suffix)))
		}
	}
	for n := 19; n <= 20; n++ {
		for _, suffix := range []string{"before", "after"} {
			if e := os.Remove(filepath.Join(d, fmt.Sprintf("%04d-%s.json", n, suffix))); e != nil {
				t.Fatal(e)
			}
		}
	}
	editInstallation(t, d, "completed.json", func(m map[string]any) { m["journal_operations"] = int64(18) })
	rehashInstallation(t, d)
	if got, e := LoadInstallation(d, k, installOrigin, owner); e != nil || got == nil {
		t.Fatalf("first-poll completed rejected: %v", e)
	}
}

func TestInstallationExactPublicBase(t *testing.T) {
	d, k, owner := installationFixture(t)
	base := installOrigin + "/ext/mw"
	for _, name := range []string{"attempt.json", "completed.json"} {
		editInstallation(t, d, name, func(m map[string]any) {
			m["target"] = base
			m["ownership_declaration"].(map[string]any)["target"] = base
		})
	}
	ownership, _ := parseJSON(owner)
	ownership.(map[string]any)["target"] = base
	owner = encodeCanonical(ownership)
	editInstallation(t, d, "0008-after.json", func(m map[string]any) {
		body := m["body"].(map[string]any)
		body["status_url"] = base + body["status_url"].(string)
	})
	rehashInstallation(t, d)
	got, e := LoadInstallation(d, k, base, owner)
	if e != nil || got == nil {
		t.Fatalf("exact public base rejected: %v", e)
	}
	if got.Binding().TargetOrigin != base {
		t.Fatal("public base lost")
	}
	editInstallation(t, d, "0008-after.json", func(m map[string]any) {
		body := m["body"].(map[string]any)
		body["status_url"] = strings.Replace(body["status_url"].(string), "/ext/mw", "", 1)
	})
	rehashInstallation(t, d)
	installationRefused(t, d, k, base, owner)
}
func TestInstallationBaseSyntax(t *testing.T) {
	for _, raw := range []string{installOrigin, "http://owned-target.test:8080", installOrigin + "/ext/mw"} {
		if !installationOrigin(raw) {
			t.Errorf("valid configured base refused: %s", raw)
		}
	}
	for _, raw := range []string{installOrigin + "/", installOrigin + "/ext/mw/", installOrigin + "/ext%2fmw", installOrigin + "/ext/mw/..", installOrigin + "/other", installOrigin + "?", installOrigin + "#", installOrigin + "?next=other", "https://user@owned-target.test", "https://owned-target.test:65536", "https://owned-target.test\\other", "https://owned-target.test\u00a0"} {
		if installationOrigin(raw) {
			t.Errorf("invalid configured base accepted: %q", raw)
		}
	}
}

func TestInstallationFixtureProducerProvenance(t *testing.T) {
	v, e := parseJSON(readTest(t, "testdata/installation", "provenance.json"))
	if e != nil {
		t.Fatal(e)
	}
	p := v.(map[string]any)
	records := p["files"].(map[string]any)
	entries, e := os.ReadDir("testdata/installation/records")
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 42 || len(records) != 42 || p["operations"] != int64(20) {
		t.Fatal("unexpected original Python fixture inventory")
	}
	for _, entry := range entries {
		metadata, ok := records[entry.Name()].(map[string]any)
		if !ok {
			t.Fatal("unrecorded fixture")
		}
		raw := readTest(t, "testdata/installation/records", entry.Name())
		if metadata["bytes"] != int64(len(raw)) || metadata["sha256"] != digest(raw) {
			t.Fatalf("producer fixture differs: %s", entry.Name())
		}
	}
	source := p["producer_sources"].(map[string]any)
	for _, name := range []string{"build_operational.py", "install_operational.py", "test_install_operational.py"} {
		if !digestPattern.MatchString(source["test-packs/"+name].(string)) {
			t.Fatal("missing producer provenance")
		}
	}
	_, k, _ := installationFixture(t)
	if p["manifest_sha256"] != k.Bindings().ManifestSHA256 {
		t.Fatal("producer used another kit")
	}
	verified := p["python_verification"].(map[string]any)
	if verified["status"] != "current-readback-matches" || verified["ownership_observed"] != false {
		t.Fatal("missing original Python verification/limit")
	}
}
