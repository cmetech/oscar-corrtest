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
}

// TaskRuns reads GET /api/v1/tasks/history for the window and keeps runs of the named jobs.
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
				Name     string     `json:"name"`
				State    string     `json:"state"`
				Started  *time.Time `json:"started"`
				Received *time.Time `json:"received"`
				Duration *float64   `json:"duration"`
			} `json:"records"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}, "order": {"asc"},
			"start_time": {start.UTC().Format(time.RFC3339)}, "end_time": {end.UTC().Format(time.RFC3339)}}
		if err := c.DoJSON(ctx, http.MethodGet, "/api/v1/tasks/history", q, nil, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Records {
			if !want[r.Name] {
				continue
			}
			run := TaskRun{Job: r.Name, Status: r.State}
			if r.Started != nil {
				run.StartedAt = *r.Started
			} else if r.Received != nil {
				run.StartedAt = *r.Received
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
