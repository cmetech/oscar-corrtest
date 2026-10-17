package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestExistingFeedHistoryReaderRequiresCompleteBoundedPages(t *testing.T) {
	for _, fault := range []string{"", "id-object", "id-bool", "id-array", "id-number", "id-empty", "id-uppercase", "id-version", "id-variant", "duplicate-same-page", "duplicate-across-pages", "changing-total", "wrong-page", "short-page", "missing-id", "duplicate-label", "overflow"} {
		t.Run(fault, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if r.Method != "GET" || r.URL.Path != "/api/v1/alerts/history" || r.Header.Get("X-API-Key") != "owned-history-key" || r.Header.Get("Authorization") != "" {
					t.Error("unexpected History transport")
				}
				records := []map[string]any{}
				count := 100
				if page == 2 {
					count = 1
				}
				for i := 0; i < count; i++ {
					records = append(records, map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012x", (page-1)*100+i+1), "alertname": "OrdinaryWindowProof", "labels": []map[string]string{{"label": "node", "value": fmt.Sprintf("node-%d", (page-1)*100+i)}}})
				}
				total := 101
				switch fault {
				case "id-object":
					records[0]["id"] = map[string]any{}
				case "id-bool":
					records[0]["id"] = true
				case "id-array":
					records[0]["id"] = []string{"id"}
				case "id-number":
					records[0]["id"] = 1
				case "id-empty":
					records[0]["id"] = ""
				case "id-uppercase":
					records[0]["id"] = "A0000000-0000-4000-8000-000000000001"
				case "id-version":
					records[0]["id"] = "00000000-0000-1000-8000-000000000001"
				case "id-variant":
					records[0]["id"] = "00000000-0000-4000-7000-000000000001"
				case "duplicate-same-page":
					if page == 1 {
						records[1]["id"] = records[0]["id"]
					}
				case "duplicate-across-pages":
					if page == 2 {
						records[0]["id"] = "00000000-0000-4000-8000-000000000001"
					}

				case "changing-total":
					if page == 2 {
						total = 102
					}
				case "wrong-page":
					page++
				case "short-page":
					records = records[:0]
				case "missing-id":
					delete(records[0], "id")
				case "duplicate-label":
					records[0]["labels"] = []map[string]string{{"label": "node", "value": "one"}, {"label": "node", "value": "two"}}
				case "overflow":
					total = 10001
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"records": records, "total_records": total, "total_pages": (total + 99) / 100, "page": page, "per_page": 100})
			}))
			defer server.Close()
			reader, err := NewExistingFeedHistoryReader(nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "owned-history-key" }})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := reader.FindHistory(context.Background(), HistoryQuery{AlertName: "OrdinaryWindowProof"})
			if fault == "" {
				if err != nil || len(rows) != 101 || calls != 2 {
					t.Fatalf("pagination rows=%d calls=%d error=%v", len(rows), calls, err)
				}
			} else if err == nil {
				t.Fatal("ambiguous page accepted")
			}
		})
	}
}
