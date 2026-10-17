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
	"strconv"
	"strings"
	"testing"
	"time"
)

func installedAuditRule() Rule {
	return Rule{ID: 27, Name: "corrtest-operational-v1-flood", Pattern: "flood"}
}
func installedAuditRow(id int) map[string]any {
	return map[string]any{"id": id, "created_at": "2026-10-17T12:34:56.123456", "alert_fingerprint": "source-fingerprint", "rule_id": 27, "rule_name": "corrtest-operational-v1-flood", "pattern": "flood", "outcome": "enriched", "window_id": "27:opaque:window", "reason": nil, "added_labels": map[string]string{"retained": "value"}}
}
func installedAuditReader(t *testing.T, origin string) *InstalledPackAuditReader {
	t.Helper()
	reader, err := NewInstalledPackAuditReader(nativeObserverTarget(origin), Options{Getenv: func(string) string { return "installed-read-key" }})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}
func TestInstalledPackAuditCompleteRead(t *testing.T) {
	for _, total := range []int{0, 1, 100, 101, 200, maxListPages * 100} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if r.Method != "GET" || r.URL.Path != correlationAuditPath || r.URL.Query().Get("fingerprint") != "source-fingerprint" || r.URL.Query().Get("perPage") != "100" || len(r.URL.Query()) != 3 || r.Header.Get("X-API-Key") != "installed-read-key" || r.Header.Get("X-Internal-Service") != "" {
					t.Error("unexpected audit transport")
				}
				rows := []map[string]any{}
				for i := (page-1)*100 + 1; i <= total && i <= page*100; i++ {
					rows = append(rows, installedAuditRow(i))
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows, "total": total, "page": page, "perPage": 100})
			}))
			defer server.Close()
			rows, err := installedAuditReader(t, server.URL).FindAudit(context.Background(), "source-fingerprint", installedAuditRule())
			wantCalls := (total + 99) / 100
			if wantCalls == 0 {
				wantCalls = 1
			}
			if err != nil || rows == nil || len(rows) != total || calls != wantCalls {
				t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls, err)
			}
			if total > 0 {
				row := rows[0]
				if row.ID != 1 || row.CreatedAtRaw != "2026-10-17T12:34:56.123456" || !row.CreatedAt.Equal(time.Date(2026, 10, 17, 12, 34, 56, 123456000, time.UTC)) || row.WindowID != "27:opaque:window" || row.Reason != nil || !strings.Contains(string(row.Raw), `"retained":"value"`) {
					t.Fatalf("lost audit attribution: %+v", row)
				}
			}
		})
	}
}
func TestInstalledPackAuditRefusesIncompleteOrForeignPages(t *testing.T) {
	faults := []string{"missing-rows", "null-rows", "missing-total", "null-total", "missing-page", "null-page", "missing-perPage", "null-perPage", "negative-total", "overflow-total", "short-first", "empty-second", "wrong-page", "wrong-perPage", "changing-total", "duplicate-same", "duplicate-across", "zero-id", "negative-id", "string-id", "bool-id", "fractional-id", "missing-id", "foreign-fingerprint", "foreign-rule-id", "null-rule-id", "foreign-rule-name", "foreign-pattern", "missing-time", "null-time", "empty-time", "zero-time", "date-only", "invalid-time", "null-window", "missing-window", "empty-window", "wrong-window-type", "unknown-outcome", "missing-outcome", "missing-reason", "wrong-reason-type", "duplicate-metadata", "duplicate-row-key", "case-alias-metadata", "case-alias-row", "wrong-case-metadata", "wrong-case-row", "unicode-alias", "trailing-json", "not-object", "status204", "status500"}
	for _, fault := range faults {
		t.Run(fault, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				rows := []map[string]any{}
				n := 100
				if page == 2 {
					n = 1
				}
				for i := 0; i < n; i++ {
					rows = append(rows, installedAuditRow((page-1)*100+i+1))
				}
				response := map[string]any{"rows": rows, "total": 101, "page": page, "perPage": 100}
				switch fault {
				case "missing-rows", "missing-total", "missing-page", "missing-perPage":
					delete(response, strings.TrimPrefix(fault, "missing-"))
				case "null-rows", "null-total", "null-page", "null-perPage":
					response[strings.TrimPrefix(fault, "null-")] = nil
				case "negative-total":
					response["total"] = -1
				case "overflow-total":
					response["total"] = maxListPages*100 + 1
				case "short-first":
					response["rows"] = rows[:99]
				case "empty-second":
					if page == 2 {
						response["rows"] = []any{}
					}
				case "wrong-page":
					response["page"] = page + 1
				case "wrong-perPage":
					response["perPage"] = 25
				case "changing-total":
					if page == 2 {
						response["total"] = 102
					}
				case "duplicate-same":
					if page == 1 {
						rows[1]["id"] = 1
					}
				case "duplicate-across":
					if page == 2 {
						rows[0]["id"] = 1
					}
				case "zero-id":
					rows[0]["id"] = 0
				case "negative-id":
					rows[0]["id"] = -1
				case "string-id":
					rows[0]["id"] = "1"
				case "bool-id":
					rows[0]["id"] = true
				case "fractional-id":
					rows[0]["id"] = 1.5
				case "missing-id":
					delete(rows[0], "id")
				case "foreign-fingerprint":
					rows[0]["alert_fingerprint"] = "foreign"
				case "foreign-rule-id":
					rows[0]["rule_id"] = 28
				case "null-rule-id":
					rows[0]["rule_id"] = nil
				case "foreign-rule-name":
					rows[0]["rule_name"] = "foreign"
				case "foreign-pattern":
					rows[0]["pattern"] = "sequence"
				case "missing-time":
					delete(rows[0], "created_at")
				case "null-time":
					rows[0]["created_at"] = nil
				case "empty-time":
					rows[0]["created_at"] = ""
				case "zero-time":
					rows[0]["created_at"] = "0001-01-01T00:00:00Z"
				case "date-only":
					rows[0]["created_at"] = "2026-10-17"
				case "invalid-time":
					rows[0]["created_at"] = "not-a-date"
				case "null-window":
					rows[0]["window_id"] = nil
				case "missing-window":
					delete(rows[0], "window_id")
				case "empty-window":
					rows[0]["window_id"] = " "
				case "wrong-window-type":
					rows[0]["window_id"] = 2
				case "unknown-outcome":
					rows[0]["outcome"] = "healthy"
				case "missing-outcome":
					delete(rows[0], "outcome")
				case "missing-reason":
					delete(rows[0], "reason")
				case "wrong-reason-type":
					rows[0]["reason"] = []string{"reason"}
				case "status204":
					w.WriteHeader(204)
					return
				case "status500":
					w.WriteHeader(500)
					return
				}
				raw, _ := json.Marshal(response)
				switch fault {
				case "duplicate-metadata":
					raw = append([]byte(`{"total":0,`), raw[1:]...)
				case "duplicate-row-key":
					raw = []byte(strings.Replace(string(raw), `"id":1,`, `"id":999,"id":1,`, 1))
				case "case-alias-metadata":
					raw = append([]byte(`{"TOTAL":101,`), raw[1:]...)
				case "case-alias-row":
					raw = []byte(strings.Replace(string(raw), `"id":1,`, `"ID":999,"id":1,`, 1))
				case "wrong-case-metadata":
					raw = []byte(strings.Replace(string(raw), `"rows":`, `"ROWS":`, 1))
				case "wrong-case-row":
					raw = []byte(strings.Replace(string(raw), `"id":1,`, `"ID":1,`, 1))
				case "unicode-alias":
					raw = []byte(strings.Replace(string(raw), `"reason":null`, `"reason":null,"reaſon":"foreign"`, 1))
				case "trailing-json":
					raw = append(raw, []byte(` {}`)...)
				case "not-object":
					raw = []byte(`[]`)
				}
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			rows, err := installedAuditReader(t, server.URL).FindAudit(context.Background(), "source-fingerprint", installedAuditRule())
			if err == nil || rows != nil {
				t.Fatalf("accepted incomplete/foreign %s: rows=%d err=%v", fault, len(rows), err)
			}
			if calls > 2 {
				t.Fatalf("unexpected retry: %d", calls)
			}
		})
	}
}
func TestInstalledPackAuditRecognizedOutcomesAndReason(t *testing.T) {
	for _, outcome := range []string{"pass_through", "enriched", "suppressed_per_notifier", "suppressed_global", "parent_emitted", "released_no_trigger"} {
		t.Run(outcome, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				row := installedAuditRow(1)
				row["outcome"] = outcome
				row["reason"] = "actual_reason"
				row["created_at"] = "2026-10-17T14:34:56.123456+02:00"
				_ = json.NewEncoder(w).Encode(map[string]any{"rows": []any{row}, "total": 1, "page": 1, "perPage": 100})
			}))
			defer server.Close()
			rows, err := installedAuditReader(t, server.URL).FindAudit(context.Background(), "source-fingerprint", installedAuditRule())
			if err != nil || len(rows) != 1 || rows[0].Reason == nil || *rows[0].Reason != "actual_reason" || rows[0].Outcome != outcome {
				t.Fatalf("outcome observation lost: %v", err)
			}
		})
	}
}
func TestInstalledPackAuditTransportRefusals(t *testing.T) {
	calls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+correlationAuditPath, 307)
	}))
	defer server.Close()
	reader := installedAuditReader(t, server.URL)
	if rows, err := reader.FindAudit(context.Background(), "source-fingerprint", installedAuditRule()); err == nil || rows != nil || calls != 0 {
		t.Fatal("followed redirect")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if _, _, err := reader.client.do(context.Background(), method, correlationAuditPath, nil, nil); err == nil {
			t.Fatalf("accepted mutation %s", method)
		}
	}
	if _, _, err := reader.client.do(context.Background(), "GET", "/api/v1/alerts/history", nil, nil); err == nil {
		t.Fatal("accepted unrelated route")
	}
}
func TestInstalledPackAuditInputRefusalsBeforeHTTP(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	reader := installedAuditReader(t, server.URL)
	for _, fingerprint := range []string{"", " ", strings.Repeat("x", 129)} {
		if _, err := reader.FindAudit(context.Background(), fingerprint, installedAuditRule()); err == nil {
			t.Fatal("accepted invalid fingerprint")
		}
	}
	for _, rule := range []Rule{{}, {ID: 1, Name: " ", Pattern: "flood"}, {ID: 1, Name: "a", Pattern: "sequence"}} {
		if _, err := reader.FindAudit(context.Background(), "fp", rule); err == nil {
			t.Fatal("accepted invalid rule binding")
		}
	}
	for _, suffix := range []string{"/path", "?query=x", "#fragment"} {
		if _, err := NewInstalledPackAuditReader(nativeObserverTarget(server.URL+suffix), Options{}); err == nil {
			t.Fatal("accepted non-origin")
		}
	}
	if _, err := NewInstalledPackAuditReader(nativeObserverTarget(server.URL), Options{HTTPClient: server.Client()}); err == nil {
		t.Fatal("accepted injected transport")
	}
	if calls != 0 {
		t.Fatalf("invalid inputs sent %d requests", calls)
	}
}
func TestInstalledPackAuditResponseBoundAndCancellation(t *testing.T) {
	for _, mode := range []string{"oversize", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "oversize" {
					fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1))
				} else {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			timeout := 50 * time.Millisecond
			if mode == "oversize" {
				timeout = 2 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			rows, err := installedAuditReader(t, server.URL).FindAudit(ctx, "source-fingerprint", installedAuditRule())
			if err == nil || rows != nil {
				t.Fatal("accepted unbounded/cancelled response")
			}
			if mode == "oversize" {
				var machine *MachineError
				if !errors.As(err, &machine) || machine.Code != "response_too_large" {
					t.Fatalf("did not prove response byte cap: %v", err)
				}
			}
		})
	}
}

func TestInstalledPackAuditVerifiedTLS(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"rows":[],"total":0,"page":1,"perPage":100}`)
	}))
	defer server.Close()
	target := nativeObserverTarget(server.URL)
	reader, err := NewInstalledPackAuditReader(target, Options{Getenv: func(string) string { return "installed-read-key" }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.FindAudit(context.Background(), "fp", installedAuditRule()); err == nil || calls != 0 {
		t.Fatal("accepted untrusted TLS")
	}
	target.TLS.Insecure = true
	if _, err = NewInstalledPackAuditReader(target, Options{}); err == nil {
		t.Fatal("accepted insecure TLS")
	}
	target.TLS.Insecure = false
	target.TLS.CAPath = filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(target.TLS.CAPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err = NewInstalledPackAuditReader(target, Options{Getenv: func(string) string { return "installed-read-key" }})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := reader.FindAudit(context.Background(), "fp", installedAuditRule()); err != nil || rows == nil || len(rows) != 0 || calls != 1 {
		t.Fatalf("trusted TLS read failed: %v", err)
	}
}
