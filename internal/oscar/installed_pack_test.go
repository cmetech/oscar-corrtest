package oscar

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func installedPreflightFixture(t *testing.T) (InstalledPackExpected, InstalledPackBinding, []map[string]any) {
	t.Helper()
	ruleBytes, err := os.ReadFile("../../test-packs/correlation-operational-v1/correlation/flood.yml")
	if err != nil {
		t.Fatal(err)
	}
	var rule map[string]any
	if err = yaml.Unmarshal(ruleBytes, &rule); err != nil {
		t.Fatal(err)
	}
	profileBytes, err := os.ReadFile("../../test-packs/correlation-operational-v1/project/conf/mappings/corrtest_operational_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles []map[string]any
	if err = json.Unmarshal(profileBytes, &profiles); err != nil {
		t.Fatal(err)
	}
	mapping := profiles[0]
	mapping["comment"] = nil
	mapping["additional_ref"] = nil
	mapping["metadata"] = []any{map[string]any{"key": "source_pack", "value": "corrtest_operational"}}
	elements := mapping["element"].([]any)
	sort.Slice(elements, func(i, j int) bool {
		return elements[i].(map[string]any)["key"].(string) < elements[j].(map[string]any)["key"].(string)
	})
	r, _ := json.Marshal(rule)
	m, _ := json.Marshal(mapping)
	expected := InstalledPackExpected{ManifestSHA256: strings.Repeat("a", 64), PackID: "corrtest_operational", Version: "0.1.0", PackContentHash: "sha256:" + strings.Repeat("b", 64), MappingNamespace: "webhook_profiles", MappingName: "corrtest_operational_v1", MappingSourceFile: "conf/mappings/corrtest_operational_v1.json", MappingStateSHA256: strings.Repeat("c", 64), MappingRowKey: "mapping:" + strings.Repeat("d", 64), RuleJSON: r, MappingStateJSON: m}
	original := InstalledPackBinding{ManifestSHA256: expected.ManifestSHA256, LoadID: "550e8400-e29b-41d4-a716-446655440000", RuleID: 7, Runtime: InstalledRuntimeBinding{LoadedAt: "2026-10-17T12:00:00Z", LastAttemptMS: 1792238400000, LoadedRuleIDs: []int{7}}}
	rule["id"] = 7
	rule["created_by"] = "installer"
	rule["created_at"] = "2026-10-17T11:59:00Z"
	rule["updated_at"] = nil
	artifact := map[string]any{"namespace": "webhook_profiles", "name": "corrtest_operational_v1"}
	rows := []map[string]any{
		{"packs": []any{map[string]any{"pack_id": expected.PackID, "display_name": "Controlled test pack", "pack_version": expected.Version, "manifest_status": "valid", "pack_content_hash": expected.PackContentHash, "counts": nil, "latest_load": nil}}},
		{"load_id": original.LoadID, "pack_id": expected.PackID, "status": "completed", "activation_status": "succeeded", "activation_retryable": false, "created_count": 1, "replaced_count": 0, "skipped_count": 0, "failed_count": 0, "activation_targets": []any{map[string]any{"artifact_type": "mapping", "namespace": "webhook_profiles"}}, "activation_errors": []any{}, "page": 1, "per_page": 100, "total": 1, "items": []any{map[string]any{"artifact_type": "mapping", "identity": artifact, "id": 1, "source_file": expected.MappingSourceFile, "source_ordinal": 0, "intended_action": "create", "result": "created", "external_id": "1", "applied_hash": expected.MappingStateSHA256, "error_code": nil, "diagnostic": nil, "started_at": nil, "completed_at": "2026-10-17T12:00:00Z"}}},
		{"row_key": expected.MappingRowKey, "artifact_type": "mapping", "identity": artifact, "state": "up_to_date", "pack": mapping, "live": mapping, "pack_hash": expected.MappingStateSHA256, "live_hash": expected.MappingStateSHA256, "applied_hash": expected.MappingStateSHA256, "changed_fields": []any{}, "expected_live_hash": nil, "operational": nil, "diagnostic": nil},
		{"rows": []any{rule}, "total": 1, "page": 1, "perPage": 100},
		rule,
		{"authoritative": false, "dispatch_enabled": false, "dependencies": map[string]any{"status": "ready", "nats": true, "consumer": true, "redis": true, "lua_registered": true, "timer_store": true}, "load_status": "loaded", "loaded_at": original.Runtime.LoadedAt, "last_attempt_ms": original.Runtime.LastAttemptMS, "checked_at_ms": original.Runtime.LastAttemptMS + 5000, "loaded_rule_ids": []any{7}, "loaded_total": 1, "loaded_omitted": 0, "rejected_rules": []any{}, "rejected_total": 0, "rejected_omitted": 0, "selected_rule": map[string]any{"id": 7, "loaded": true, "rejection": nil}, "operational": false, "breaker": map[string]any{"state": "healthy"}, "enhanced_correlation": map[string]any{}},
	}
	// Separate wire records, so a nested mutation cannot change its paired expected image.
	raw, _ := json.Marshal(rows)
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return expected, original, rows
}
func installedPreflightPeer(t *testing.T, original *InstalledPackBinding, expected InstalledPackExpected, rows []map[string]any, mutateRaw func(int, []byte) []byte) (*InstalledPackReader, *int, func()) {
	t.Helper()
	calls := 0
	paths := []string{"/api/v1/packs", "/api/v1/packs/corrtest_operational/configuration/loads/" + original.LoadID, "/api/v1/packs/corrtest_operational/configuration/items/" + expected.MappingRowKey, "/api/v1/correlation_rules", "/api/v1/correlation_rules/7", "/api/v1/correlation_rules/runtime"}
	queries := []string{"", "page=1&per_page=100", "", "column=id&order=asc&page=1&perPage=100", "", "rule_id=7"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls
		calls++
		if n >= len(paths) {
			t.Error("unexpected retry")
			w.WriteHeader(500)
			return
		}
		if r.Method != "GET" || r.URL.Path != paths[n] || r.URL.RawQuery != queries[n] || r.Header.Get("X-API-Key") != "installed-key" || r.Header.Get("X-Internal-Service") != "" {
			t.Errorf("unexpected public read %s %s", r.Method, r.URL.String())
		}
		raw, _ := json.Marshal(rows[n])
		if mutateRaw != nil {
			raw = mutateRaw(n, raw)
		}
		w.Write(raw)
	}))
	original.TargetOrigin = server.URL
	reader, err := NewInstalledPackReader(nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "installed-key" }})
	if err != nil {
		t.Fatal(err)
	}
	return reader, &calls, server.Close
}
func TestInstalledPackCompletePublicReadback(t *testing.T) {
	expected, original, rows := installedPreflightFixture(t)
	reader, calls, close := installedPreflightPeer(t, &original, expected, rows, nil)
	defer close()
	observed, err := reader.Read(context.Background(), expected, original)
	if err != nil || observed == nil || *calls != 6 {
		t.Fatalf("observation=%v calls=%d err=%v", observed, *calls, err)
	}
	if len(observed.Runtime) == 0 || len(observed.Mapping) == 0 || len(observed.Rule) == 0 || len(observed.Packs) == 0 || len(observed.Load) == 0 || len(observed.Rules) == 0 {
		t.Fatal("lost full evidence")
	}
	var actual map[string]any
	json.Unmarshal(observed.Runtime, &actual)
	if actual["operational"] != false {
		t.Fatal("must allow real audit-only non-operational runtime")
	}
}
func TestInstalledPackRejectsChangedOrIncompleteReadback(t *testing.T) {
	type change struct {
		name   string
		index  int
		mutate func(map[string]any)
	}
	cases := []change{
		{"pack-missing", 0, func(v map[string]any) { v["packs"] = []any{} }},
		{"pack-duplicate", 0, func(v map[string]any) { v["packs"] = append(v["packs"].([]any), v["packs"].([]any)[0]) }},
		{"pack-hash", 0, func(v map[string]any) {
			v["packs"].([]any)[0].(map[string]any)["pack_content_hash"] = "sha256:" + strings.Repeat("e", 64)
		}},
		{"load-id", 1, func(v map[string]any) { v["load_id"] = "550e8400-e29b-41d4-a716-446655440001" }},
		{"load-pending", 1, func(v map[string]any) { v["status"] = "running" }},
		{"activation-pending", 1, func(v map[string]any) { v["activation_status"] = "pending" }},
		{"activation-not-required", 1, func(v map[string]any) { v["activation_status"] = "not_required" }},
		{"load-bool-count", 1, func(v map[string]any) { v["skipped_count"] = false }},
		{"load-page", 1, func(v map[string]any) { v["total"] = 2 }},
		{"load-item", 1, func(v map[string]any) { v["items"].([]any)[0].(map[string]any)["result"] = "unchanged" }},
		{"load-error", 1, func(v map[string]any) { v["items"].([]any)[0].(map[string]any)["error_code"] = "conflict" }},
		{"load-activation-target", 1, func(v map[string]any) { v["activation_targets"].([]any)[0].(map[string]any)["namespace"] = "other" }},
		{"mapping-live", 2, func(v map[string]any) {
			v["live"].(map[string]any)["element"].([]any)[0].(map[string]any)["value"] = "changed"
		}},
		{"mapping-pack", 2, func(v map[string]any) { v["pack"].(map[string]any)["comment"] = "changed" }},
		{"mapping-extra", 2, func(v map[string]any) { v["live"].(map[string]any)["extra"] = nil }},
		{"mapping-null-diff", 2, func(v map[string]any) { v["changed_fields"] = nil }},
		{"mapping-identity", 2, func(v map[string]any) { v["identity"].(map[string]any)["name"] = "other" }},
		{"rule-list-extra", 3, func(v map[string]any) { v["rows"] = append(v["rows"].([]any), v["rows"].([]any)[0]); v["total"] = 2 }},
		{"rule-list-short", 3, func(v map[string]any) { v["rows"] = []any{} }},
		{"rule-list-nested", 3, func(v map[string]any) {
			v["rows"].([]any)[0].(map[string]any)["match_criteria"].(map[string]any)["min_count"] = 6
		}},
		{"rule-original-id", 4, func(v map[string]any) { v["id"] = 8 }},
		{"rule-disabled", 4, func(v map[string]any) { v["enabled"] = false }},
		{"rule-null-not-empty", 4, func(v map[string]any) { v["for_notifiers"] = []any{} }},
		{"rule-nested", 4, func(v map[string]any) { v["emit_spec"].(map[string]any)["alertname"] = "other" }},
		{"runtime-authoritative", 5, func(v map[string]any) { v["authoritative"] = true }},
		{"runtime-dispatch", 5, func(v map[string]any) { v["dispatch_enabled"] = true }},
		{"runtime-dependency", 5, func(v map[string]any) { v["dependencies"].(map[string]any)["redis"] = false }},
		{"runtime-status", 5, func(v map[string]any) { v["dependencies"].(map[string]any)["status"] = "unavailable" }},
		{"runtime-selected", 5, func(v map[string]any) { v["selected_rule"].(map[string]any)["loaded"] = false }},
		{"runtime-rejected", 5, func(v map[string]any) { v["rejected_total"] = 1 }},
		{"runtime-omitted", 5, func(v map[string]any) { v["loaded_omitted"] = 1 }},
		{"runtime-empty", 5, func(v map[string]any) { v["loaded_rule_ids"] = []any{} }},
		{"runtime-load-changed", 5, func(v map[string]any) { v["last_attempt_ms"] = 1792238400001 }},
		{"runtime-loaded-at-changed", 5, func(v map[string]any) { v["loaded_at"] = "2026-10-17T12:01:00Z" }},
		{"runtime-missing-time", 5, func(v map[string]any) { v["checked_at_ms"] = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, b, rows := installedPreflightFixture(t)
			tc.mutate(rows[tc.index])
			r, calls, close := installedPreflightPeer(t, &b, e, rows, nil)
			defer close()
			got, err := r.Read(context.Background(), e, b)
			if err == nil || got != nil || *calls != tc.index+1 {
				t.Fatalf("accepted partial/foreign observation calls=%d err=%v", *calls, err)
			}
		})
	}
}
func TestInstalledPackRequiredReadbackFields(t *testing.T) {
	groups := [][]string{{"packs"}, {"load_id", "pack_id", "status", "activation_status", "created_count", "replaced_count", "skipped_count", "failed_count", "activation_targets", "activation_errors", "page", "per_page", "total", "items"}, {"row_key", "artifact_type", "identity", "state", "pack", "live", "pack_hash", "live_hash", "applied_hash", "changed_fields"}, {"rows", "total", "page", "perPage"}, {"id", "name", "pattern", "window_seconds", "group_by_labels", "match_criteria", "correlation_policy", "emit_spec", "for_notifiers", "priority", "max_synthetic_per_minute", "enabled", "description"}, {"authoritative", "dispatch_enabled", "dependencies", "load_status", "loaded_at", "last_attempt_ms", "checked_at_ms", "loaded_rule_ids", "loaded_total", "loaded_omitted", "rejected_rules", "rejected_total", "rejected_omitted", "selected_rule"}}
	for index, keys := range groups {
		for _, key := range keys {
			for _, mode := range []string{"missing", "null"} {
				if mode == "null" && index == 4 && (key == "correlation_policy" || key == "for_notifiers") {
					continue
				}
				t.Run(fmt.Sprintf("%d/%s/%s", index, key, mode), func(t *testing.T) {
					e, b, rows := installedPreflightFixture(t)
					if mode == "missing" {
						delete(rows[index], key)
					} else {
						rows[index][key] = nil
					}
					r, _, close := installedPreflightPeer(t, &b, e, rows, nil)
					defer close()
					got, err := r.Read(context.Background(), e, b)
					if err == nil || got != nil {
						t.Fatal("accepted missing/null required value")
					}
				})
			}
		}
	}
}
func TestInstalledPackAmbiguousReadback(t *testing.T) {
	for _, mode := range []string{"duplicate-top", "duplicate-nested", "unicode-alias", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			e, b, rows := installedPreflightFixture(t)
			r, _, close := installedPreflightPeer(t, &b, e, rows, func(i int, raw []byte) []byte {
				if i != 5 {
					return raw
				}
				switch mode {
				case "duplicate-top":
					return append([]byte(`{"dispatch_enabled":true,`), raw[1:]...)
				case "duplicate-nested":
					return []byte(strings.Replace(string(raw), `"redis":true`, `"redis":false,"redis":true`, 1))
				case "unicode-alias":
					return append([]byte(`{"diſpatch_enabled":true,`), raw[1:]...)
				default:
					return append(raw, []byte(`{}`)...)
				}
			})
			defer close()
			got, err := r.Read(context.Background(), e, b)
			if err == nil || got != nil {
				t.Fatal("accepted ambiguous JSON")
			}
		})
	}
}
func TestInstalledPackBindingRefusesBeforeHTTP(t *testing.T) {
	for _, mode := range []string{"origin", "manifest", "load", "rule", "runtime", "mode", "kit", "state", "rule-json"} {
		t.Run(mode, func(t *testing.T) {
			e, b, rows := installedPreflightFixture(t)
			r, calls, close := installedPreflightPeer(t, &b, e, rows, nil)
			defer close()
			switch mode {
			case "origin":
				b.TargetOrigin = "https://other.test"
			case "manifest":
				b.ManifestSHA256 = strings.Repeat("e", 64)
			case "load":
				b.LoadID = "../other"
			case "rule":
				b.RuleID = 0
			case "runtime":
				b.Runtime.LoadedRuleIDs = []int{8}
			case "mode":
				b.Runtime.Authoritative = true
			case "kit":
				e.PackID = "other"
			case "state":
				e.MappingStateJSON = json.RawMessage(`null`)
			case "rule-json":
				e.RuleJSON = json.RawMessage(`{}`)
			}
			got, err := r.Read(context.Background(), e, b)
			if err == nil || got != nil || *calls != 0 {
				t.Fatalf("invalid original binding made requests: %d %v", *calls, err)
			}
		})
	}
}
func TestInstalledPackExpectedSnapshot(t *testing.T) {
	e, b, rows := installedPreflightFixture(t)
	originalRule := append([]byte(nil), e.RuleJSON...)
	r, _, close := installedPreflightPeer(t, &b, e, rows, func(i int, raw []byte) []byte {
		if i == 0 {
			for j := range e.RuleJSON {
				e.RuleJSON[j] = ' '
			}
			b.Runtime.LoadedRuleIDs[0] = 9
		}
		return raw
	})
	defer close()
	got, err := r.Read(context.Background(), e, b)
	if err != nil || got == nil {
		t.Fatalf("did not retain expected snapshot: %v", err)
	}
	if reflect.DeepEqual(originalRule, e.RuleJSON) {
		t.Fatal("test did not change caller bytes")
	}
}

func TestInstalledPackDecodeBounds(t *testing.T) {
	for _, mode := range []string{"wide-object", "invalid-utf8", "deep-object"} {
		t.Run(mode, func(t *testing.T) {
			e, b, rows := installedPreflightFixture(t)
			r, _, close := installedPreflightPeer(t, &b, e, rows, func(i int, raw []byte) []byte {
				if i != 0 {
					return raw
				}
				if mode == "wide-object" {
					for j := 0; j < 1025; j++ {
						rows[0][fmt.Sprintf("field%d", j)] = nil
					}
					raw, _ = json.Marshal(rows[0])
					return raw
				}
				if mode == "invalid-utf8" {
					return append(append([]byte(`{"extra":"`), byte(255)), append([]byte(`",`), raw[1:]...)...)
				}
				return append([]byte(`{"extra":`+strings.Repeat(`[`, 34)+`0`+strings.Repeat(`]`, 34)+`,`), raw[1:]...)
			})
			defer close()
			got, err := r.Read(context.Background(), e, b)
			if err == nil || got != nil {
				t.Fatal("accepted JSON outside fixed decode bounds")
			}
		})
	}
}

func TestInstalledPackTransportRefusals(t *testing.T) {
	e, b, _ := installedPreflightFixture(t)
	calls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/api/v1/packs", 307)
	}))
	defer server.Close()
	b.TargetOrigin = server.URL
	r, err := NewInstalledPackReader(nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "installed-key" }})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := r.Read(context.Background(), e, b); err == nil || result != nil || calls != 0 {
		t.Fatal("followed redirect")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req, _ := http.NewRequest(method, server.URL+"/api/v1/packs", nil)
		if _, err := r.client.http.Transport.RoundTrip(req); err == nil {
			t.Fatal("allowed mutation")
		}
	}
	for _, path := range []string{"/api/v1/packs/other/configuration/loads/" + b.LoadID, "/api/v1/packs/corrtest_operational/configuration/items/other", "/api/v1/correlation_rules/import", "/api/v1/alerts/history"} {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		if _, err := r.client.http.Transport.RoundTrip(req); err == nil {
			t.Fatal("allowed unrelated path")
		}
	}
	for _, mode := range []string{"path", "query", "fragment", "insecure", "client", "input", "credential"} {
		target := nativeObserverTarget(server.URL)
		options := Options{Getenv: func(string) string { return "key" }}
		switch mode {
		case "path":
			target.BaseURL += "/path"
		case "query":
			target.BaseURL += "?x=1"
		case "fragment":
			target.BaseURL += "#x"
		case "insecure":
			target.TLS.Insecure = true
		case "client":
			options.HTTPClient = server.Client()
		case "input":
			options.InputPath = "alert-engine"
		case "credential":
			options.Getenv = func(string) string { return "" }
		}
		if _, err := NewInstalledPackReader(target, options); err == nil {
			t.Fatalf("accepted %s", mode)
		}
	}
}
func TestInstalledPackHTTPStatusAndBounds(t *testing.T) {
	for _, mode := range []string{"created", "oversize", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			e, b, rows := installedPreflightFixture(t)
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "created":
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(rows[0])
				case "oversize":
					fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1))
				default:
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			b.TargetOrigin = server.URL
			reader, err := NewInstalledPackReader(nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "key" }})
			if err != nil {
				t.Fatal(err)
			}
			timeout := 2 * time.Second
			if mode == "cancel" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			got, err := reader.Read(ctx, e, b)
			if err == nil || got != nil || calls.Load() != 1 {
				t.Fatalf("allowed response/no-retry violation: calls=%d err=%v", calls.Load(), err)
			}
			if mode == "oversize" {
				var machine *MachineError
				if !errors.As(err, &machine) || machine.Code != "response_too_large" {
					t.Fatalf("did not prove byte cap: %v", err)
				}
			}
		})
	}
}
func TestInstalledPackVerifiedTLS(t *testing.T) {
	e, b, rows := installedPreflightFixture(t)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls >= len(rows) {
			t.Error("extra request")
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(rows[calls])
		calls++
	}))
	defer server.Close()
	b.TargetOrigin = server.URL
	target := nativeObserverTarget(server.URL)
	r, err := NewInstalledPackReader(target, Options{Getenv: func(string) string { return "key" }})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(context.Background(), e, b); err == nil || got != nil || calls != 0 {
		t.Fatal("accepted untrusted TLS")
	}
	target.TLS.CAPath = filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(target.TLS.CAPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = NewInstalledPackReader(target, Options{Getenv: func(string) string { return "key" }})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(context.Background(), e, b); err != nil || got == nil || calls != 6 {
		t.Fatalf("trusted read failed: %v", err)
	}
}
