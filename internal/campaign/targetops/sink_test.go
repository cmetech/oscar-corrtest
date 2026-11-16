package targetops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Foreign-run alerts (run10, other) and a receipt holding only foreign alerts must be excluded.
func TestSinkFiltersByCampaignRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Header.Get("Authorization") != "Bearer tok" || q.Get("run") != "" || q.Get("since") != "2026-11-13T00:00:00Z" || q.Get("capture") != "cap" {
			t.Errorf("bad request %v", q)
		}
		if q.Get("after_id") == "" {
			w.Write([]byte(`{"receipts":[{"received_at":"2026-11-13T00:00:00Z","alerts":[{"alertname":"A1","status":"firing","labels":{"campaign_run":"run1"}}]}],"next_after_id":7}`))
			return
		}
		if q.Get("after_id") != "7" {
			t.Errorf("after_id = %s", q.Get("after_id"))
		}
		w.Write([]byte(`{"receipts":[{"received_at":"2026-11-13T00:01:00Z","alerts":[{"alertname":"A1","status":"resolved","labels":{"campaign_run":"run1"}},{"alertname":"B","status":"resolved","labels":{"campaign_run":"run10"}}]},{"received_at":"2026-11-13T00:02:00Z","alerts":[{"alertname":"F","status":"firing","labels":{"campaign_run":"other"}}]}]}`))
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(tok, []byte("tok\n"), 0o600)
	s, err := NewSink(srv.URL, tok, "")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := s.Receipts(context.Background(), "cap", "run1", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if err != nil || len(rs) != 2 || len(rs[1].Alerts) != 1 || rs[0].Alerts[0].Labels["campaign_run"] != "run1" || rs[1].Alerts[0].Status != "resolved" {
		t.Fatal(rs, err)
	}
}
