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

func installedActiveRow(id int) map[string]any {
	labels := []map[string]any{}
	for _, pair := range [][2]string{{"alertname", "CorrTestOperationalFlood"}, {"qualification_pack", "corrtest_operational_v1"}, {"qualification_run", "run-1"}, {"site", "positive"}, {"source_id", fmt.Sprintf("source-%d", id)}, {"severity", "warning"}} {
		labels = append(labels, map[string]any{"name": pair[0], "value": pair[1]})
	}
	return map[string]any{"alert_labels": labels, "alert_annotations": []any{}, "starts_at": "2026-10-17T12:00:00.123456Z", "ends_at": "2026-10-17T12:10:00Z", "modified_at": "2026-10-17T12:00:01Z", "fingerprint": fmt.Sprintf("%016x", id), "alertname": "CorrTestOperationalFlood", "severity": "warning", "alert_status": "active", "status": map[string]any{"state": "active", "silencedBy": []any{}, "inhibitedBy": []any{}}, "receivers": []any{map[string]any{"name": "controlled-history"}}, "correlation_parent_state": nil}
}
func installedActiveReader(t *testing.T, origin string) *InstalledPackActiveReader {
	t.Helper()
	r, e := NewInstalledPackActiveReader(nativeObserverTarget(origin), Options{Getenv: func(string) string { return "installed-key" }})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestInstalledPackActiveCompleteNativeObservations(t *testing.T) {
	for _, total := range []int{0, 1, 100, 101, maxListPages * 100} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				page, _ := strconv.Atoi(q.Get("page"))
				if r.Method != "GET" || r.URL.Path != "/api/v1/alerts/active-alerts" || q.Get("perPage") != "100" || q.Get("column") != "fingerprint" || q.Get("order") != "asc" || q.Get("show_children") != "true" || r.Header.Get("X-API-Key") != "installed-key" || r.Header.Get("X-Internal-Service") != "" {
					t.Error("wrong public Active transport")
				}
				var filter struct {
					Items []struct{ Field, Operator, Value string }
					Logic string `json:"logicOperator"`
				}
				if json.Unmarshal([]byte(q.Get("filter")), &filter) != nil || filter.Logic != "and" || len(filter.Items) != 3 {
					t.Fatal("missing finite scope")
				}
				want := map[string]string{"alertname": "CorrTestOperationalFlood", "label:qualification_pack": "corrtest_operational_v1", "label:qualification_run": "run-1"}
				for _, item := range filter.Items {
					if want[item.Field] != item.Value || item.Operator != "equals" {
						t.Error("wrong scope predicate")
					}
					delete(want, item.Field)
				}
				if len(want) != 0 {
					t.Error("missing scope predicate")
				}
				rows := []any{}
				for i := (page-1)*100 + 1; i <= total && i <= page*100; i++ {
					rows = append(rows, installedActiveRow(i))
				}
				// The actual public schema deliberately has no page/per_page echo.
				_ = json.NewEncoder(w).Encode(map[string]any{"records": rows, "total_records": total, "total_pages": (total + 99) / 100})
			}))
			defer server.Close()
			rows, err := installedActiveReader(t, server.URL).FindActive(context.Background(), "run-1")
			expectedCalls := (total + 99) / 100
			if expectedCalls == 0 {
				expectedCalls = 1
			}
			if err != nil || rows == nil || len(rows) != total || calls != expectedCalls {
				t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls, err)
			}
			if total > 0 {
				row := rows[0]
				if row.Fingerprint != "0000000000000001" || row.StartsAtRaw != "2026-10-17T12:00:00.123456Z" || !row.StartsAt.Equal(time.Date(2026, 10, 17, 12, 0, 0, 123456000, time.UTC)) || row.Labels["source_id"] != "source-1" || row.Labels["qualification_run"] != "run-1" || len(row.Raw) == 0 {
					t.Fatalf("lost Native identity: %+v", row)
				}
			}
		})
	}
}
func TestInstalledPackActiveRefusesPartialForeignOrAmbiguous(t *testing.T) {
	faults := []string{"missing-records", "null-records", "missing-total_records", "null-total_records", "missing-total_pages", "null-total_pages", "negative-total", "bool-total", "overflow-total", "wrong-pages", "changing-total", "short-page", "empty-second", "duplicate-fingerprint", "duplicate-across", "changed-start-same-fingerprint", "duplicate-source-id", "reverse-order", "invalid-fingerprint", "uppercase-fingerprint", "missing-fingerprint", "null-fingerprint", "missing-start", "null-start", "zero-start", "date-only", "invalid-start", "null-labels", "missing-labels", "duplicate-label", "null-label", "null-label-value", "number-label-value", "empty-label-name", "missing-required-label", "foreign-run", "foreign-pack", "foreign-name", "wrong-top-name", "foreign-site", "foreign-severity", "empty-source-id", "synthetic-parent", "synthetic-parent-alias", "duplicate-json-key", "unicode-alias", "trailing-json", "status503"}
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
					rows = append(rows, installedActiveRow((page-1)*100+i+1))
				}
				body := map[string]any{"records": rows, "total_records": 101, "total_pages": 2}
				labels := rows[0]["alert_labels"].([]map[string]any)
				switch fault {
				case "missing-records", "missing-total_records", "missing-total_pages":
					delete(body, strings.TrimPrefix(fault, "missing-"))
				case "null-records", "null-total_records", "null-total_pages":
					body[strings.TrimPrefix(fault, "null-")] = nil
				case "negative-total":
					body["total_records"] = -1
				case "bool-total":
					body["total_records"] = true
				case "overflow-total":
					body["total_records"] = 10001
					body["total_pages"] = 101
				case "wrong-pages":
					body["total_pages"] = 1
				case "changing-total":
					if page == 2 {
						body["total_records"] = 102
					}
				case "short-page":
					body["records"] = rows[:len(rows)-1]
				case "empty-second":
					if page == 2 {
						body["records"] = []any{}
					}
				case "duplicate-fingerprint":
					if page == 1 {
						rows[1]["fingerprint"] = rows[0]["fingerprint"]
					}
				case "duplicate-across":
					if page == 2 {
						rows[0]["fingerprint"] = "0000000000000001"
					}
				case "changed-start-same-fingerprint":
					if page == 2 {
						rows[0]["fingerprint"] = "0000000000000001"
						rows[0]["starts_at"] = "2026-10-17T12:01:00Z"
					}
				case "duplicate-source-id":
					if page == 1 {
						rows[1]["alert_labels"].([]map[string]any)[4]["value"] = "source-1"
					}
				case "reverse-order":
					if page == 1 {
						rows[0], rows[1] = rows[1], rows[0]
					}
				case "invalid-fingerprint":
					rows[0]["fingerprint"] = "history-fingerprint"
				case "uppercase-fingerprint":
					rows[0]["fingerprint"] = "ABCDEF0000000001"
				case "missing-fingerprint":
					delete(rows[0], "fingerprint")
				case "null-fingerprint":
					rows[0]["fingerprint"] = nil
				case "missing-start":
					delete(rows[0], "starts_at")
				case "null-start":
					rows[0]["starts_at"] = nil
				case "zero-start":
					rows[0]["starts_at"] = "0001-01-01T00:00:00Z"
				case "date-only":
					rows[0]["starts_at"] = "2026-10-17"
				case "invalid-start":
					rows[0]["starts_at"] = "invalid"
				case "null-labels":
					rows[0]["alert_labels"] = nil
				case "missing-labels":
					delete(rows[0], "alert_labels")
				case "duplicate-label":
					rows[0]["alert_labels"] = append(labels, labels[0])
				case "null-label":
					rows[0]["alert_labels"] = []any{nil}
				case "null-label-value":
					labels[0]["value"] = nil
				case "number-label-value":
					labels[0]["value"] = 1
				case "empty-label-name":
					labels[0]["name"] = ""
				case "missing-required-label":
					rows[0]["alert_labels"] = labels[1:]
				case "foreign-run":
					labels[2]["value"] = "foreign"
				case "foreign-pack":
					labels[1]["value"] = "foreign"
				case "foreign-name":
					labels[0]["value"] = "foreign"
				case "wrong-top-name":
					rows[0]["alertname"] = "foreign"
				case "foreign-site":
					labels[3]["value"] = "foreign"
				case "foreign-severity":
					labels[5]["value"] = "critical"
				case "empty-source-id":
					labels[4]["value"] = " "
				case "synthetic-parent":
					rows[0]["correlation_parent_state"] = "ready"
				case "status503":
					w.WriteHeader(503)
					return
				}
				raw, _ := json.Marshal(body)
				switch fault {
				case "duplicate-json-key":
					raw = append([]byte(`{"total_records":0,`), raw[1:]...)
				case "unicode-alias":
					raw = []byte(strings.Replace(string(raw), `"starts_at":`, `"ſtarts_at":"2026-10-17T00:00:00Z","starts_at":`, 1))
				case "synthetic-parent-alias":
					raw = []byte(strings.Replace(string(raw), `"correlation_parent_state":null`, `"correlation_parent_state":"ready","correlation_parent_ſtate":null`, 1))
				case "trailing-json":
					raw = append(raw, []byte(`{}`)...)
				}
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			rows, err := installedActiveReader(t, server.URL).FindActive(context.Background(), "run-1")
			if err == nil || rows != nil {
				t.Fatalf("accepted %s rows=%d err=%v", fault, len(rows), err)
			}
			if calls > 2 {
				t.Fatal("unexpected retry")
			}
		})
	}
}
func TestInstalledPackActiveTransportAndInputGuards(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Redirect(w, r, "/foreign", 307) }))
	defer server.Close()
	reader := installedActiveReader(t, server.URL)
	if _, err := reader.FindActive(context.Background(), "run-1"); err == nil || calls != 1 {
		t.Fatal("followed redirect")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if _, _, err := reader.client.do(context.Background(), method, "/api/v1/alerts/active-alerts", nil, nil); err == nil {
			t.Fatal("allowed mutation")
		}
	}
	if _, _, err := reader.client.do(context.Background(), "GET", correlationAuditPath, nil, nil); err == nil {
		t.Fatal("allowed other route")
	}
	for _, run := range []string{"", " ", strings.Repeat("x", 129)} {
		if _, err := reader.FindActive(context.Background(), run); err == nil {
			t.Fatal("invalid run scope")
		}
	}
	if calls != 1 {
		t.Fatal("invalid requests reached peer")
	}
	for _, suffix := range []string{"/path", "?x=y", "#fragment"} {
		if _, err := NewInstalledPackActiveReader(nativeObserverTarget(server.URL+suffix), Options{}); err == nil {
			t.Fatal("accepted non-origin")
		}
	}
	target := nativeObserverTarget(server.URL)
	target.TLS.Insecure = true
	if _, err := NewInstalledPackActiveReader(target, Options{}); err == nil {
		t.Fatal("insecure TLS")
	}
	if _, err := NewInstalledPackActiveReader(nativeObserverTarget(server.URL), Options{HTTPClient: server.Client()}); err == nil {
		t.Fatal("injected client")
	}
}

func TestInstalledPackActiveResponseBoundAndCancellation(t *testing.T) {
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
			rows, err := installedActiveReader(t, server.URL).FindActive(ctx, "run-1")
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

func TestInstalledPackActiveVerifiedTLS(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"records":[],"total_records":0,"total_pages":0}`)
	}))
	defer server.Close()
	target := nativeObserverTarget(server.URL)
	reader, err := NewInstalledPackActiveReader(target, Options{Getenv: func(string) string { return "installed-read-key" }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.FindActive(context.Background(), "run-1"); err == nil || calls != 0 {
		t.Fatal("accepted untrusted TLS")
	}
	target.TLS.Insecure = true
	if _, err = NewInstalledPackActiveReader(target, Options{}); err == nil {
		t.Fatal("accepted insecure TLS")
	}
	target.TLS.Insecure = false
	target.TLS.CAPath = filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(target.TLS.CAPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err = NewInstalledPackActiveReader(target, Options{Getenv: func(string) string { return "installed-read-key" }})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := reader.FindActive(context.Background(), "run-1"); err != nil || rows == nil || len(rows) != 0 || calls != 1 {
		t.Fatalf("trusted TLS read failed: %v", err)
	}
}
