package oscar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func compactGolden(t *testing.T, name string) ([]byte, CompactEvidenceScope) {
	t.Helper()
	raw, err := os.ReadFile("testdata/compact-evidence/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Scope CompactEvidenceScope }
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return raw, v.Scope
}
func compactMutate(t *testing.T, raw []byte, f func(map[string]any)) []byte {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	f(v)
	delete(v, "evidence_sha256")
	plain, _ := json.Marshal(v)
	canonical, err := testpack.CanonicalJSONInt64(plain)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	v["evidence_sha256"] = hex.EncodeToString(hash[:])
	out, _ := json.Marshal(v)
	return out
}
func TestCompactEvidenceBackendGoldens(t *testing.T) {
	for _, name := range []string{"projector-response", "mysql-accepted", "unicode-int64-python"} {
		t.Run(name, func(t *testing.T) {
			raw, scope := compactGolden(t, name)
			doc, err := ValidateCompactEvidence(raw, scope)
			if err != nil {
				t.Fatal(err)
			}
			if doc.GenerationCount != 1 || !doc.Actions[0].PhysicalHistoryVerified {
				t.Fatal("lost physical proof")
			}
		})
	}
}
func TestCompactEvidenceRejectsRehashedMalformedEvidence(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	cases := map[string]func(map[string]any){
		"extra":            func(v map[string]any) { v["secret"] = "never expose" },
		"incomplete":       func(v map[string]any) { v["inventory_complete"] = false },
		"scope":            func(v map[string]any) { v["scope"].(map[string]any)["epoch"] = strings.Repeat("b", 32) },
		"closed":           func(v map[string]any) { v["partition"].(map[string]any)["state"] = "closed" },
		"dense":            func(v map[string]any) { v["actions"].([]any)[0].(map[string]any)["generation_ordinal"] = 2 },
		"count":            func(v map[string]any) { v["generation_count"] = 0 },
		"historylessproof": func(v map[string]any) { v["actions"].([]any)[0].(map[string]any)["history"] = nil },
		"pendinghistory":   func(v map[string]any) { v["actions"].([]any)[0].(map[string]any)["physical_history_verified"] = false },
		"nestedextra": func(v map[string]any) {
			v["actions"].([]any)[0].(map[string]any)["sender"].(map[string]any)["token"] = "secret"
		},
		"unknownreason": func(v map[string]any) { v["actions"].([]any)[0].(map[string]any)["validation_reason"] = "ok" },
		"naivetime":     func(v map[string]any) { v["observed_at"] = "2026-10-20T01:01:01" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			bad := compactMutate(t, raw, change)
			if _, err := ValidateCompactEvidence(bad, scope); err == nil {
				t.Fatal("accepted malformed evidence")
			}
		})
	}
	duplicate := bytes.Replace(raw, []byte(`"format":`), []byte(`"format":"compact-evidence-v1","format":`), 1)
	if _, err := ValidateCompactEvidence(duplicate, scope); err == nil {
		t.Fatal("duplicate accepted")
	}
	bad := append([]byte(nil), raw...)
	bad = bytes.Replace(bad, []byte("Flood proof"), []byte("Flood spoof"), 1)
	if _, err := ValidateCompactEvidence(bad, scope); err == nil {
		t.Fatal("bad digest accepted")
	}
}
func compactPeer(t *testing.T, handler http.HandlerFunc) (domain.Target, func()) {
	t.Helper()
	s := httptest.NewTLSServer(handler)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	target := nativeObserverTarget(s.URL)
	target.TLS.CAPath = ca
	return target, s.Close
}
func TestCompactEvidenceClientExactTLSRead(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	var calls atomic.Int32
	target, close := compactPeer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/ext/mw/api/v1/alerts/correlation/compact/evidence" || len(r.URL.Query()) != 3 || r.URL.Query().Get("epoch") != scope.Epoch || r.Header.Get("X-API-Key") != "user-key" || r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("X-Internal-Service") != "" || r.Header.Get("Authorization") != "" {
			t.Error("wrong request")
		}
		w.Write(raw)
	})
	defer close()
	target.BaseURL += "/ext/mw"
	reader, err := NewCompactEvidenceReader(target, Options{Getenv: func(string) string { return "user-key" }})
	if err != nil {
		t.Fatal(err)
	}
	obs, err := reader.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || obs.HTTPStatus != 200 || !bytes.Equal(obs.Body, raw) || obs.StartedAt == "" || obs.CompletedAt == "" {
		t.Fatal("lost exchange")
	}
	if err = ValidateCompactEvidenceObservation(scope, obs); err != nil {
		t.Fatal(err)
	}
}
func TestCompactEvidenceClientFailureNeverEmptySuccess(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	for _, mode := range []string{"503", "redirect", "gzip", "oversize", "invalid", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			target, close := compactPeer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "503":
					w.WriteHeader(503)
					w.Write([]byte("secret database credential"))
				case "redirect":
					w.Header().Set("Location", "https://elsewhere.invalid")
					w.WriteHeader(302)
				case "gzip":
					w.Header().Set("Content-Encoding", "gzip")
					w.Write(raw)
				case "oversize":
					w.Header().Set("Content-Length", "4194305")
				case "invalid":
					w.Write([]byte(`{"unexpected":true}`))
				case "timeout":
					<-r.Context().Done()
				}
			})
			defer close()
			reader, err := NewCompactEvidenceReader(target, Options{Getenv: func(string) string { return "user-key" }})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			obs, err := reader.Read(ctx, scope)
			if err == nil || obs.FailureClass == "" || calls.Load() != 1 {
				t.Fatalf("failure lost calls %d class %s err %v", calls.Load(), obs.FailureClass, err)
			}
			if strings.Contains(err.Error(), "secret") || bytes.Contains(obs.Body, []byte("secret")) {
				t.Fatal("leaked raw error")
			}
			if mode == "invalid" && len(obs.Body) == 0 {
				t.Fatal("lost bounded malformed200")
			}
			if err = ValidateCompactEvidenceObservation(scope, obs); err == nil {
				t.Fatal("failure replay accepted")
			}
		})
	}
}
func TestCompactEvidenceClientRejectsUnsafeTargets(t *testing.T) {
	target := nativeObserverTarget("https://localhost")
	for _, mode := range []string{"http", "insecure", "injected", "alias", "emptykey"} {
		t.Run(mode, func(t *testing.T) {
			v := target
			opts := Options{Getenv: func(string) string { return "key" }}
			switch mode {
			case "http":
				v.BaseURL = "http://localhost"
			case "insecure":
				v.TLS.Insecure = true
			case "injected":
				opts.HTTPClient = &http.Client{}
			case "alias":
				v.BaseURL += "/ext/mw/"
			case "emptykey":
				opts.Getenv = func(string) string { return " " }
			}
			if _, err := NewCompactEvidenceReader(v, opts); err == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
}

func TestCompactEvidenceRejectsContradictoryProjectedStages(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	for _, field := range []string{"receipt_slot", "inactive_parent", "ready_attempt", "pending_physical", "early_ancestry"} {
		t.Run(field, func(t *testing.T) {
			bad := compactMutate(t, raw, func(v map[string]any) {
				a := v["actions"].([]any)[0].(map[string]any)
				switch field {
				case "receipt_slot":
					a["history_receipt"].(map[string]any)["slot_id"] = strings.Repeat("0", 64)
				case "inactive_parent":
					a["incident"].(map[string]any)["state"] = "parent_pending"
				case "ready_attempt":
					a["sender"].(map[string]any)["attempts"] = 0
				case "pending_physical":
					a["physical_history_verified"] = false
					a["history"] = nil
					a["validation_reason"] = "history_pending"
				case "early_ancestry":
					a["physical_history_verified"] = false
					a["history"] = nil
					a["history_receipt"] = nil
					a["validation_reason"] = "ingress_pending"
				}
			})
			if _, err := ValidateCompactEvidence(bad, scope); err == nil {
				t.Fatal("contradictory projection accepted")
			}
		})
	}
}
func TestCompactEvidenceValidEmptyAndPendingAreDistinct(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	empty := compactMutate(t, raw, func(v map[string]any) { v["actions"] = []any{}; v["generation_count"] = 0 })
	doc, err := ValidateCompactEvidence(empty, scope)
	if err != nil || doc.GenerationCount != 0 {
		t.Fatal("healthy empty refused", err)
	}
	for _, reason := range []string{"ingress_pending", "summary_expansion_pending", "summary_retention_pending", "history_pending", "history_rejected"} {
		t.Run(reason, func(t *testing.T) {
			pending := compactMutate(t, raw, func(v map[string]any) {
				a := v["actions"].([]any)[0].(map[string]any)
				a["physical_history_verified"] = false
				a["validation_reason"] = reason
				a["history"] = nil
				a["history_receipt"] = nil
				switch reason {
				case "ingress_pending":
					a["sender"] = map[string]any{"state": "ready", "attempts": 0}
					a["ingress"] = nil
					a["outbox"] = nil
					a["summary_child"] = nil
					a["incident"] = nil
					a["parent_create_work"] = nil
				case "summary_expansion_pending":
					a["outbox"].(map[string]any)["publication_state"] = "unattempted"
					a["summary_child"] = nil
					a["incident"] = nil
					a["parent_create_work"] = nil
				case "summary_retention_pending":
					a["summary_child"].(map[string]any)["publication_state"] = "unattempted"
					a["incident"] = nil
					a["parent_create_work"] = nil
				default:
					a["incident"].(map[string]any)["state"] = "parent_pending"
					a["incident"].(map[string]any)["version"] = 1
					a["parent_create_work"].(map[string]any)["state"] = "waiting_receipt"
					a["parent_create_work"].(map[string]any)["destination_state"] = "ambiguous"
					if reason == "history_rejected" {
						a["history_receipt"] = map[string]any{"service": "alertmanager", "action_id": strings.Repeat("a", 64), "slot_id": a["parent_create_work"].(map[string]any)["work_id"], "state": "rejected", "recovery_scope": "summary", "history_id": nil}
					}
				}
			})
			doc, err := ValidateCompactEvidence(pending, scope)
			if err != nil || doc.GenerationCount != 1 || doc.Actions[0].PhysicalHistoryVerified {
				t.Fatal("pending misclassified", err)
			}
		})
	}
}

func TestCompactEvidenceClientRejectsUntrustedCertificateAndInvalidScopeBeforeRequest(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	var calls atomic.Int32
	target, close := compactPeer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(raw) })
	defer close()
	options := Options{Getenv: func(string) string { return "key" }}
	trusted, err := NewCompactEvidenceReader(target, options)
	if err != nil {
		t.Fatal(err)
	}
	invalid := scope
	invalid.Epoch = strings.ToUpper(strings.Repeat("b", 32))
	obs, err := trusted.Read(context.Background(), invalid)
	if err == nil || calls.Load() != 0 || obs.HTTPStatus != 0 {
		t.Fatal("invalid scope performed IO")
	}
	target.TLS.CAPath = ""
	untrusted, err := NewCompactEvidenceReader(target, options)
	if err != nil {
		t.Fatal(err)
	}
	obs, err = untrusted.Read(context.Background(), scope)
	if err == nil || calls.Load() != 0 || obs.HTTPStatus != 0 {
		t.Fatal("untrusted server reached application")
	}
}
func TestCompactEvidenceClientBoundsChunkedBodiesAndNeverRetries(t *testing.T) {
	_, scope := compactGolden(t, "projector-response")
	var calls atomic.Int32
	target, close := compactPeer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		chunk := bytes.Repeat([]byte("a"), 32768)
		for i := 0; i < 130; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	defer close()
	reader, err := NewCompactEvidenceReader(target, Options{Getenv: func(string) string { return "key" }})
	if err != nil {
		t.Fatal(err)
	}
	obs, err := reader.Read(context.Background(), scope)
	if err == nil || calls.Load() != 1 || len(obs.Body) != 0 || obs.FailureClass != "response_too_large" {
		t.Fatalf("unbounded/partial response: calls=%d body=%d failure=%s err=%v", calls.Load(), len(obs.Body), obs.FailureClass, err)
	}
}
func TestCompactEvidenceWireRejectsIntegerAndDuplicateAmbiguity(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	for _, change := range []struct{ old, new string }{
		{`"generation_count": 1`, `"generation_count": 1.0`},
		{`"generation_count": 1`, `"generation_count": true`},
		{`"version": 1`, `"version": 9223372036854775808`},
		{`"site": "NYC"`, `"site": "NYC", "site": "OTHER"`},
		{`"site": "NYC"`, `"site": "\ud800"`},
	} {
		bad := bytes.Replace(raw, []byte(change.old), []byte(change.new), 1)
		if bytes.Equal(bad, raw) {
			t.Fatalf("fixture replacement missed %s", change.old)
		}
		if _, err := ValidateCompactEvidence(bad, scope); err == nil {
			t.Fatalf("accepted %s", change.new)
		}
	}
	other := scope
	other.Revision = strings.Repeat("a", 64)
	if _, err := ValidateCompactEvidence(raw, other); err == nil {
		t.Fatal("stale revision accepted")
	}
}
func TestCompactEvidenceReplayRefusesMissingOrReversedObservationTimes(t *testing.T) {
	raw, scope := compactGolden(t, "projector-response")
	for _, times := range [][2]string{{"", ""}, {"2026-10-20T12:00:00Z", "2026-10-20T11:00:00Z"}} {
		obs := CompactEvidenceObservation{StartedAt: times[0], CompletedAt: times[1], HTTPStatus: 200, Body: raw}
		if err := ValidateCompactEvidenceObservation(scope, obs); err == nil {
			t.Fatal("invalid observation replayed")
		}
	}
}
