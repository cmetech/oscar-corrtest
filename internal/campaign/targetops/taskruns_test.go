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

// Live OSCAR rows carry the job in "alias" (name is the Celery task) and often
// only "timestamp" for time.
func TestTaskRunsMatchesAliasAndFallsBackToTimestamp(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_pages":1,"records":[{"name":"tm.tasks.common","alias":"monitor:check_connection:homelab","state":"SUCCESS","started":null,"received":null,"timestamp":"2026-11-15T05:00:15+00:00"},{"name":"tm.tasks.common","alias":"dg:generate_alerts","state":"SUCCESS","timestamp":"2026-11-15T05:00:00+00:00"}]}`))
	}))
	runs, err := TaskRuns(context.Background(), c, []string{"monitor:check_connection:homelab"}, time.Now().Add(-time.Hour), time.Now())
	want := time.Date(2026, 11, 15, 5, 0, 15, 0, time.UTC)
	if err != nil || len(runs) != 1 || runs[0].Job != "monitor:check_connection:homelab" || !runs[0].StartedAt.Equal(want) {
		t.Fatal(runs, err)
	}
}

// Only rows the scheduler started are Scheduled: user (the v4 lane's fo-01)
// and unattributed rows are not (OSC-156 AC-2 counts only scheduled runs).
func TestTaskRunsMarksScheduledRuns(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_pages":1,"records":[{"alias":"monitor:check_connection:homelab","state":"SUCCESS","timestamp":"2026-11-15T05:00:00+00:00","trigger_source":"scheduled"},` +
			`{"alias":"monitor:check_connection:homelab","state":"SUCCESS","timestamp":"2026-11-15T05:01:00+00:00","trigger_source":"api","triggered_by_user_id":"5b0c9a5e-1c1d-4c3f-9a3c-0d6f3c0e2a11"},` +
			`{"alias":"monitor:check_connection:homelab","state":"SUCCESS","timestamp":"2026-11-15T05:02:00+00:00"}]}`))
	}))
	runs, err := TaskRuns(context.Background(), c, []string{"monitor:check_connection:homelab"}, time.Now().Add(-time.Hour), time.Now())
	if err != nil || len(runs) != 3 || !runs[0].Scheduled || runs[1].Scheduled || runs[2].Scheduled {
		t.Fatal(runs, err)
	}
}
