//go:build linux || darwin

package testpack_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

// This composes the real offline APIs and actual six-GET reader using response
// bodies emitted by the Python installer fixture. The HTTP peer is controlled;
// this is not a target installation, deployed admission, or CLI authority test.
func TestInstallationBindingComposesWithCurrentReader(t *testing.T) {
	for _, test := range []string{"same-readback", "changed-runtime", "missing-original-receipt"} {
		t.Run(test, func(t *testing.T) {
			read := func(name string) []byte {
				t.Helper()
				raw, e := os.ReadFile(name)
				if e != nil {
					t.Fatal(e)
				}
				return raw
			}
			pin := sha256.Sum256(read("testdata/kit/manifest.json"))
			kit, e := testpack.Load("testdata/kit", hex.EncodeToString(pin[:]))
			if e != nil {
				t.Fatal(e)
			}
			d := filepath.Join(t.TempDir(), "installation")
			if e = os.Mkdir(d, 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.Mkdir(filepath.Join(d, "kit"), 0700); e != nil {
				t.Fatal(e)
			}
			write := func(name string, raw []byte) {
				t.Helper()
				if e := os.WriteFile(filepath.Join(d, name), raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			for _, dir := range []string{"testdata/kit", "testdata/installation/records"} {
				entries, e := os.ReadDir(dir)
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					name := entry.Name()
					if dir == "testdata/kit" {
						name = "kit/" + name
					}
					write(name, read(filepath.Join(dir, entry.Name())))
				}
			}
			var calls atomic.Int32
			bodies := make([]json.RawMessage, 0, 6)
			for _, n := range []int{19, 11, 20, 16, 17, 18} {
				var after struct {
					Body json.RawMessage `json:"body"`
				}
				if e = json.Unmarshal(read(fmt.Sprintf("testdata/installation/records/%04d-after.json", n)), &after); e != nil {
					t.Fatal(e)
				}
				bodies = append(bodies, after.Body)
			}
			if test == "changed-runtime" {
				var body map[string]json.RawMessage
				json.Unmarshal(bodies[5], &body)
				body["last_attempt_ms"] = json.RawMessage("1")
				bodies[5], _ = json.Marshal(body)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1)) - 1
				if n >= 6 || r.Method != "GET" || r.Header.Get("X-API-Key") != "controlled-key" || r.Header.Get("X-Internal-Service") != "" {
					t.Error("unexpected read/credential")
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(bodies[n])
			}))
			defer server.Close()
			// Bind this controlled transport's origin in both original controls. All
			// original operation and response bytes remain unchanged (relative load URL).
			var owner json.RawMessage
			for _, name := range []string{"attempt.json", "completed.json"} {
				var value map[string]json.RawMessage
				json.Unmarshal(read(filepath.Join(d, name)), &value)
				value["target"], _ = json.Marshal(server.URL)
				var declaration map[string]json.RawMessage
				json.Unmarshal(value["ownership_declaration"], &declaration)
				declaration["target"], _ = json.Marshal(server.URL)
				owner, _ = json.Marshal(declaration)
				value["ownership_declaration"] = owner
				raw, _ := json.Marshal(value)
				canonical, e := testpack.CanonicalJSON(raw)
				if e != nil {
					t.Fatal(e)
				}
				write(name, canonical)
			}
			if test == "missing-original-receipt" {
				if e = os.Remove(filepath.Join(d, "0005-after.json")); e != nil {
					t.Fatal(e)
				}
			}
			installed, e := testpack.LoadInstallation(d, kit, server.URL, owner)
			if test == "missing-original-receipt" {
				if e == nil || installed != nil || calls.Load() != 0 {
					t.Fatal("missing original record reached HTTP")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			b := installed.Binding()
			kb := kit.Bindings()
			expected := oscar.InstalledPackExpected{ManifestSHA256: kb.ManifestSHA256, PackID: kb.PackID, Version: kb.Version, PackContentHash: kb.PackContentHash, MappingNamespace: kb.ProfileNamespace, MappingName: kb.ProfileName, MappingSourceFile: kb.MappingSourceFile, MappingStateSHA256: kb.MappingStateSHA256, MappingRowKey: kb.MappingRowKey, RuleJSON: kit.RuleJSON(), MappingStateJSON: kit.MappingStateJSON()}
			original := oscar.InstalledPackBinding{TargetOrigin: b.TargetOrigin, ManifestSHA256: b.ManifestSHA256, LoadID: b.LoadID, RuleID: b.RuleID, Runtime: oscar.InstalledRuntimeBinding{LoadedAt: b.LoadedAt, LastAttemptMS: b.LastAttemptMS, LoadedRuleIDs: []int{b.RuleID}}}
			reader, e := oscar.NewInstalledPackReader(domain.Target{ID: "controlled", BaseURL: server.URL, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "CONTROLLED_KEY"}}, oscar.Options{Getenv: func(string) string { return "controlled-key" }})
			if e != nil {
				t.Fatal(e)
			}
			observed, e := reader.Read(context.Background(), expected, original)
			if calls.Load() != 6 {
				t.Fatalf("expected six bounded GETs, got %d", calls.Load())
			}
			if test == "same-readback" {
				if e != nil || observed == nil {
					t.Fatalf("genuine journal binding mismatch: %v", e)
				}
			} else if e == nil || observed != nil {
				t.Fatal("changed live runtime adopted")
			}
		})
	}
}
