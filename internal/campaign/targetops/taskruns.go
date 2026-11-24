package targetops

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

type TaskRun struct {
	Job       string
	Status    string
	StartedAt time.Time
	Duration  time.Duration
	// Scheduled: the scheduler started the run (history trigger_source
	// "scheduled"); user and unattributed runs are not.
	Scheduled bool `json:",omitempty"`
}

// TaskRuns reads GET /api/v1/tasks/history for the window and keeps runs of the named jobs.
// A row names its job in "alias" ("name" is the Celery task, e.g. tm.tasks.common);
// "name" is matched only when alias is empty. Time falls back started → received → timestamp.
// Results are sorted by the record "timestamp" column, and start/end filter on that same timestamp.
func TaskRuns(ctx context.Context, c *oscar.Client, jobNames []string, start, end time.Time) ([]TaskRun, error) {
	want := map[string]bool{}
	for _, j := range jobNames {
		want[j] = true
	}
	var out []TaskRun
	for page := 1; page <= 500; page++ {
		var resp struct {
			TotalPages int `json:"total_pages"`
			Records    []struct {
				Name      string     `json:"name"`
				Alias     string     `json:"alias"`
				State     string     `json:"state"`
				Started   *time.Time `json:"started"`
				Received  *time.Time `json:"received"`
				Timestamp *time.Time `json:"timestamp"`
				Duration  *float64   `json:"duration"`
				Source    string     `json:"trigger_source"`
			} `json:"records"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}, "order": {"asc"}, "column": {"timestamp"},
			"start_time": {start.UTC().Format(time.RFC3339)}, "end_time": {end.UTC().Format(time.RFC3339)}}
		if err := c.DoJSON(ctx, http.MethodGet, "/api/v1/tasks/history", q, nil, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Records {
			job := r.Alias
			if job == "" {
				job = r.Name
			}
			if !want[job] {
				continue
			}
			run := TaskRun{Job: job, Status: r.State, Scheduled: r.Source == "scheduled"}
			switch {
			case r.Started != nil:
				run.StartedAt = *r.Started
			case r.Received != nil:
				run.StartedAt = *r.Received
			case r.Timestamp != nil:
				run.StartedAt = *r.Timestamp
			}
			if r.Duration != nil {
				run.Duration = time.Duration(*r.Duration * float64(time.Second))
			}
			out = append(out, run)
		}
		if len(resp.Records) == 0 || page >= resp.TotalPages {
			return out, nil
		}
	}
	return nil, fmt.Errorf("task history exceeded page limit")
}
