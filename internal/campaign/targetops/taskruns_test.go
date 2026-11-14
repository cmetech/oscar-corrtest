package targetops

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTaskRunsFiltersAndPages(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Write([]byte(`{"total_pages":2,"records":[{"name":"homelab_a","state":"SUCCESS","started":"2026-11-13T00:00:00Z","duration":1.5},{"name":"other","state":"SUCCESS"}]}`))
			return
		}
		w.Write([]byte(`{"total_pages":2,"records":[{"name":"homelab_a","state":"FAILURE","started":"2026-11-13T00:05:00Z"}]}`))
	}))
	runs, err := TaskRuns(context.Background(), c, []string{"homelab_a"}, time.Now().Add(-time.Hour), time.Now())
	if err != nil || len(runs) != 2 || runs[0].Duration != 1500*time.Millisecond || runs[1].Status != "FAILURE" {
		t.Fatal(runs, err)
	}
}
