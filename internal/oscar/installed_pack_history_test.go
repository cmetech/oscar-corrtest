package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func installedHistoryPage(page, total int) map[string]any {
	records := []map[string]any{}
	for i := (page - 1) * 100; i < page*100 && i < total; i++ {
		records = append(records, map[string]any{"ID": fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1), "alertname": "CorrTestOperationalFlood", "fingerprint": fmt.Sprintf("history-%d", i), "labels": []map[string]string{{"Label": "source_id", "Value": fmt.Sprintf("source-%d", i)}}, "uninterpreted": map[string]any{"preserved": true}})
	}
	return map[string]any{"records": records, "total_records": total, "total_pages": (total + 99) / 100, "page": page, "per_page": 100}
}

func TestInstalledHistoryPublicTLSAndRawPages(t *testing.T) {
	for _, prefix := range []string{"", "/ext/mw"} {
		t.Run(prefix, func(t *testing.T) {
			_, target, calls := installedPublicProxy(t, prefix, "history-key", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != historyPath || r.Header.Get("X-Forwarded-Prefix") != prefix || r.Header.Get("X-API-Key") != "history-key" {
					t.Error("public/backend route or auth differs")
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				_ = json.NewEncoder(w).Encode(installedHistoryPage(page, 101))
			}))
			reader, err := NewInstalledPackHistoryReader(target, Options{Getenv: func(string) string { return "history-key" }})
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.FindHistory(context.Background(), HistoryQuery{AlertName: "CorrTestOperationalFlood"})
			if err != nil || got == nil || len(got.Records) != 101 || len(got.Pages) != 2 || calls.Load() != 2 {
				t.Fatalf("incomplete observation %+v %v calls%d", got, err, calls.Load())
			}
			if !strings.Contains(string(got.Pages[0]), `"uninterpreted":{"preserved":true}`) || got.Records[0].Fingerprint != "history-0" {
				t.Fatal("raw fields lost or fingerprint inferred")
			}
			if prefix != "" {
				if _, err := NewExistingFeedHistoryReader(target, Options{}); err == nil {
					t.Fatal("legacy constructor broadened")
				}
			}
		})
	}
}

func TestInstalledHistoryIncompletePagesRefuseWithoutPartialEvidence(t *testing.T) {
	for _, fault := range []string{"missing", "null records", "short", "duplicate row", "duplicate across pages", "duplicate label", "wrong page", "changing total", "overflow", "duplicate key", "row key alias", "lowercase id", "extra JSON", "bad key", "redirect"} {
		t.Run(fault, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				v := installedHistoryPage(page, 101)
				records := v["records"].([]map[string]any)
				switch fault {
				case "missing":
					delete(v, "total_records")
				case "null records":
					v["records"] = nil
				case "short":
					v["records"] = []any{}
				case "duplicate row":
					if page == 1 {
						records[1]["ID"] = records[0]["ID"]
					}
				case "duplicate across pages":
					if page == 2 {
						records[0]["ID"] = "00000000-0000-4000-8000-000000000001"
					}
				case "duplicate label":
					records[0]["labels"] = []map[string]string{{"Label": "source_id", "Value": "a"}, {"Label": "source_id", "Value": "b"}}
				case "wrong page":
					v["page"] = page + 1
				case "changing total":
					if page == 2 {
						v["total_records"] = 102
					}
				case "overflow":
					v["total_records"] = 10001
				case "lowercase id":
					records[0]["id"] = records[0]["ID"]
					delete(records[0], "ID")
				case "row key alias":
					records[0]["id"] = records[0]["ID"]
				case "bad key":
					w.WriteHeader(401)
					return
				case "redirect":
					w.Header().Set("Location", r.URL.Path)
					w.WriteHeader(307)
					return
				}
				raw, _ := json.Marshal(v)
				if fault == "duplicate key" {
					raw = append([]byte(`{"page":99,`), raw[1:]...)
				}
				_, _ = w.Write(raw)
				if fault == "extra JSON" {
					_, _ = io.WriteString(w, `{}`)
				}
			}))
			defer server.Close()
			reader, err := NewInstalledPackHistoryReader(nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "history-key" }})
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.FindHistory(context.Background(), HistoryQuery{AlertName: "CorrTestOperationalFlood"})
			if err == nil || got != nil {
				t.Fatal("partial/ambiguous response accepted")
			}
			if fault == "redirect" && calls != 1 {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestInstalledHistoryTransportRefusesForeignRequests(t *testing.T) {
	route, err := installedBaseURL("https://api.example/ext/mw", "/ext/mw")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := installedHistoryReadTransport{route: route, base: existingFeedRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	for _, endpoint := range []string{"https://api.example/api/v1/alerts/history", "https://foreign.example/ext/mw/api/v1/alerts/history", "http://api.example/ext/mw/api/v1/alerts/history", "https://api.example/ext/mw/api/v1/alerts/active-alerts", "https://api.example/ext/mw/ext/mw/api/v1/alerts/history", "https://api.example/ext/mw/api/v1/alerts/%68istory", "https://api.example/ext/mw/api/v1/alerts/history#fragment"} {
		request, _ := http.NewRequest("GET", endpoint, nil)
		if _, err := transport.RoundTrip(request); err == nil {
			t.Fatalf("escaped route accepted %s", endpoint)
		}
	}
	for _, method := range []string{"POST", "DELETE"} {
		request, _ := http.NewRequest(method, "https://api.example/ext/mw"+historyPath, nil)
		if _, err := transport.RoundTrip(request); err == nil {
			t.Fatal("mutation allowed")
		}
	}
	request, _ := http.NewRequest("GET", "https://api.example/ext/mw"+historyPath, nil)
	request.Host = "foreign.example"
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("Host override allowed")
	}
	if calls != 0 {
		t.Fatal("forbidden request reached transport")
	}
	request.Host = "api.example"
	response, err := transport.RoundTrip(request)
	if err != nil || calls != 1 {
		t.Fatal("valid route refused")
	}
	_ = response.Body.Close()
}
