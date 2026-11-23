package targetops

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagedReceiptsEnforcesExactAdmissionWindow(t *testing.T) {
	now := time.Date(2026, 11, 22, 5, 0, 0, 500000000, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/receipts" || r.URL.Query().Get("capture") != "campaign" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"receipts":[{"receipt_id":1,"received_at":"2026-11-22T05:00:00.499Z","alerts":[{"labels":{"aid":"source"}}]},{"receipt_id":2,"received_at":"2026-11-22T05:00:00.501Z","alerts":[{"history_id":"owned","starts_at":"2026-11-22T05:00:00Z","status":"firing","labels":{"aid":"source"}},{"labels":{"aid":"other"}}]}]}`)
	}))
	defer srv.Close()
	s := Sink{base: srv.URL, hc: srv.Client()}
	rows, err := s.ManagedReceipts(context.Background(), "source", now)
	if err != nil || len(rows) != 1 || rows[0].ID != 2 || len(rows[0].Alerts) != 1 || rows[0].Alerts[0].HistoryID != "owned" {
		t.Fatal(rows, err)
	}
}
